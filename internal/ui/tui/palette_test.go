package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tenon-code-eu/tenon/internal/i18n"
	"github.com/tenon-code-eu/tenon/internal/llm"
	"github.com/tenon-code-eu/tenon/internal/session"
)

func ctrlP(h *harness) { h.send(tea.KeyMsg{Type: tea.KeyCtrlP}) }

func TestFuzzyScore(t *testing.T) {
	if _, ok := fuzzyScore("rw", "rewind"); !ok {
		t.Fatal("rw doit correspondre à rewind")
	}
	if _, ok := fuzzyScore("rw", "memory"); ok {
		t.Fatal("rw ne doit pas correspondre à memory")
	}
	if _, ok := fuzzyScore("RW", "Rewind"); !ok {
		t.Fatal("insensible à la casse")
	}
	a, _ := fuzzyScore("mem", "memory")
	b, _ := fuzzyScore("mem", "remember")
	if a <= b {
		t.Fatalf("memory (%d) doit passer devant remember (%d)", a, b)
	}
	items := rankItems("mem", []paletteItem{{Label: "remember"}, {Label: "memory"}})
	if items[0].Label != "memory" {
		t.Fatalf("ordre : %v", items)
	}
}

func TestRankStable(t *testing.T) {
	in := []paletteItem{{Label: "ab1"}, {Label: "ab2"}, {Label: "ab3"}}
	for _, q := range []string{"", "ab"} {
		out := rankItems(q, in)
		for i := range in {
			if out[i].Label != in[i].Label {
				t.Fatalf("q=%q : ordre %v", q, out)
			}
		}
	}
}

func TestPaletteOpenClose(t *testing.T) {
	h := newHarness(t, newFake(), statusOpts(i18n.EN))
	h.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	ctrlP(h)
	if h.m.pal == nil {
		t.Fatal("Ctrl+P doit ouvrir la palette")
	}
	esc(h)
	if h.m.pal != nil {
		t.Fatal("Échap doit fermer la palette")
	}
	ctrlP(h)
	typeText(h, "skills")
	enter(h)
	if h.m.pal != nil {
		t.Fatal("Entrée ferme la palette")
	}
	if got := h.m.input.Value(); got != "/skills " {
		t.Fatalf("saisie %q", got)
	}
}

func TestPaletteSessions(t *testing.T) {
	o := statusOpts(i18n.EN)
	o.SessionID = "s_current"
	o.Redact = func(s string) string { return strings.ReplaceAll(s, "SECRET", "***") }
	o.Sessions = []session.Summary{
		{ID: "s_current", FirstPrompt: "courante"},
		{ID: "s_aaa", FirstPrompt: "fix SECRET\x1b[31m bug", Turns: 2},
		{ID: "s_bbb", FirstPrompt: "second"},
	}
	h := newHarness(t, newFake(), o)
	h.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	ctrlP(h)
	typeText(h, "fix")
	v := h.m.View()
	if strings.Contains(v, "SECRET") || strings.Contains(v, "\x1b[31m") || strings.Contains(v, "courante") {
		t.Fatalf("titre non masqué ou session courante listée :\n%s", v)
	}
	if !strings.Contains(v, "fix *** bug") {
		t.Fatalf("session absente :\n%s", v)
	}
	enter(h)
	found := false
	for _, it := range h.m.tr.Items() {
		if it.Kind == ItemNotice && strings.Contains(it.Text, "tenon --resume s_aaa") {
			found = true
		}
	}
	if !found {
		t.Fatalf("notice de reprise absent : %+v", h.m.tr.Items())
	}
}

func TestPaletteProviderInfo(t *testing.T) {
	o := statusOpts(i18n.EN)
	o.Provider = llm.ProviderInfo{ID: "mistral", Name: "Mistral"}
	h := newHarness(t, newFake(), o)
	h.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	ctrlP(h)
	typeText(h, "provider")
	v := h.m.View()
	if !strings.Contains(v, "mistral") || !strings.Contains(v, "--provider") {
		t.Fatalf("entrée fournisseur absente :\n%s", v)
	}
	enter(h) // info : ne fait rien
	if h.m.pal == nil {
		t.Fatal("info : la palette reste ouverte")
	}
}

func TestPaletteTheme(t *testing.T) {
	h := newHarness(t, newFake(), statusOpts(i18n.EN))
	h.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	ctrlP(h)
	typeText(h, "gruvbox")
	enter(h)
	if h.m.theme.Name != "gruvbox" {
		t.Fatalf("thème %q", h.m.theme.Name)
	}
}

