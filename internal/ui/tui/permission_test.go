package tui

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"codeberg.org/tenon/tenon/internal/engine"
	"codeberg.org/tenon/tenon/internal/event"
	"codeberg.org/tenon/tenon/internal/i18n"
	"codeberg.org/tenon/tenon/internal/llm"
)

func runeKey(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }

// askPerm émet une demande de permission et attend qu'elle soit affichée.
func askPerm(t *testing.T, h *harness, f *fakeEngine, p event.PermissionRequestedPayload) {
	t.Helper()
	n := len(h.m.prompts)
	f.emit(t, event.PermissionRequested, p)
	h.until("demande affichée", func() bool { return len(h.m.prompts) == n+1 })
}

func shellPerm(id string) event.PermissionRequestedPayload {
	return event.PermissionRequestedPayload{PermissionID: id, Kind: "tool", Tool: "shell", Command: "ls -la"}
}

func wantReply(t *testing.T, f *fakeEngine, id string, want engine.Reply) {
	t.Helper()
	got, ok := f.reply(id)
	if !ok {
		t.Fatalf("aucune réponse pour %s", id)
	}
	if got != want {
		t.Fatalf("réponse %s = %+v, attendu %+v", id, got, want)
	}
}

func TestPermissionYesOnce(t *testing.T) {
	f := newFake()
	h := newHarness(t, f, Options{Lang: i18n.FR})
	askPerm(t, h, f, shellPerm("p1"))
	v := h.m.View()
	for _, want := range []string{"shell", "ls -la", i18n.T(i18n.FR, "tui.perm.keys_tool")} {
		if !strings.Contains(v, want) {
			t.Fatalf("vue sans %q : %q", want, v)
		}
	}
	h.send(runeKey('o'))
	wantReply(t, f, "p1", engine.Reply{Allow: true})
	if h.m.prompting() {
		t.Fatal("demande toujours affichée après la réponse")
	}
}

func TestPermissionSession(t *testing.T) {
	f := newFake()
	h := newHarness(t, f, Options{Lang: i18n.FR})
	askPerm(t, h, f, shellPerm("p1"))
	h.send(runeKey('s'))
	wantReply(t, f, "p1", engine.Reply{Allow: true, Remember: true})
}

func TestPermissionNoAndEsc(t *testing.T) {
	f := newFake()
	h := newHarness(t, f, Options{Lang: i18n.FR})
	askPerm(t, h, f, shellPerm("p1"))
	h.send(runeKey('n'))
	wantReply(t, f, "p1", engine.Reply{Allow: false})
	askPerm(t, h, f, shellPerm("p2"))
	h.send(tea.KeyMsg{Type: tea.KeyEsc})
	wantReply(t, f, "p2", engine.Reply{Allow: false})
	if h.m.prompting() {
		t.Fatal("demande toujours affichée")
	}
}

func TestEnglishKeys(t *testing.T) {
	f := newFake()
	h := newHarness(t, f, Options{Lang: i18n.EN})
	askPerm(t, h, f, shellPerm("p1"))
	if v := h.m.View(); !strings.Contains(v, "y: yes once · a: always for this session · n: no") {
		t.Fatalf("touches EN absentes : %q", v)
	}
	h.send(runeKey('o'))
	h.send(runeKey('s'))
	if _, ok := f.reply("p1"); ok {
		t.Fatal("o/s acceptés en anglais")
	}
	h.send(runeKey('y'))
	wantReply(t, f, "p1", engine.Reply{Allow: true})
	askPerm(t, h, f, shellPerm("p2"))
	h.send(runeKey('a'))
	wantReply(t, f, "p2", engine.Reply{Allow: true, Remember: true})
	askPerm(t, h, f, shellPerm("p3"))
	h.send(runeKey('n'))
	wantReply(t, f, "p3", engine.Reply{Allow: false})
}

func TestOtherKeysIgnored(t *testing.T) {
	f := newFake()
	h := newHarness(t, f, Options{Lang: i18n.FR})
	typeText(h, "brouillon")
	askPerm(t, h, f, shellPerm("p1"))
	for _, k := range []tea.KeyMsg{runeKey('x'), runeKey('y'), runeKey('O'), {Type: tea.KeyEnter}, {Type: tea.KeySpace}, {Type: tea.KeyRunes, Runes: []rune("on")}} {
		h.send(k)
	}
	if got := f.answered(); len(got) != 0 {
		t.Fatalf("réponses inattendues : %v", got)
	}
	if !h.m.prompting() {
		t.Fatal("demande fermée par une touche ignorée")
	}
	if v := h.m.input.Value(); v != "brouillon" {
		t.Fatalf("saisie modifiée pendant la demande : %q", v)
	}
	if n := len(f.submitted()); n != 0 {
		t.Fatalf("Entrée a envoyé la saisie pendant la demande (%d)", n)
	}
}

