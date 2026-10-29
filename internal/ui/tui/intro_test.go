package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/tenon-code-eu/tenon/internal/i18n"
)

func TestIntroSkipped(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	if !introEnabled(true, true, 100) {
		t.Fatal("intro attendue (TTY, animation, 100 colonnes)")
	}
	for _, c := range []struct {
		name           string
		animation, tty bool
		width          int
	}{
		{"sans TTY", true, false, 100},
		{"animation coupée", false, true, 100},
		{"moins de 70 colonnes", true, true, 69},
	} {
		if introEnabled(c.animation, c.tty, c.width) {
			t.Errorf("%s : intro non sautée", c.name)
		}
	}
	t.Setenv("NO_COLOR", "1")
	if introEnabled(true, true, 100) {
		t.Error("NO_COLOR : intro non sautée")
	}
	// New ne lance jamais l'intro (aucun TTY connu).
	if m := New(newFake(), Options{Lang: i18n.EN, Animation: true}); m.introActive() {
		t.Error("intro active sans Run")
	}
}

// Garde de Run : l'intro ne démarre ni pour une session reprise, ni sans
// TTY, ni avec l'animation coupée.
func TestIntroRunGate(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	start := func(o Options, tty bool) bool {
		m := New(newFake(), o)
		m.maybeStartIntro(tty)
		return m.introActive()
	}
	if !start(Options{Lang: i18n.EN, Animation: true}, true) {
		t.Fatal("intro attendue (nouvelle session, TTY, animation)")
	}
	if start(Options{Lang: i18n.EN, Animation: true, Resumed: true}, true) {
		t.Error("session reprise animée")
	}
	if start(Options{Lang: i18n.EN, Animation: true}, false) {
		t.Error("sortie non TTY animée")
	}
	if start(Options{Lang: i18n.EN}, true) {
		t.Error("animation coupée mais jouée")
	}
}

// 6 à 8 images à environ 80 ms, moins de 700 ms au total ; la dernière est
// l'art de l'accueil.
func TestIntroFrames(t *testing.T) {
	noLinkEnv(t)
	t.Setenv("NO_COLOR", "")
	if introFrames < 6 || introFrames > 8 || time.Duration(introFrames)*introDelay >= 700*time.Millisecond {
		t.Fatalf("%d images de %v", introFrames, introDelay)
	}
	m := New(newFake(), homeOpts(i18n.EN))
	m.resize(120, 30)
	final := strings.Join(m.homeLines(), "\n")
	m.introFrame = 0
	first := strings.Join(m.homeLines(), "\n")
	if first == final {
		t.Fatal("la première image est déjà l'accueil")
	}
	for i, l := range m.homeLines() {
		if w := ansi.StringWidth(l); w > 120 {
			t.Fatalf("image 0 : ligne %d de %d colonnes", i, w)
		}
	}
	var mm tea.Model = m
	for i := 1; i < introFrames; i++ {
		mm, _ = mm.Update(introTickMsg{})
		if !mm.(Model).introActive() {
			t.Fatalf("intro terminée trop tôt (image %d)", i)
		}
	}
	if got := strings.Join(mm.(Model).homeLines(), "\n"); got != final {
		t.Fatalf("dernière image différente de l'accueil :\n%s\n---\n%s", got, final)
	}
	mm, cmd := mm.Update(introTickMsg{})
	if mm.(Model).introActive() || cmd != nil {
		t.Fatal("intro non terminée après la dernière image")
	}
	// Un terminal rétréci sous 70 colonnes l'arrête.
	m.introFrame = 0
	mm, _ = m.Update(tea.WindowSizeMsg{Width: 60, Height: 30})
	if mm.(Model).introActive() {
		t.Fatal("intro active sous 70 colonnes")
	}
}

// Review Focus 4 : la touche tapée pendant l'intro l'interrompt et arrive
// dans la saisie.
func TestIntroKeyKept(t *testing.T) {
	noLinkEnv(t)
	h := newHarness(t, newFake(), homeOpts(i18n.EN))
	h.send(tea.WindowSizeMsg{Width: 120, Height: 30})
	h.m.introFrame = 0
	typeText(h, "a")
	if h.m.introActive() {
		t.Fatal("intro non interrompue par une touche")
	}
	if got := h.m.input.Value(); got != "a" {
		t.Fatalf("saisie %q, attendu \"a\"", got)
	}
	// Un tick tardif ne relance rien.
	h.send(introTickMsg{})
	if h.m.introActive() {
		t.Fatal("intro relancée par un tick tardif")
	}
}
