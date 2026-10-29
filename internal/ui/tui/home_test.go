package tui

import (
	"io"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/tenon-code-eu/tenon/internal/i18n"
	"github.com/tenon-code-eu/tenon/internal/llm"
)

// noLinkEnv retire les variables qui activent les hyperliens : le rendu ne
// dépend pas du terminal qui lance les tests.
func noLinkEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"TERM_PROGRAM", "KITTY_WINDOW_ID", "VTE_VERSION"} {
		t.Setenv(k, "")
	}
}

func homeOpts(l i18n.Lang) Options {
	o := statusOpts(l)
	o.Version = "v1.2.3"
	o.ProjectRoot = "/work/projet"
	return o
}

func homeView(t *testing.T, o Options, width int) string {
	t.Helper()
	noLinkEnv(t)
	h := newHarness(t, newFake(), o)
	h.send(tea.WindowSizeMsg{Width: width, Height: 30})
	return h.m.View()
}

const (
	shortcutsEN = "/help /skills /rewind /memory · Ctrl+P palette · Tab agents"
	linksEN     = "docs · github · report a bug"
	linksFR     = "docs · github · signaler un bug"
)

func TestHomeWidths(t *testing.T) {
	o := homeOpts(i18n.EN)
	for _, w := range []int{40, 60, 69, 70, 80, 100, 120, 200} {
		for _, l := range []i18n.Lang{i18n.FR, i18n.EN} {
			o.Lang = l
			for i, line := range strings.Split(homeView(t, o, w), "\n") {
				if lw := lipgloss.Width(line); lw > w {
					t.Errorf("largeur %d %s : ligne %d de %d colonnes : %q", w, l, i, lw, line)
				}
			}
		}
	}
	o.Lang = i18n.EN
	v := homeView(t, o, 60)
	if !strings.Contains(v, "⬢ TENON") || strings.Contains(v, "████████") {
		t.Errorf("60 colonnes : logo compact attendu :\n%s", v)
	}
	v = homeView(t, o, 120)
	for _, want := range []string{
		"████████ ███████ ███    ██  ██████  ███    ██", "╱▔▔▔▔▔▔▔╲",
		"sovereign coding agent  v1.2.3", "╭", "╰", shortcutsEN, linksEN, "Tip: ",
	} {
		if !strings.Contains(v, want) {
			t.Errorf("120 colonnes : vue sans %q :\n%s", want, v)
		}
	}
	if strings.Contains(v, "⬢ TENON") {
		t.Errorf("120 colonnes : logo compact présent :\n%s", v)
	}
}

func TestHomeFrench(t *testing.T) {
	v := homeView(t, homeOpts(i18n.FR), 120)
	for _, want := range []string{"agent de code souverain  v1.2.3", linksFR, "Astuce : ", "/help /skills /rewind /memory"} {
		if !strings.Contains(v, want) {
			t.Errorf("vue sans %q :\n%s", want, v)
		}
	}
}

// L'accueil est centré : la zone de saisie (largeur min(72, largeur − 8))
// commence au milieu, et le bloc est centré verticalement.
func TestHomeCentered(t *testing.T) {
	v := homeView(t, homeOpts(i18n.EN), 120)
	lines := strings.Split(v, "\n")
	top := -1
	for i, l := range lines {
		s := ansi.Strip(l)
		if i := strings.Index(s, "╭"); i >= 0 {
			if col := ansi.StringWidth(s[:i]); col != (120-72)/2 {
				t.Errorf("zone de saisie en colonne %d", col)
			}
			if w := ansi.StringWidth(strings.TrimSpace(s)); w != 72 {
				t.Errorf("zone de saisie de %d colonnes", w)
			}
		}
		if top < 0 && strings.TrimSpace(s) != "" {
			top = i
		}
	}
	if top < 2 {
		t.Errorf("bloc non centré verticalement (première ligne %d) :\n%s", top, v)
	}
	v = homeView(t, homeOpts(i18n.EN), 60)
	for _, l := range strings.Split(v, "\n") {
		s := ansi.Strip(l)
		if strings.Contains(s, "╭") && ansi.StringWidth(strings.TrimSpace(s)) != 52 {
			t.Errorf("60 colonnes : zone de saisie %q", s)
		}
	}
}