func TestPaletteFits(t *testing.T) {
	for _, w := range []int{60, 30, 10} {
		o := statusOpts(i18n.FR)
		o.Sessions = []session.Summary{{ID: "s_aaa", FirstPrompt: strings.Repeat("très long titre ", 10)}}
		h := newHarness(t, newFake(), o)
		h.send(tea.WindowSizeMsg{Width: w, Height: 24})
		ctrlP(h)
		assertFits(t, h.m, "palette")
		// Redimensionnement palette ouverte.
		h.send(tea.WindowSizeMsg{Width: 120, Height: 24})
		assertFits(t, h.m, "palette 120")
		h.send(tea.WindowSizeMsg{Width: w, Height: 8})
		assertFits(t, h.m, "palette petite hauteur")
		if n := len(strings.Split(h.m.View(), "\n")); n > 8 {
			t.Fatalf("%d lignes pour 8", n)
		}
	}
}

func TestPaletteNavigation(t *testing.T) {
	h := newHarness(t, newFake(), statusOpts(i18n.EN))
	h.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	ctrlP(h)
	h.send(tea.KeyMsg{Type: tea.KeyDown})
	h.send(tea.KeyMsg{Type: tea.KeyDown})
	h.send(tea.KeyMsg{Type: tea.KeyUp})
	if h.m.pal.cursor != 1 {
		t.Fatalf("curseur %d", h.m.pal.cursor)
	}
	typeText(h, "x")
	h.send(tea.KeyMsg{Type: tea.KeyBackspace})
	if h.m.pal.query != "" || h.m.pal.cursor != 0 {
		t.Fatalf("état %+v", h.m.pal)
	}
}

func TestHelpMentionsPalette(t *testing.T) {
	for _, l := range []i18n.Lang{i18n.EN, i18n.FR} {
		if !strings.Contains(i18n.T(l, "tui.help"), "Ctrl+P") {
			t.Fatalf("%s : /help sans Ctrl+P", l)
		}
	}
}

func TestPaletteSpaceKey(t *testing.T) {
	h := newHarness(t, newFake(), statusOpts(i18n.EN))
	h.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	ctrlP(h)
	typeText(h, "a")
	h.send(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
	typeText(h, "b")
	if h.m.pal.query != "a b" {
		t.Fatalf("requête %q", h.m.pal.query)
	}
}

func TestPaletteThemeBusy(t *testing.T) {
	h := newHarness(t, newFake(), statusOpts(i18n.EN))
	h.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	before := h.m.theme.Name
	ctrlP(h)
	typeText(h, "gruvbox")
	h.m.turn.start(func() {}, make(chan struct{}))
	enter(h)
	if h.m.theme.Name != before {
		t.Fatalf("thème changé pendant un tour : %q", h.m.theme.Name)
	}
	found := false
	for _, it := range h.m.tr.Items() {
		found = found || (it.Kind == ItemNotice && strings.Contains(it.Text, "unavailable"))
	}
	if !found {
		t.Fatal("avis absent")
	}
}

func TestPaletteKeepsDraft(t *testing.T) {
	h := newHarness(t, newFake(), statusOpts(i18n.EN))
	h.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	typeText(h, "mon brouillon")
	ctrlP(h)
	typeText(h, "memory")
	enter(h)
	if got := h.m.input.Value(); got != "/memory mon brouillon" {
		t.Fatalf("saisie %q", got)
	}
}

func TestPaletteClosesOnPermission(t *testing.T) {
	f := newFake()
	h := newHarness(t, f, statusOpts(i18n.EN))
	h.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	ctrlP(h)
	askPermNow(t, h, f, shellPerm("p1"))
	if h.m.pal != nil {
		t.Fatal("la palette doit se fermer à l'arrivée d'une demande")
	}
}

// TestPaletteProviderEntry : l'entrée fournisseur montre le fournisseur
// effectif (bascule comprise) et un <id> littéral dans l'indication.
func TestPaletteProviderEntry(t *testing.T) {
	info := func(m Model) string {
		for _, it := range m.paletteItems() {
			if it.Kind == kindInfo {
				return it.Label
			}
		}
		return ""
	}
	m := New(newFake(), statusOpts(i18n.EN))
	if got, want := info(m), "provider: mistral — change with tenon --provider <id>"; got != want {
		t.Fatalf("EN : %q, attendu %q", got, want)
	}
	m.tr.selected = &llm.ProviderInfo{ID: "ollama", Jurisdiction: llm.Local}
	if got, want := info(m), "provider: ollama — change with tenon --provider <id>"; got != want {
		t.Fatalf("après bascule : %q, attendu %q", got, want)
	}
	m = New(newFake(), statusOpts(i18n.FR))
	if got, want := info(m), "fournisseur : mistral — changer avec tenon --provider <id>"; got != want {
		t.Fatalf("FR : %q, attendu %q", got, want)
	}
}
