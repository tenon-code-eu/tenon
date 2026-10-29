package tui

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/i18n"
	"github.com/tenon-code-eu/tenon/internal/llm"
)

// sidebarOpts : session reprise (le fil s'affiche d'emblée), deux
// fournisseurs configurés.
func sidebarOpts(l i18n.Lang) Options {
	o := statusOpts(l)
	o.Resumed = true
	o.Providers = map[string]llm.ProviderInfo{
		"mistral": {ID: "mistral", Name: "Mistral", Country: "FR", Jurisdiction: llm.EU},
		"openai":  {ID: "openai", Name: "OpenAI", Country: "US", Jurisdiction: llm.NonEU},
	}
	return o
}

func panelOpen(m Model) bool {
	return strings.Contains(m.View(), i18n.T(m.lang, "tui.panel.sovereignty"))
}

func assertFits(t *testing.T, m Model, what string) {
	t.Helper()
	for i, l := range strings.Split(m.View(), "\n") {
		if w := lipgloss.Width(l); w > m.width {
			t.Fatalf("%s : ligne %d de %d colonnes (largeur %d) : %q", what, i, w, m.width, l)
		}
	}
}

func ctrlB(h *harness) { h.send(tea.KeyMsg{Type: tea.KeyCtrlB}) }

func TestSidebarAutoOpen(t *testing.T) {
	h := newHarness(t, newFake(), sidebarOpts(i18n.FR))
	h.send(tea.WindowSizeMsg{Width: 99, Height: 30})
	if panelOpen(h.m) {
		t.Fatal("panneau ouvert à 99 colonnes")
	}
	h.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	if !panelOpen(h.m) {
		t.Fatalf("panneau fermé à 100 colonnes :\n%s", h.m.View())
	}
	// Panneau de 26 colonnes, précédé d'un trait de séparation.
	first := []rune(ansi.Strip(strings.Split(h.m.View(), "\n")[0]))
	if len(first) != 100 || first[100-sidebarWidth-1] != '│' {
		t.Fatalf("séparateur absent à la colonne %d : %q", 100-sidebarWidth-1, string(first))
	}
	assertFits(t, h.m, "100")

	// Sans choix manuel : fermé sous 100, rouvert au-dessus.
	h.send(tea.WindowSizeMsg{Width: 120, Height: 30})
	h.send(tea.WindowSizeMsg{Width: 60, Height: 30})
	if panelOpen(h.m) {
		t.Fatal("panneau ouvert à 60 colonnes sans choix manuel")
	}
	assertFits(t, h.m, "60")
	h.send(tea.WindowSizeMsg{Width: 120, Height: 30})
	if !panelOpen(h.m) {
		t.Fatal("panneau fermé à 120 colonnes")
	}

	// Ctrl+B ferme ; le choix survit à 120 → 60 → 120.
	ctrlB(h)
	if panelOpen(h.m) {
		t.Fatal("Ctrl+B n'a pas fermé le panneau")
	}
	for _, w := range []int{60, 120} {
		h.send(tea.WindowSizeMsg{Width: w, Height: 30})
		if panelOpen(h.m) {
			t.Fatalf("largeur %d : choix manuel (fermé) perdu", w)
		}
		assertFits(t, h.m, fmt.Sprint(w))
	}
	// Ctrl+B rouvre ; ouvert même à 60 colonnes, jamais plus large que le terminal.
	ctrlB(h)
	for _, w := range []int{120, 60, 120} {
		h.send(tea.WindowSizeMsg{Width: w, Height: 30})
		if !panelOpen(h.m) {
			t.Fatalf("largeur %d : choix manuel (ouvert) perdu", w)
		}
		assertFits(t, h.m, fmt.Sprint(w))
	}
	// Trop étroit pour le fil et le panneau : masqué, sans débordement.
	h.send(tea.WindowSizeMsg{Width: 40, Height: 30})
	if panelOpen(h.m) {
		t.Fatal("panneau affiché à 40 colonnes")
	}
	assertFits(t, h.m, "40")
}

// Le panneau n'apparaît pas sur l'accueil.
func TestSidebarHiddenOnHome(t *testing.T) {
	o := sidebarOpts(i18n.EN)
	o.Resumed = false
	h := newHarness(t, newFake(), o)
	h.send(tea.WindowSizeMsg{Width: 140, Height: 30})
	if panelOpen(h.m) {
		t.Fatal("panneau affiché sur l'accueil")
	}
}