func TestHomeVersionDefaults(t *testing.T) {
	o := homeOpts(i18n.EN)
	o.Version = ""
	if v := homeView(t, o, 120); !strings.Contains(v, "agent  dev") {
		t.Errorf("version dev absente :\n%s", v)
	}
	o.Version = "1.0.0"
	if v := homeView(t, o, 120); !strings.Contains(v, "agent  v1.0.0") {
		t.Errorf("préfixe v absent :\n%s", v)
	}
}

// colored force un profil couleur : sans lui, le terminal de test n'en a pas.
func colored(m Model) Model {
	r := lipgloss.NewRenderer(io.Discard)
	r.SetColorProfile(2 /* termenv.ANSI256 */)
	m.st = newStyles(r, m.theme)
	return m
}

func TestHomeNoColor(t *testing.T) {
	noLinkEnv(t)
	m := colored(New(newFake(), homeOpts(i18n.FR)))
	m.resize(120, 30)
	if !strings.Contains(strings.Join(m.homeLines(), "\n"), "\x1b[") {
		t.Fatal("l'accueil devrait être coloré sans NO_COLOR")
	}
	t.Setenv("NO_COLOR", "1")
	m = colored(New(newFake(), homeOpts(i18n.FR)))
	for _, w := range []int{120, 60} {
		m.resize(w, 30)
		if s := strings.Join(m.homeLines(), "\n"); strings.Contains(s, "\x1b[") {
			t.Errorf("largeur %d : couleur avec NO_COLOR :\n%q", w, s)
		}
		if s := m.statusLine(); strings.Contains(s, "\x1b[") && colorSGR.MatchString(s) {
			t.Errorf("largeur %d : barre d'état colorée avec NO_COLOR :\n%q", w, s)
		}
	}
}

func TestHomeHiddenAfterFirstMessage(t *testing.T) {
	noLinkEnv(t)
	h := newHarness(t, newFake(), homeOpts(i18n.EN))
	h.send(tea.WindowSizeMsg{Width: 120, Height: 30})
	if !strings.Contains(h.m.View(), shortcutsEN) {
		t.Fatalf("accueil absent :\n%s", h.m.View())
	}
	typeText(h, "bonjour")
	if v := h.m.View(); !strings.Contains(v, shortcutsEN) || !strings.Contains(v, "bonjour") {
		t.Fatalf("saisie absente de l'accueil :\n%s", v)
	}
	enter(h)
	if v := h.m.View(); strings.Contains(v, shortcutsEN) || strings.Contains(v, "████████") {
		t.Fatalf("accueil encore affiché après le premier message :\n%s", v)
	}
	// Une commande intégrée (/help) le masque aussi : sa réponse est visible.
	h = newHarness(t, newFake(), homeOpts(i18n.EN))
	h.send(tea.WindowSizeMsg{Width: 120, Height: 30})
	typeText(h, "/help")
	enter(h)
	if v := h.m.View(); strings.Contains(v, shortcutsEN) || !strings.Contains(v, i18n.T(i18n.EN, "tui.help.cmd.help")) {
		t.Fatalf("/help : accueil ou aide :\n%s", v)
	}
}

func TestHomeHiddenOnResume(t *testing.T) {
	o := homeOpts(i18n.EN)
	o.Resumed = true
	if v := homeView(t, o, 120); strings.Contains(v, shortcutsEN) || strings.Contains(v, "████████") {
		t.Fatalf("accueil affiché pour une session reprise :\n%s", v)
	}
}

// Les avis du démarrage restent visibles sous l'accueil.
func TestHomeStartupNotices(t *testing.T) {
	o := homeOpts(i18n.EN)
	o.Notices = []string{"mcp: server down"}
	v := homeView(t, o, 120)
	if !strings.Contains(v, shortcutsEN) || !strings.Contains(v, "mcp: server down") {
		t.Fatalf("accueil ou avis absent :\n%s", v)
	}
}

