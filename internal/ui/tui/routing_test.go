package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/i18n"
	"github.com/tenon-code-eu/tenon/internal/llm"
)

func lastLine(s string) string {
	lines := strings.Split(s, "\n")
	return lines[len(lines)-1]
}

func TestRoutingTUIIndicator(t *testing.T) {
	for l, want := range map[i18n.Lang]string{i18n.FR: "sensible → ollama", i18n.EN: "sensitive → ollama"} {
		f := newFake()
		h := newHarness(t, f, statusOpts(l))
		h.send(tea.WindowSizeMsg{Width: 200, Height: 30})
		f.emit(t, event.SessionStarted, event.SessionStartedPayload{
			Provider: statusOpts(l).Provider, Routing: &event.RoutingInfo{Local: "ollama"},
		})
		h.until("bannière", func() bool { return len(h.m.tr.Items()) == 1 })
		if s := lastLine(h.m.View()); strings.Contains(s, want) {
			t.Fatalf("%s : indicateur avant classification : %q", l, s)
		}
		f.emit(t, event.SessionClassified, event.SessionClassifiedPayload{Reason: "path", Source: "tool", Path: "rh/paie.csv"})
		h.until("indicateur", func() bool { return strings.Contains(lastLine(h.m.View()), want) })
	}
}

// Sans [routing], un fournisseur courant local est le fournisseur local.
func TestRoutingTUIIndicatorCurrentLocal(t *testing.T) {
	f := newFake()
	opts := statusOpts(i18n.FR)
	opts.Provider = llm.ProviderInfo{ID: "ollama", Name: "Ollama", Model: "llama", Jurisdiction: llm.Local}
	h := newHarness(t, f, opts)
	h.send(tea.WindowSizeMsg{Width: 200, Height: 30})
	f.emit(t, event.SessionClassified, event.SessionClassifiedPayload{Reason: "marker", Source: "tool", Marker: "DIFFUSION RESTREINTE"})
	h.until("indicateur", func() bool { return strings.Contains(lastLine(h.m.View()), "sensible → ollama") })
}

func TestRoutingTUIFallbackNotice(t *testing.T) {
	f := newFake()
	h := newHarness(t, f, statusOpts(i18n.EN))
	h.send(tea.WindowSizeMsg{Width: 200, Height: 30})
	f.emit(t, event.ProviderSelected, event.ProviderSelectedPayload{
		Provider: llm.ProviderInfo{ID: "mistral", Model: "mistral-large", Jurisdiction: llm.EU}, Reason: "default",
	})
	f.emit(t, event.ProviderSelected, event.ProviderSelectedPayload{
		Provider: llm.ProviderInfo{ID: "scaleway", Name: "Scaleway", Model: "qwen", Jurisdiction: llm.EU},
		Reason:   "fallback", Error: "provider unavailable: HTTP status 503",
	})
	h.until("notice", func() bool {
		for _, it := range h.m.tr.Items() {
			if it.Kind == ItemNotice && it.Text == "routing: mistral failed (provider unavailable: HTTP status 503), using scaleway" {
				return true
			}
		}
		return false
	})
	// La barre d'état suit le fournisseur effectif.
	if s := lastLine(h.m.View()); !strings.Contains(s, "qwen") || !strings.Contains(s, "Scaleway") {
		t.Fatalf("barre d'état : %q", s)
	}
}
