package tui

import (
	"io"
	"regexp"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tenon-code-eu/tenon/internal/config"
	"github.com/tenon-code-eu/tenon/internal/i18n"
)

func TestThemeNames(t *testing.T) {
	want := []string{"tenon", "tokyonight", "catppuccin", "gruvbox", "light"}
	if got := ThemeNames(); !slices.Equal(got, want) {
		t.Fatalf("ThemeNames = %v, want %v", got, want)
	}
	for _, n := range want {
		th, ok := ThemeByName(n)
		if !ok || th.Name != n {
			t.Fatalf("ThemeByName(%q) = %+v %v", n, th, ok)
		}
	}
	if _, ok := ThemeByName("nope"); ok {
		t.Fatal("thème inconnu accepté")
	}
}

func TestThemeTenonColors(t *testing.T) {
	th, _ := ThemeByName("tenon")
	if th.Structure != lipgloss.Color("#003399") || th.Accent != lipgloss.Color("#FFCC00") {
		t.Fatalf("palette tenon : %+v", th)
	}
}

func TestThemeCommand(t *testing.T) {
	h := newHarness(t, newFake(), Options{Lang: i18n.EN, SessionID: "s"})
	if h.m.theme.Name != "tenon" {
		t.Fatalf("thème par défaut %q", h.m.theme.Name)
	}
	type_ := func(s string) {
		h.m.input.SetValue(s)
		h.send(tea.KeyMsg{Type: tea.KeyEnter})
	}
	type_("/theme gruvbox")
	if h.m.theme.Name != "gruvbox" {
		t.Fatalf("thème %q", h.m.theme.Name)
	}
	type_("/theme nope")
	if h.m.theme.Name != "gruvbox" {
		t.Fatalf("thème changé par une valeur inconnue : %q", h.m.theme.Name)
	}
	items := h.m.tr.Items()
	last := items[len(items)-1]
	if last.Kind != ItemError {
		t.Fatalf("dernière ligne %+v", last)
	}
	for _, n := range ThemeNames() {
		if !strings.Contains(last.Text, n) {
			t.Errorf("%q absent de %q", n, last.Text)
		}
	}
	type_("/theme")
	items = h.m.tr.Items()
	if txt := items[len(items)-1].Text; !strings.Contains(txt, "tokyonight") || !strings.Contains(txt, "gruvbox") {
		t.Fatalf("liste %q", txt)
	}
}

func TestThemeFromOptions(t *testing.T) {
	m := New(newFake(), Options{Lang: i18n.EN, SessionID: "s", Theme: "light"})
	if m.theme.Name != "light" {
		t.Fatalf("thème %q", m.theme.Name)
	}
	m = New(newFake(), Options{Lang: i18n.EN, SessionID: "s", Theme: "inconnu"})
	if m.theme.Name != "tenon" {
		t.Fatalf("repli %q", m.theme.Name)
	}
}

// Les couleurs changent avec le thème (sortie colorée forcée).
func TestThemeChangesColors(t *testing.T) {
	m := colored(New(newFake(), homeOpts(i18n.EN)))
	m.resize(100, 30)
	a := strings.Join(m.homeLines(), "\n")
	g, _ := ThemeByName("gruvbox")
	m.setTheme(g)
	b := strings.Join(m.homeLines(), "\n")
	if a == b {
		t.Fatal("le rendu ne dépend pas du thème")
	}
}

var colorSGR = regexp.MustCompile(`\x1b\[[0-9;]*(38|48|3[0-7]|4[0-7]|9[0-7]|10[0-7])[0-9;]*m`)

func themedView(name string) string {
	r := lipgloss.NewRenderer(io.Discard)
	r.SetColorProfile(0 /* termenv.TrueColor */)
	o := homeOpts(i18n.EN)
	o.Theme = name
	m := New(newFake(), o)
	m.st = newStyles(r, m.theme)
	m.resize(100, 30)
	m.tr.notice("avis")
	m.tr.add(Item{Kind: ItemError, Text: "erreur"})
	m.tr.add(Item{Kind: ItemTool, Text: "outil"})
	return m.View()
}

func TestNoColorIgnoresTheme(t *testing.T) {
	for _, name := range ThemeNames() {
		// Base : sans NO_COLOR, la regex voit bien des couleurs.
		if v := themedView(name); !colorSGR.MatchString(v) {
			t.Fatalf("thème %s : aucune couleur sans NO_COLOR :\n%q", name, v)
		}
	}
	t.Setenv("NO_COLOR", "1")
	for _, name := range ThemeNames() {
		if v := themedView(name); colorSGR.MatchString(v) {
			t.Errorf("thème %s : couleur avec NO_COLOR :\n%q", name, v)
		}
	}
}

func darkRenderer(dark bool) *lipgloss.Renderer {
	r := lipgloss.NewRenderer(io.Discard)
	r.SetColorProfile(0 /* termenv.TrueColor */)
	r.SetHasDarkBackground(dark)
	return r
}

func TestStructureAdaptive(t *testing.T) {
	th, _ := ThemeByName("tenon")
	if got := newStyles(darkRenderer(true), th).structure; got != "#6C8CFF" {
		t.Fatalf("fond sombre : %q", got)
	}
	if got := newStyles(darkRenderer(false), th).structure; got != "#003399" {
		t.Fatalf("fond clair : %q", got)
	}
	// L'accueil utilise la variante claire sur fond sombre.
	m := New(newFake(), homeOpts(i18n.EN))
	m.st = newStyles(darkRenderer(true), m.theme)
	m.resize(100, 30)
	if v := strings.Join(m.homeLines(), "\n"); !strings.Contains(v, "38;2;108;140;255") {
		t.Fatalf("bordure sans bleu clair :\n%q", v)
	}
}

func TestLocalBadgeExplicitForeground(t *testing.T) {
	for _, name := range ThemeNames() {
		th, _ := ThemeByName(name)
		for _, dark := range []bool{true, false} {
			st := newStyles(darkRenderer(dark), th)
			if st.local.GetForeground() == (lipgloss.NoColor{}) || st.local.GetBackground() == (lipgloss.NoColor{}) || st.local.GetReverse() {
				t.Errorf("%s : badge local sans fond/texte explicites", name)
			}
		}
	}
}

// TestLocalPillOK : la pastille locale prend le vert OK (spéc. §3, R12).
func TestLocalPillOK(t *testing.T) {
	for _, name := range ThemeNames() {
		th, _ := ThemeByName(name)
		st := newStyles(darkRenderer(true), th)
		if st.local.GetBackground() != th.OK {
			t.Errorf("%s : pastille locale %v, attendu OK %q", name, st.local.GetBackground(), th.OK)
		}
	}
}

func TestErrorRole(t *testing.T) {
	for _, name := range ThemeNames() {
		th, _ := ThemeByName(name)
		if th.Error == "" || th.Error == th.Alert {
			t.Errorf("%s : Error %q Alert %q", name, th.Error, th.Alert)
		}
		st := newStyles(darkRenderer(true), th)
		if st.err.GetForeground() != th.Error || st.del.GetForeground() != th.Error {
			t.Errorf("%s : err/del hors rôle Error", name)
		}
		if st.nonEU.GetForeground() != th.Alert {
			t.Errorf("%s : nonEU hors rôle Alert", name)
		}
	}
}

func TestThemesMatchConfig(t *testing.T) {
	if !slices.Equal(ThemeNames(), config.UIThemes) {
		t.Fatalf("tui %v, config %v", ThemeNames(), config.UIThemes)
	}
}