func TestHomeNotATranscriptItem(t *testing.T) {
	h := newHarness(t, newFake(), homeOpts(i18n.FR))
	h.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	if n := len(h.m.tr.Items()); n != 0 {
		t.Fatalf("l'accueil ne doit pas être un élément de transcription (%d)", n)
	}
}

func TestHyperlinks(t *testing.T) {
	const url = "https://tenon-code-eu.github.io/tenon/"
	if got := hyperlink(url, "docs", true); got != "\x1b]8;;"+url+"\x1b\\docs\x1b]8;;\x1b\\" {
		t.Fatalf("activé : %q", got)
	}
	if got := hyperlink(url, "docs", false); got != "docs" {
		t.Fatalf("désactivé : %q", got)
	}
	if lipgloss.Width(hyperlink(url, "docs", true)) != 4 {
		t.Fatal("la séquence OSC 8 ne doit pas compter dans la largeur")
	}
	// Accueil : liens OSC 8 quand le terminal les gère, toujours dans la largeur.
	noLinkEnv(t)
	t.Setenv("TERM_PROGRAM", "WezTerm")
	h := newHarness(t, newFake(), homeOpts(i18n.EN))
	h.send(tea.WindowSizeMsg{Width: 120, Height: 30})
	v := h.m.View()
	for _, u := range []string{url, "https://github.com/tenon-code-eu/tenon", "https://github.com/tenon-code-eu/tenon/issues/new"} {
		if !strings.Contains(v, "\x1b]8;;"+u+"\x1b\\") {
			t.Errorf("lien %s absent :\n%q", u, v)
		}
	}
	assertWidth(t, v, 120)
}

func TestSupportsHyperlinks(t *testing.T) {
	for _, c := range []struct {
		env  map[string]string
		want bool
	}{
		{map[string]string{"TERM_PROGRAM": "iTerm.app"}, true},
		{map[string]string{"TERM_PROGRAM": "WezTerm"}, true},
		{map[string]string{"TERM_PROGRAM": "vscode"}, true},
		{map[string]string{"TERM_PROGRAM": "ghostty"}, true},
		{map[string]string{"TERM_PROGRAM": "Apple_Terminal"}, false},
		{map[string]string{"KITTY_WINDOW_ID": "1"}, true},
		{map[string]string{"VTE_VERSION": "5000"}, true},
		{map[string]string{"VTE_VERSION": "7200"}, true},
		{map[string]string{"VTE_VERSION": "4999"}, false},
		{map[string]string{"VTE_VERSION": "abc"}, false},
		{map[string]string{}, false},
		{map[string]string{"TERM_PROGRAM": "iTerm.app", "TERM": "dumb"}, false},
		{map[string]string{"KITTY_WINDOW_ID": "1", "TERM": "dumb"}, false},
	} {
		if got := supportsHyperlinks(func(k string) string { return c.env[k] }); got != c.want {
			t.Errorf("%v : %v, attendu %v", c.env, got, c.want)
		}
	}
}

func TestTipOfTheDay(t *testing.T) {
	for _, l := range []i18n.Lang{i18n.EN, i18n.FR} {
		seen := map[string]bool{}
		n := 0
		first := tipOfTheDay(l, func(k int) int { n = k; return 0 })
		if n < 8 {
			t.Fatalf("%s : %d astuces, au moins 8 attendues", l, n)
		}
		for i := range n {
			tip := tipOfTheDay(l, func(int) int { return i })
			if tip == "" || strings.Contains(tip, "tui.tip") || seen[tip] {
				t.Errorf("%s : astuce %d invalide ou en double : %q", l, i, tip)
			}
			seen[tip] = true
		}
		if again := tipOfTheDay(l, func(int) int { return 0 }); again != first {
			t.Errorf("%s : non déterministe : %q / %q", l, first, again)
		}
	}
	if tipOfTheDay(i18n.EN, func(int) int { return 3 }) == tipOfTheDay(i18n.FR, func(int) int { return 3 }) {
		t.Error("astuce non traduite")
	}
}

func statusLineOf(t *testing.T, o Options, width int) string {
	t.Helper()
	noLinkEnv(t)
	h := newHarness(t, newFake(), o)
	h.send(tea.WindowSizeMsg{Width: width, Height: 30})
	return lastLine(h.m.View())
}

