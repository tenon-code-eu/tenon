package tui

import (
	"io"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"codeberg.org/tenon/tenon/internal/i18n"
)

func welcomeOpts(l i18n.Lang) Options {
	o := statusOpts(l)
	o.Version = "v1.2.3"
	o.ProjectRoot = "/work/projet"
	return o
}

func welcomeView(t *testing.T, o Options, width int) string {
	t.Helper()
	h := newHarness(t, newFake(), o)
	h.send(tea.WindowSizeMsg{Width: width, Height: 30})
	return h.m.View()
}

func TestWelcomeWide(t *testing.T) {
	v := welcomeView(t, welcomeOpts(i18n.FR), 100)
	for _, want := range []string{
		"████████ ███████ ███    ██  ██████  ███    ██",
		"██    ███████ ██   ████  ██████  ██   ████",
		"╱▔▔▔▔▔▔▔╲", "Harness open-source · européen · français", "v1.2.3",
		"mistral-large", "Mistral AI", "juridiction UE", "projet : /work/projet",
		"/help pour l'aide · Entrée pour envoyer · Ctrl+C pour quitter", "╭", "╯",
	} {
		if !strings.Contains(v, want) {
			t.Errorf("vue sans %q :\n%s", want, v)
		}
	}
}

func TestWelcomeCompact(t *testing.T) {
	v := welcomeView(t, welcomeOpts(i18n.FR), 60)
	for _, want := range []string{"⬢ TENON  v1.2.3", "Harness open-source", "juridiction UE", "projet :"} {
		if !strings.Contains(v, want) {
			t.Errorf("vue sans %q :\n%s", want, v)
		}
	}
	if strings.Contains(v, "████████") {
		t.Errorf("art présent en version compacte :\n%s", v)
	}
}

func TestWelcomeNeverWiderThanTerminal(t *testing.T) {
	o := welcomeOpts(i18n.FR)
	o.ProjectRoot = "/très/long/chemin/" + strings.Repeat("répertoire/", 12) + "fin"
	o.Provider.Model = strings.Repeat("m", 90)
	for _, w := range []int{40, 60, 69, 70, 100, 200} {
		for _, l := range []i18n.Lang{i18n.FR, i18n.EN} {
			o.Lang = l
			for i, line := range strings.Split(welcomeView(t, o, w), "\n") {
				if lw := lipgloss.Width(line); lw > w {
					t.Errorf("largeur %d %s : ligne %d de %d colonnes : %q", w, l, i, lw, line)
				}
			}
		}
	}
}

func TestWelcomeEnglish(t *testing.T) {
	v := welcomeView(t, welcomeOpts(i18n.EN), 100)
	for _, want := range []string{
		"Open-source harness · European · French",
		"/help for help · Enter to send · Ctrl+C to quit", "project: /work/projet", "jurisdiction EU",
	} {
		if !strings.Contains(v, want) {
			t.Errorf("vue sans %q :\n%s", want, v)
		}
	}
}

func TestWelcomeVersionDefaults(t *testing.T) {
	o := welcomeOpts(i18n.FR)
	o.Version = ""
	if v := welcomeView(t, o, 100); !strings.Contains(v, "  dev") {
		t.Errorf("version dev absente :\n%s", v)
	}
	o.Version = "1.0.0"
	if v := welcomeView(t, o, 100); !strings.Contains(v, "  v1.0.0") {
		t.Errorf("préfixe v absent :\n%s", v)
	}
}

func TestWelcomeHomeShortened(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	o := welcomeOpts(i18n.FR)
	o.ProjectRoot = "/home/u/code/p"
	if v := welcomeView(t, o, 100); !strings.Contains(v, "projet : ~/code/p") {
		t.Errorf("~ absent :\n%s", v)
	}
}

func TestWelcomeSanitizedAndRedacted(t *testing.T) {
	o := welcomeOpts(i18n.FR)
	o.Provider.Model = "m\x1b[31mx"
	o.ProjectRoot = "/w/SECRET"
	o.Redact = func(s string) string { return strings.ReplaceAll(s, "SECRET", "[masqué]") }
	v := welcomeView(t, o, 100)
	if strings.Contains(v, "\x1b") || strings.Contains(v, "SECRET") || !strings.Contains(v, "[masqué]") {
		t.Errorf("désinfection ou masquage manquant :\n%q", v)
	}
}

// colored force un profil couleur : sans lui, le terminal de test n'en a pas.
func colored(m Model) Model {
	r := lipgloss.NewRenderer(io.Discard)
	r.SetColorProfile(2 /* termenv.ANSI256 */)
	m.st = newStyles(r)
	return m
}

func TestWelcomeNoColor(t *testing.T) {
	m := colored(New(newFake(), welcomeOpts(i18n.FR)))
	m.resize(100, 30)
	if !strings.Contains(strings.Join(m.welcomeLines(), "\n"), "\x1b") {
		t.Fatal("le cadre devrait être coloré sans NO_COLOR")
	}
	t.Setenv("NO_COLOR", "1")
	for _, w := range []int{100, 60} {
		m.resize(w, 30)
		if s := strings.Join(m.welcomeLines(), "\n"); strings.Contains(s, "\x1b") {
			t.Errorf("largeur %d : séquence ESC avec NO_COLOR :\n%q", w, s)
		}
	}
}

func TestWelcomeScrollsWithConversation(t *testing.T) {
	h := newHarness(t, newFake(), welcomeOpts(i18n.FR))
	h.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	if n := len(h.m.tr.Items()); n != 0 {
		t.Fatalf("le cadre ne doit pas être un élément de transcription (%d)", n)
	}
}