func panelText(m Model) string {
	return strings.Join(m.sidebarLines(m.convHeight()), "\n")
}

func TestSessionStats(t *testing.T) {
	f := newFake()
	h := newHarness(t, f, sidebarOpts(i18n.FR))
	h.send(tea.WindowSizeMsg{Width: 120, Height: 40})
	f.emit(t, event.UserMessage, event.UserMessagePayload{Text: "corrige le test"})
	f.emit(t, event.Usage, event.UsagePayload{Provider: "mistral", ProviderName: "Mistral", Jurisdiction: llm.EU, Purpose: "turn", Input: 10000, Output: 500, Window: 128000})
	// Sous-agent : compte comme envoi, ne bouge pas la jauge.
	f.emit(t, event.Usage, event.UsagePayload{Provider: "mistral", ProviderName: "Mistral", Jurisdiction: llm.EU, Purpose: "subagent", Input: 120000, Output: 100, Window: 128000, ParentCallID: "c9"})
	// Bascule vers un fournisseur hors UE : dernier appel de l'agent principal.
	f.emit(t, event.Usage, event.UsagePayload{Provider: "openai", ProviderName: "OpenAI", Jurisdiction: llm.NonEU, Purpose: "turn", Input: 39680, Output: 400, Window: 128000})
	h.until("envois", func() bool { return strings.Contains(panelText(h.m), "● 1 → OpenAI · US") })
	m := colored(h.m)
	p := panelText(m)
	for _, want := range []string{"● 2 → Mistral · FR", "███░░░░░░░ 31 %", "entrée 170 k · sortie 1 k"} {
		if !strings.Contains(ansi.Strip(p), want) {
			t.Fatalf("%q absent du panneau :\n%s", want, ansi.Strip(p))
		}
	}
	if !strings.Contains(p, m.st.alert.Render("● 1 → OpenAI · US")) {
		t.Fatalf("envoi hors UE pas en couleur d'alerte :\n%q", p)
	}
	if strings.Contains(p, m.st.alert.Render("● 2 → Mistral · FR")) {
		t.Fatalf("envoi UE en couleur d'alerte :\n%q", p)
	}
	// Ordre : titre, contexte, jetons, fichiers, souveraineté.
	plain := ansi.Strip(p)
	order := []string{"corrige le test", "███", "entrée", i18n.T(i18n.FR, "tui.panel.files"), i18n.T(i18n.FR, "tui.panel.sovereignty"), "● 2"}
	last := -1
	for _, s := range order {
		i := strings.Index(plain, s)
		if i <= last {
			t.Fatalf("ordre : %q à %d (précédent %d) :\n%s", s, i, last, plain)
		}
		last = i
	}
	for i, l := range m.sidebarLines(m.convHeight()) {
		if w := lipgloss.Width(l); w != sidebarWidth {
			t.Fatalf("ligne %d du panneau de %d colonnes : %q", i, w, l)
		}
	}
}

func fileEvents(t *testing.T, f *fakeEngine, id, tool, path string, args map[string]any, before string) {
	t.Helper()
	raw, _ := json.Marshal(args)
	f.emit(t, event.ToolCallRequested, event.ToolCallRequestedPayload{CallID: id, Tool: tool, Args: raw})
	f.emit(t, event.ToolResult, event.ToolResultPayload{CallID: id, Tool: tool, Content: "ok", Path: path, HashBefore: before, HashAfter: "sha256:after-" + id})
}