func TestStatusLine(t *testing.T) {
	o := homeOpts(i18n.FR)
	o.Sandbox = SandboxOn
	o.Profile = "eu-only"
	o.SessionTokens = 2000
	s := statusLineOf(t, o, 200)
	for _, want := range []string{"/work/projet", "mistral-large", "Mistral AI", "FR", " UE ", " isolé ", "profil : eu-only", "session 2 k"} {
		if !strings.Contains(s, want) {
			t.Errorf("FR : barre d'état %q sans %q", s, want)
		}
	}
	if lipgloss.Width(s) != 200 {
		t.Errorf("barre d'état de %d colonnes", lipgloss.Width(s))
	}
	o = homeOpts(i18n.EN)
	o.Sandbox = SandboxOff
	o.Provider = llm.ProviderInfo{ID: "openai", Name: "OpenAI", Model: "gpt", Country: "US", Jurisdiction: llm.NonEU}
	s = statusLineOf(t, o, 200)
	for _, want := range []string{" non-EU ", " not sandboxed ", "OpenAI"} {
		if !strings.Contains(s, want) {
			t.Errorf("EN : barre d'état %q sans %q", s, want)
		}
	}
	if strings.Contains(s, "profile") {
		t.Errorf("profil sans profil actif : %q", s)
	}
	o.Provider = llm.ProviderInfo{ID: "ollama", Name: "Ollama", Model: "llama", Jurisdiction: llm.Local}
	if s := statusLineOf(t, o, 200); !strings.Contains(s, " local ") {
		t.Errorf("pastille locale absente : %q", s)
	}
	// Sans information d'isolation, aucune pastille d'isolation.
	if s := statusLineOf(t, homeOpts(i18n.EN), 200); strings.Contains(s, "sandboxed") {
		t.Errorf("pastille d'isolation inventée : %q", s)
	}
}

func TestStatusLineFits(t *testing.T) {
	o := homeOpts(i18n.FR)
	o.Sandbox = SandboxOn
	o.ProjectRoot = "/très/long/chemin/" + strings.Repeat("répertoire/", 12) + "fin"
	for _, w := range []int{200, 120, 80, 60, 30, 5} {
		s := statusLineOf(t, o, w)
		if lw := lipgloss.Width(s); lw > w {
			t.Errorf("largeur %d : barre de %d colonnes : %q", w, lw, s)
		}
	}
	s := statusLineOf(t, o, 120)
	if !strings.Contains(s, "/très/") || !strings.Contains(s, "…") || !strings.Contains(s, "/fin") {
		t.Errorf("projet non coupé au milieu : %q", s)
	}
	if !strings.Contains(s, "mistral-large") || !strings.Contains(s, " UE ") {
		t.Errorf("modèle ou juridiction perdus : %q", s)
	}
	// Les éléments secondaires partent d'abord : session, puis estimation.
	o = homeOpts(i18n.EN)
	o.Sandbox = SandboxOn
	o.Profile = "eu-only"
	s = statusLineOf(t, o, 80)
	if strings.Contains(s, "sess-42") || !strings.Contains(s, "Mistral AI") || !strings.Contains(s, "profile: eu-only") || !strings.Contains(s, "/work/projet") {
		t.Errorf("ordre d'abandon : %q", s)
	}
}

func TestStatusLineHomeShortened(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	o := homeOpts(i18n.FR)
	o.ProjectRoot = "/home/u/code/p"
	if s := statusLineOf(t, o, 200); !strings.Contains(s, "~/code/p") {
		t.Errorf("~ absent : %q", s)
	}
}

func TestHomeSanitizedAndRedacted(t *testing.T) {
	o := homeOpts(i18n.FR)
	o.Provider.Model = "m\x1b[31mx"
	o.ProjectRoot = "/w/SECRET"
	o.Redact = func(s string) string { return strings.ReplaceAll(s, "SECRET", "[masqué]") }
	v := homeView(t, o, 120)
	if strings.Contains(v, "\x1b") || strings.Contains(v, "SECRET") || !strings.Contains(v, "[masqué]") {
		t.Errorf("désinfection ou masquage manquant :\n%q", v)
	}
}
