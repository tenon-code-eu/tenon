package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/i18n"
)

// scrollHarness renvoie une TUI de 40×10 avec 30 lignes « ligneN » puis
// « DERNIERE », vue en bas.
func scrollHarness(t *testing.T) (*harness, *fakeEngine) {
	t.Helper()
	f := newFake()
	h := newHarness(t, f, Options{Lang: i18n.FR})
	h.send(tea.WindowSizeMsg{Width: 40, Height: 10})
	for i := 0; i < 30; i++ {
		f.emit(t, event.AssistantMessage, event.AssistantMessagePayload{Text: "ligne"})
	}
	f.emit(t, event.AssistantMessage, event.AssistantMessagePayload{Text: "DERNIERE"})
	h.until("messages", func() bool { return len(h.m.tr.Items()) == 31 })
	if !strings.Contains(h.m.View(), "DERNIERE") {
		t.Fatal("la vue ne suit pas le bas")
	}
	return h, f
}

func TestMouseWheelScrolls(t *testing.T) {
	h, _ := scrollHarness(t)
	h.send(tea.MouseMsg{Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress})
	if h.m.scrollBack != wheelLines {
		t.Fatalf("molette haut : scrollBack = %d, attendu %d", h.m.scrollBack, wheelLines)
	}
	h.send(tea.MouseMsg{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	if h.m.scrollBack != 0 {
		t.Fatalf("molette bas : scrollBack = %d", h.m.scrollBack)
	}
}

func TestShiftArrowsScrollOneLine(t *testing.T) {
	h, _ := scrollHarness(t)
	h.send(tea.KeyMsg{Type: tea.KeyShiftUp})
	h.send(tea.KeyMsg{Type: tea.KeyShiftUp})
	if h.m.scrollBack != 2 {
		t.Fatalf("Maj+↑ ×2 : scrollBack = %d", h.m.scrollBack)
	}
	h.send(tea.KeyMsg{Type: tea.KeyShiftDown})
	if h.m.scrollBack != 1 {
		t.Fatalf("Maj+↓ : scrollBack = %d", h.m.scrollBack)
	}
}

func TestHalfPageAndEndsWhenInputEmpty(t *testing.T) {
	h, _ := scrollHarness(t)
	half := max(1, h.m.convHeight()/2)
	h.send(tea.KeyMsg{Type: tea.KeyCtrlU})
	if h.m.scrollBack != half {
		t.Fatalf("Ctrl+U : scrollBack = %d, attendu %d", h.m.scrollBack, half)
	}
	h.send(tea.KeyMsg{Type: tea.KeyCtrlD})
	if h.m.scrollBack != 0 {
		t.Fatalf("Ctrl+D : scrollBack = %d", h.m.scrollBack)
	}
	h.send(tea.KeyMsg{Type: tea.KeyHome})
	if top := len(h.m.conversationLines()) - h.m.convHeight(); h.m.scrollBack != top {
		t.Fatalf("Début : scrollBack = %d, attendu %d (haut)", h.m.scrollBack, top)
	}
	h.send(tea.KeyMsg{Type: tea.KeyEnd})
	if h.m.scrollBack != 0 {
		t.Fatalf("Fin : scrollBack = %d", h.m.scrollBack)
	}
}

func TestEditKeysKeptWhenInputNotEmpty(t *testing.T) {
	h, _ := scrollHarness(t)
	h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("abc")})
	h.send(tea.KeyMsg{Type: tea.KeyHome})
	h.send(tea.KeyMsg{Type: tea.KeyCtrlU})
	if h.m.scrollBack != 0 {
		t.Fatalf("Début/Ctrl+U avec une saisie font défiler : scrollBack = %d", h.m.scrollBack)
	}
}

func TestViewStaysWhileReadingAndShowsMarker(t *testing.T) {
	h, f := scrollHarness(t)
	h.send(tea.KeyMsg{Type: tea.KeyPgUp})
	before := h.m.View()
	if strings.Contains(before, "↓") {
		t.Fatal("repère affiché sans nouveau texte")
	}
	f.emit(t, event.AssistantMessage, event.AssistantMessagePayload{Text: "NOUVEAU"})
	h.until("nouveau message", func() bool { return len(h.m.tr.Items()) == 32 })
	after := h.m.View()
	if strings.Contains(after, "NOUVEAU") {
		t.Fatal("la vue a sauté en bas pendant la lecture")
	}
	if !strings.Contains(after, i18n.T(i18n.FR, "tui.scroll.new")) {
		t.Fatalf("repère de nouveau texte absent : %q", after)
	}
	h.send(tea.KeyMsg{Type: tea.KeyEnd})
	v := h.m.View()
	if !strings.Contains(v, "NOUVEAU") || strings.Contains(v, i18n.T(i18n.FR, "tui.scroll.new")) {
		t.Fatalf("Fin ne revient pas au suivi : %q", v)
	}
}