func TestJurisdictionNoRemember(t *testing.T) {
	f := newFake()
	h := newHarness(t, f, Options{Lang: i18n.FR})
	prov := &llm.ProviderInfo{ID: "openai", Name: "OpenAI", Country: "US", Jurisdiction: llm.NonEU}
	askPerm(t, h, f, event.PermissionRequestedPayload{PermissionID: "j1", Kind: "jurisdiction", Provider: prov})
	v := h.m.View()
	for _, want := range []string{"OpenAI", "US", "hors UE", i18n.T(i18n.FR, "tui.perm.keys_jurisdiction")} {
		if !strings.Contains(v, want) {
			t.Fatalf("vue sans %q : %q", want, v)
		}
	}
	h.send(runeKey('s'))
	h.send(runeKey('a'))
	if _, ok := f.reply("j1"); ok {
		t.Fatal("« pour la session » accepté pour une juridiction")
	}
	h.send(runeKey('o'))
	wantReply(t, f, "j1", engine.Reply{Allow: true})
}

func TestDiffShownForWrite(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte("un\nancien\ntrois\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := newFake()
	h := newHarness(t, f, Options{Lang: i18n.FR, ProjectRoot: root})
	h.send(tea.WindowSizeMsg{Width: 80, Height: 30})
	args, _ := json.Marshal(map[string]string{"path": "f.txt", "content": "un\nnouveau\ntrois\n"})
	f.emit(t, event.ToolCallRequested, event.ToolCallRequestedPayload{CallID: "c1", Tool: "write_file", Args: args})
	askPerm(t, h, f, event.PermissionRequestedPayload{
		PermissionID: "p1", Kind: "tool", Tool: "write_file", Paths: []string{filepath.Join(root, "f.txt")},
	})
	v := h.m.View()
	for _, want := range []string{"- ancien", "+ nouveau", "f.txt"} {
		if !strings.Contains(v, want) {
			t.Fatalf("vue sans %q : %q", want, v)
		}
	}
}

func TestDiffNotShownForOtherPath(t *testing.T) {
	root := t.TempDir()
	f := newFake()
	h := newHarness(t, f, Options{Lang: i18n.FR, ProjectRoot: root})
	h.send(tea.WindowSizeMsg{Width: 80, Height: 30})
	args, _ := json.Marshal(map[string]string{"path": "a.txt", "content": "secret\n"})
	f.emit(t, event.ToolCallRequested, event.ToolCallRequestedPayload{CallID: "c1", Tool: "write_file", Args: args})
	askPerm(t, h, f, event.PermissionRequestedPayload{
		PermissionID: "p1", Kind: "tool", Tool: "write_file", Paths: []string{filepath.Join(root, "b.txt")},
	})
	if v := h.m.View(); strings.Contains(v, "+ secret") {
		t.Fatalf("aperçu d'un autre appel affiché : %q", v)
	}
}

func TestDiffMoreLinesAndSanitized(t *testing.T) {
	root := t.TempDir()
	f := newFake()
	h := newHarness(t, f, Options{Lang: i18n.FR, ProjectRoot: root})
	h.send(tea.WindowSizeMsg{Width: 80, Height: 30})
	var b strings.Builder
	b.WriteString("evil\x1b[2J\n")
	for i := 0; i < 500; i++ {
		b.WriteString("ligne\n")
	}
	name := "n\x1b]0;x\x07.txt"
	args, _ := json.Marshal(map[string]string{"path": name, "content": b.String()})
	f.emit(t, event.ToolCallRequested, event.ToolCallRequestedPayload{CallID: "c1", Tool: "write_file", Args: args})
	askPerm(t, h, f, event.PermissionRequestedPayload{
		PermissionID: "p1", Kind: "tool", Tool: "write_file", Paths: []string{filepath.Join(root, name)},
	})
	v := h.m.View()
	if strings.Contains(v, "\x1b[2J") || strings.Contains(v, "\x1b]0") {
		t.Fatalf("séquence de contrôle transmise : %q", v)
	}
	if !strings.Contains(v, "+ evil[2J") {
		t.Fatalf("première ligne du diff absente : %q", v)
	}
	if !strings.Contains(v, "lignes de plus)") {
		t.Fatalf("mention des lignes omises absente : %q", v)
	}
	assertWidth(t, v, 80)
	if n := len(strings.Split(v, "\n")); n > 30 {
		t.Fatalf("%d lignes > hauteur 30", n)
	}
	// La touche de réponse reste visible même sur un petit écran.
	h.send(tea.WindowSizeMsg{Width: 30, Height: 6})
	v = h.m.View()
	assertWidth(t, v, 30)
	if !strings.Contains(v, "n : non") {
		t.Fatalf("touches masquées sur petit écran : %q", v)
	}
}

func TestPermissionKeepsDraft(t *testing.T) {
	f := newFake()
	h := newHarness(t, f, Options{Lang: i18n.FR})
	typeText(h, "brouillon")
	askPerm(t, h, f, shellPerm("p1"))
	if strings.Contains(h.m.View(), "brouillon") {
		t.Fatal("la saisie reste affichée pendant la demande")
	}
	h.send(runeKey('n'))
	if v := h.m.input.Value(); v != "brouillon" {
		t.Fatalf("saisie %q, attendu « brouillon »", v)
	}
	if !strings.Contains(h.m.View(), "brouillon") {
		t.Fatal("la saisie ne réapparaît pas après la réponse")
	}
}

func TestQueuedPermissions(t *testing.T) {
	f := newFake()
	h := newHarness(t, f, Options{Lang: i18n.FR})
	askPerm(t, h, f, shellPerm("p1"))
	askPerm(t, h, f, event.PermissionRequestedPayload{PermissionID: "p2", Kind: "tool", Tool: "shell", Command: "pwd"})
	if v := h.m.View(); !strings.Contains(v, "ls -la") || strings.Contains(v, "pwd") {
		t.Fatalf("la première demande n'est pas affichée seule : %q", v)
	}
	h.send(runeKey('o'))
	if v := h.m.View(); !strings.Contains(v, "pwd") {
		t.Fatalf("seconde demande non affichée : %q", v)
	}
	h.send(runeKey('n'))
	got := f.answered()
	if len(got) != 2 || got[0] != "p1" || got[1] != "p2" {
		t.Fatalf("ordre des réponses %v", got)
	}
	wantReply(t, f, "p1", engine.Reply{Allow: true})
	wantReply(t, f, "p2", engine.Reply{Allow: false})
}

func TestCtrlCDuringPermission(t *testing.T) {
	f := newFake()
	f.onSubmit = blockUntilCancel
	h := newHarness(t, f, Options{Lang: i18n.FR})
	typeText(h, "long")
	enter(h)
	h.until("Submit appelé", func() bool { return f.lastCtx() != nil })
	askPerm(t, h, f, shellPerm("p1"))
	if isQuit(h.update(tea.KeyMsg{Type: tea.KeyCtrlC})) {
		t.Fatal("Ctrl+C pendant une demande quitte")
	}
	wantReply(t, f, "p1", engine.Reply{Allow: false})
	f.mu.Lock()
	live := f.liveAtRespond[0]
	f.mu.Unlock()
	if !live {
		t.Fatal("le tour a été annulé avant l'envoi du refus")
	}
	select {
	case <-f.lastCtx().Done():
	case <-time.After(5 * time.Second):
		t.Fatal("tour non annulé")
	}
	if h.m.prompting() {
		t.Fatal("demande toujours affichée")
	}
}

func TestRespondErrorDropsPrompt(t *testing.T) {
	f := newFake()
	f.respondErr = engine.ErrUnknownPermission
	h := newHarness(t, f, Options{Lang: i18n.FR})
	askPerm(t, h, f, shellPerm("p1"))
	h.send(runeKey('o'))
	if h.m.prompting() {
		t.Fatal("demande toujours affichée après l'échec de Respond")
	}
	items := h.m.tr.Items()
	if len(items) == 0 || items[len(items)-1].Kind != ItemNotice {
		t.Fatalf("aucun avis après l'échec de Respond : %+v", items)
	}
}

func TestTurnEndDiscardsPrompts(t *testing.T) {
	f := newFake()
	h := newHarness(t, f, Options{Lang: i18n.FR})
	askPerm(t, h, f, shellPerm("p1"))
	askPerm(t, h, f, shellPerm("p2"))
	f.emit(t, event.TurnCompleted, event.TurnCompletedPayload{Reason: "cancelled"})
	h.until("demandes écartées", func() bool { return !h.m.prompting() })
	if got := f.answered(); len(got) != 0 {
		t.Fatalf("réponses inattendues : %v", got)
	}
}

func TestSubmitDoneDiscardsPrompts(t *testing.T) {
	f := newFake()
	release := make(chan struct{})
	f.onSubmit = func(context.Context, *fakeEngine) error { <-release; return errors.New("coupure") }
	h := newHarness(t, f, Options{Lang: i18n.FR})
	typeText(h, "x")
	enter(h)
	h.until("Submit appelé", func() bool { return f.lastCtx() != nil })
	askPerm(t, h, f, shellPerm("p1"))
	close(release)
	h.until("tour terminé", func() bool { return !h.m.busy() })
	if h.m.prompting() {
		t.Fatal("demande conservée après la fin du tour")
	}
}

func TestReaderContinuesDuringPrompt(t *testing.T) {
	f := newFake()
	h := newHarness(t, f, Options{Lang: i18n.FR})
	askPerm(t, h, f, shellPerm("p1"))
	d := ev(t, event.AssistantDelta, event.AssistantDeltaPayload{Text: "x"})
	go func() {
		// Plus d'événements que le tampon n'en contient : envois bloquants.
		for i := 0; i < 400; i++ {
			f.ch <- d
		}
	}()
	h.until("deltas lus pendant la demande", func() bool {
		items := h.m.tr.Items()
		return len(items) == 1 && len(items[0].Text) == 400
	})
}