func TestSidebarFiles(t *testing.T) {
	root := t.TempDir()
	o := sidebarOpts(i18n.EN)
	o.ProjectRoot = root
	f := newFake()
	h := newHarness(t, f, o)
	h.send(tea.WindowSizeMsg{Width: 120, Height: 50})
	a := filepath.Join(root, "internal", "a.go")
	b := filepath.Join(root, "b.go")
	fileEvents(t, f, "c1", "edit_file", a, map[string]any{"path": a, "old_string": "x", "new_string": "y\nz"}, "sha256:before")
	fileEvents(t, f, "c2", "write_file", b, map[string]any{"path": b, "content": "l1\nl2\nl3\n"}, "")
	fileEvents(t, f, "c3", "edit_file", a, map[string]any{"path": a, "old_string": "y", "new_string": "w"}, "sha256:after-c1")
	// Lecture et échec : aucun fichier modifié.
	f.emit(t, event.ToolResult, event.ToolResultPayload{CallID: "c4", Tool: "read_file", Path: filepath.Join(root, "r.go"), Content: "x"})
	f.emit(t, event.ToolResult, event.ToolResultPayload{CallID: "c5", Tool: "edit_file", Path: filepath.Join(root, "e.go"), IsError: true, Content: "text not found"})
	h.until("fichiers", func() bool { return strings.Contains(panelText(h.m), "+3 −2") })
	if got := h.m.stats.Files; len(got) != 2 || got[0].Op != "M" || got[1].Op != "A" {
		t.Fatalf("fichiers : %+v", got)
	}
	p := panelText(h.m)
	for _, want := range []string{"M internal/a.go +3 −2", "A b.go +3"} {
		if !strings.Contains(p, want) {
			t.Fatalf("%q absent :\n%s", want, p)
		}
	}
	if strings.Contains(p, "r.go") || strings.Contains(p, "e.go") {
		t.Fatalf("lecture ou échec comptés :\n%s", p)
	}
	// Au plus 8 fichiers, puis « +N ».
	for i := range 10 {
		n := filepath.Join(root, fmt.Sprintf("n%d.go", i))
		fileEvents(t, f, fmt.Sprintf("w%d", i), "write_file", n, map[string]any{"path": n, "content": "x"}, "")
	}
	h.until("+N", func() bool { return strings.Contains(panelText(h.m), "+4") })
	if c := strings.Count(panelText(h.m), ".go"); c != sidebarMaxFiles {
		t.Fatalf("%d fichiers affichés, attendu %d :\n%s", c, sidebarMaxFiles, panelText(h.m))
	}
}

// badgeAfter renvoie la ligne qui suit la réponse text dans la vue.
func badgeAfter(t *testing.T, m Model, text string) string {
	t.Helper()
	lines := m.conversationLines()
	for i, l := range lines {
		if strings.Contains(ansi.Strip(l), text) && i+1 < len(lines) {
			return lines[i+1]
		}
	}
	t.Fatalf("réponse %q absente :\n%s", text, strings.Join(lines, "\n"))
	return ""
}

func TestBadgeFallback(t *testing.T) {
	f := newFake()
	h := newHarness(t, f, sidebarOpts(i18n.FR))
	h.send(tea.WindowSizeMsg{Width: 120, Height: 40})
	// Tour 1 : Mistral (UE).
	f.emit(t, event.UserMessage, event.UserMessagePayload{Text: "un"})
	f.emit(t, event.Usage, event.UsagePayload{Provider: "mistral", ProviderName: "Mistral", Jurisdiction: llm.EU, Purpose: "turn", Input: 10, Output: 5})
	f.emit(t, event.AssistantMessage, event.AssistantMessagePayload{Text: "réponse une"})
	f.emit(t, event.TurnCompleted, event.TurnCompletedPayload{Reason: "done"})
	// Tour 2 : bascule en cours de tour vers OpenAI (hors UE) ; un sous-agent
	// passé ensuite par Mistral ne change pas le badge.
	f.emit(t, event.UserMessage, event.UserMessagePayload{Text: "deux"})
	f.emit(t, event.Usage, event.UsagePayload{Provider: "mistral", ProviderName: "Mistral", Jurisdiction: llm.EU, Purpose: "turn", Input: 10, Output: 5})
	f.emit(t, event.Usage, event.UsagePayload{Provider: "openai", ProviderName: "OpenAI", Jurisdiction: llm.NonEU, Purpose: "turn", Input: 10, Output: 5})
	f.emit(t, event.Usage, event.UsagePayload{Provider: "mistral", ProviderName: "Mistral", Jurisdiction: llm.EU, Purpose: "subagent", Input: 10, Output: 5, ParentCallID: "c1"})
	f.emit(t, event.AssistantMessage, event.AssistantMessagePayload{Text: "réponse deux"})
	f.emit(t, event.TurnCompleted, event.TurnCompletedPayload{Reason: "done"})
	h.until("tour 2", func() bool { return strings.Contains(h.m.View(), "OpenAI · US · hors UE") })
	m := colored(h.m)
	if b := badgeAfter(t, m, "réponse une"); ansi.Strip(b) != "Mistral · FR · UE" || strings.Contains(b, m.st.alert.Render("Mistral · FR · UE")) {
		t.Fatalf("badge UE : %q", b)
	}
	if b := badgeAfter(t, m, "réponse deux"); b != m.st.alert.Render("OpenAI · US · hors UE") {
		t.Fatalf("badge hors UE : %q, attendu %q", b, m.st.alert.Render("OpenAI · US · hors UE"))
	}
	// Juridiction inconnue (fournisseur non configuré) : en alerte, sans pays.
	f.emit(t, event.UserMessage, event.UserMessagePayload{Text: "trois"})
	f.emit(t, event.Usage, event.UsagePayload{Provider: "autre", ProviderName: "Autre", Purpose: "turn", Input: 10, Output: 5})
	f.emit(t, event.AssistantMessage, event.AssistantMessagePayload{Text: "réponse trois"})
	f.emit(t, event.TurnCompleted, event.TurnCompletedPayload{Reason: "done"})
	h.until("tour 3", func() bool { return strings.Contains(h.m.View(), "Autre · hors UE") })
	m = colored(h.m)
	if b := badgeAfter(t, m, "réponse trois"); b != m.st.alert.Render("Autre · hors UE") {
		t.Fatalf("badge inconnu : %q", b)
	}
	// Le panneau montre aussi le fournisseur qui a répondu, en ambre.
	if p := panelText(m); !strings.Contains(p, m.st.alert.Render("● 1 → OpenAI · US")) {
		t.Fatalf("panneau :\n%q", p)
	}
}

