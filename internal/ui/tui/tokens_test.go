package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/i18n"
	"github.com/tenon-code-eu/tenon/internal/llm"
)

func TestStatusLineTokens(t *testing.T) {
	for l, want := range map[i18n.Lang]string{
		i18n.FR: "entrée 12,4 k · sortie 1,1 k · session 340 k",
		i18n.EN: "in 12.4k · out 1.1k · session 340k",
	} {
		f := newFake()
		h := newHarness(t, f, statusOpts(l))
		h.send(tea.WindowSizeMsg{Width: 220, Height: 30})
		if s := lastLine(h.m.View()); strings.Contains(s, "session 0") || strings.Contains(s, " · in ") || strings.Contains(s, "entrée") {
			t.Fatalf("%s : sans Usage : %q", l, s)
		}
		// Session reprise : 326 500 jetons déjà comptés, puis 12 400 + 1 100.
		f.emit(t, event.Usage, event.UsagePayload{Purpose: "turn", Input: 300000, Output: 26500})
		f.emit(t, event.TurnCompleted, event.TurnCompletedPayload{Reason: "done"})
		f.emit(t, event.Usage, event.UsagePayload{Purpose: "turn", Input: 12400, Output: 1100})
		h.until("statut", func() bool { return strings.Contains(lastLine(h.m.View()), want) })
	}
}

func TestBudgetNoticesTUI(t *testing.T) {
	f := newFake()
	h := newHarness(t, f, statusOpts(i18n.EN))
	h.send(tea.WindowSizeMsg{Width: 200, Height: 30})
	f.emit(t, event.BudgetWarning, event.BudgetWarningPayload{Used: 800, Max: 1000})
	f.emit(t, event.Usage, event.UsagePayload{Purpose: "turn", Input: 700, Output: 300})
	f.emit(t, event.TurnCompleted, event.TurnCompletedPayload{Reason: "budget_exceeded"})
	h.until("avis", func() bool {
		v := h.m.View()
		return strings.Contains(v, "budget: 800 of 1000 tokens used (80 %)") && strings.Contains(v, "[budget] session_tokens")
	})
}

func TestDedicatedProviderNotShownAsSwitch(t *testing.T) {
	f := newFake()
	h := newHarness(t, f, statusOpts(i18n.EN))
	h.send(tea.WindowSizeMsg{Width: 200, Height: 30})
	small := llm.ProviderInfo{ID: "petit", Name: "Petit", Model: "small-1", Country: "FR", Jurisdiction: llm.EU}
	f.emit(t, event.ProviderSelected, event.ProviderSelectedPayload{Provider: small, Reason: "default", Purpose: "compaction"})
	f.emit(t, event.Usage, event.UsagePayload{Purpose: "compaction", Input: 5, Output: 5})
	h.until("usage", func() bool { return strings.Contains(lastLine(h.m.View()), "session 10") })
	if s := lastLine(h.m.View()); strings.Contains(s, "small-1") {
		t.Fatalf("statut : %q", s)
	}
}
