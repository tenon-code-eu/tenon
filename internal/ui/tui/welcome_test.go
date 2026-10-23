package tui

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tenon-code-eu/tenon/internal/hooks"
	"github.com/tenon-code-eu/tenon/internal/i18n"
	"github.com/tenon-code-eu/tenon/internal/skills"
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
		"Entrée pour envoyer · Échap ×2 pour revenir en arrière", "Ctrl+C pour quitter", "╭", "╯",
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
		"Enter to send · Esc twice to rewind · Ctrl+C to quit", "project: /work/projet", "jurisdiction EU",
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

func TestWelcomeCounts(t *testing.T) {
	o := welcomeOpts(i18n.EN)
	o.Skills = skillCatalog(t, "review", "deploy")
	home := t.TempDir()
	file := filepath.Join(home, ".config", "tenon", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(`{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"echo a"}]}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	set, _, err := hooks.Load(hooks.Options{Getenv: func(k string) string {
		if k == "HOME" {
			return home
		}
		return ""
	}, ProjectRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	o.Hooks = set
	v := welcomeView(t, o, 100)
	if !strings.Contains(v, "skills: 6 · agents: 0 · hooks: 1") {
		t.Errorf("compteurs absents :\n%s", v)
	}
	if v := welcomeView(t, welcomeOpts(i18n.FR), 100); !strings.Contains(v, "skills : 0 · agents : 0 · hooks : 0") {
		t.Errorf("compteurs à 0 absents :\n%s", v)
	}
}

func TestWelcomeCommands(t *testing.T) {
	for _, l := range []i18n.Lang{i18n.EN, i18n.FR} {
		line := commandsLine(l, skills.Builtins)
		for _, n := range skills.Builtins {
			if !strings.Contains(line, "/"+n) {
				t.Errorf("%s : /%s absent de %q", l, n, line)
			}
		}
		if !strings.Contains(commandsLine(l, append(slices.Clone(skills.Builtins), "fake")), "/fake") {
			t.Errorf("%s : commande ajoutée absente", l)
		}
	}
	if v := welcomeView(t, welcomeOpts(i18n.EN), 100); !strings.Contains(v, "commands: /help · /quit · /skills · /agents · /hooks · /rewind") {
		t.Errorf("ligne des commandes absente :\n%s", v)
	}
}

func TestWelcomeWraps(t *testing.T) {
	v := welcomeView(t, welcomeOpts(i18n.FR), 50)
	for _, l := range strings.Split(v, "\n") {
		if strings.Contains(l, "…") && !strings.Contains(l, "juridiction") {
			t.Errorf("troncature au lieu du retour à la ligne : %q", l)
		}
	}
	for _, want := range []string{"/hooks", "/rewind", "revenir en arrière", "Ctrl+C pour quitter"} {
		if !strings.Contains(v, want) {
			t.Errorf("vue sans %q :\n%s", want, v)
		}
	}
	lines := wrapAtSeparators("commandes : /help · /quit · /skills · /agents", len("commandes : "), 30)
	if len(lines) < 2 || !strings.HasPrefix(lines[1], "            /") {
		t.Errorf("retrait des lignes de suite : %q", lines)
	}
}

// Le séparateur « · » occupe 3 colonnes (4 octets) : une ligne qui tient
// exactement n'est pas coupée.
func TestWrapAtSeparatorsDisplayWidth(t *testing.T) {
	if got := wrapAtSeparators("ab · cd", 0, 7); len(got) != 1 || got[0] != "ab · cd" {
		t.Fatalf("lignes %q", got)
	}
	if got := wrapAtSeparators("ab · cd", 0, 6); len(got) != 2 {
		t.Fatalf("lignes %q", got)
	}
}

func TestWelcomeProfile(t *testing.T) {
	o := welcomeOpts(i18n.FR)
	o.Profile = "eu-only"
	v := welcomeView(t, o, 100)
	if !strings.Contains(v, "profil : eu-only") {
		t.Errorf("ligne du profil absente :\n%s", v)
	}
	// Juste sous la ligne du projet.
	lines := strings.Split(v, "\n")
	for i, l := range lines {
		if strings.Contains(l, "projet : /work/projet") && (i+1 >= len(lines) || !strings.Contains(lines[i+1], "profil : eu-only")) {
			t.Errorf("profil hors de la ligne suivant le projet :\n%s", v)
		}
	}
	o = welcomeOpts(i18n.EN)
	o.Profile = "strict-local"
	if v := welcomeView(t, o, 100); !strings.Contains(v, "profile: strict-local") {
		t.Errorf("en : ligne absente :\n%s", v)
	}
	if v := welcomeView(t, welcomeOpts(i18n.FR), 100); strings.Contains(v, "profil :") {
		t.Errorf("ligne du profil sans profil :\n%s", v)
	}
}

func TestWelcomePolicy(t *testing.T) {
	o := welcomeOpts(i18n.FR)
	o.Profile = "eu-only"
	o.Policy = "politique : acme-dev v3 (clé 0123456789ABCDEF)"
	v := welcomeView(t, o, 100)
	// Juste sous la ligne du profil.
	lines := strings.Split(v, "\n")
	found := false
	for i, l := range lines {
		if strings.Contains(l, "profil : eu-only") {
			found = i+1 < len(lines) && strings.Contains(lines[i+1], "politique : acme-dev v3 (clé 0123456789ABCDEF)")
		}
	}
	if !found {
		t.Errorf("politique hors de la ligne suivant le profil :\n%s", v)
	}
	if v := welcomeView(t, welcomeOpts(i18n.FR), 100); strings.Contains(v, "politique :") {
		t.Errorf("ligne de politique sans politique :\n%s", v)
	}
}