func TestSidebarRedacts(t *testing.T) {
	root := t.TempDir()
	o := redactOpts(t, root)
	o.Resumed = true
	o.SessionID = "sess-42"
	h := newHarness(t, newFake(), o)
	f := h.m.eng.(*fakeEngine)
	h.send(tea.WindowSizeMsg{Width: 140, Height: 40})
	f.emit(t, event.UserMessage, event.UserMessagePayload{Text: "titre " + secretLine + " \x1b[31mrouge"})
	path := filepath.Join(root, "dir \x1b[31m "+secretLine)
	fileEvents(t, f, "c1", "write_file", path, map[string]any{"path": path, "content": "x"}, "")
	f.emit(t, event.Usage, event.UsagePayload{Provider: "p", ProviderName: "Prov \x1b[31m" + secretLine, Jurisdiction: llm.EU, Purpose: "turn", Input: 10, Output: 5})
	f.emit(t, event.AssistantMessage, event.AssistantMessagePayload{Text: "fini"})
	f.emit(t, event.TurnCompleted, event.TurnCompletedPayload{Reason: "done"})
	h.until("badge", func() bool { return strings.Contains(h.m.View(), "fini") && !h.m.tr.Busy() })
	v := h.m.View()
	p := panelText(h.m)
	for what, s := range map[string]string{"vue": v, "panneau": p, "badge": badgeAfter(t, h.m, "fini")} {
		assertNoSecret(t, what, s)
		if strings.Contains(s, "\x1b[31m") {
			t.Fatalf("%s : séquence d'échappement conservée :\n%q", what, s)
		}
	}
	if !strings.Contains(p, "titre") || !strings.Contains(p, "A dir") {
		t.Fatalf("titre ou fichier absent :\n%s", p)
	}
}

// Session, politique et décomptes (skills, agents, hooks) restent visibles
// depuis que l'accueil a remplacé l'écran de bienvenue.
func TestSidebarSessionPolicyCounts(t *testing.T) {
	for l, want := range map[i18n.Lang][]string{
		i18n.FR: {"session sess-42", "politique : acme-dev v3", "skills 0 · agents 0", "profil : eu-only", "isolé"},
		i18n.EN: {"session sess-42", "politique : acme-dev v3", "skills 0 · agents 0", "profile: eu-only", "sandboxed"},
	} {
		o := sidebarOpts(l)
		o.Policy = "politique : acme-dev v3 (clé abcdef0123456789)"
		o.Profile = "eu-only"
		o.Sandbox = SandboxOn
		h := newHarness(t, newFake(), o)
		h.send(tea.WindowSizeMsg{Width: 120, Height: 40})
		p := panelText(h.m)
		for _, s := range want {
			if !strings.Contains(p, s) {
				t.Fatalf("%s : %q absent :\n%s", l, s, p)
			}
		}
		// Les décomptes occupent le bas du panneau, coupés aux « · » faute de
		// largeur.
		lines := h.m.sidebarLines(h.m.convHeight())
		if !strings.Contains(lines[len(lines)-2], "skills 0 · agents 0") || !strings.Contains(lines[len(lines)-1], "hooks 0") {
			t.Fatalf("%s : dernière ligne : %q", l, lines[len(lines)-1])
		}
		if !strings.Contains(h.m.View(), "sess-42") {
			t.Fatalf("%s : session absente de la vue", l)
		}
	}
}
