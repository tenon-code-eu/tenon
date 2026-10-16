package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/i18n"
	"github.com/tenon-code-eu/tenon/internal/llm"
	"github.com/tenon-code-eu/tenon/internal/ui/headless"
)

func ev(t *testing.T, typ event.Type, payload any) event.Event {
	t.Helper()
	e, err := event.New("s", 1, time.Unix(0, 0), typ, payload)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestBannerAndUser(t *testing.T) {
	tr := NewTranscript(i18n.FR)
	p := llm.ProviderInfo{ID: "mistral", Name: "Mistral", Model: "m", Country: "FR", Jurisdiction: llm.EU}
	tr.Apply(ev(t, event.SessionStarted, event.SessionStartedPayload{Provider: p, Resumed: true, Instructions: []string{"AGENTS.md"}}))
	tr.Apply(ev(t, event.UserMessage, event.UserMessagePayload{Text: "salut"}))
	it := tr.Items()
	if len(it) != 2 || it[0].Kind != ItemBanner || it[1].Kind != ItemUser || it[1].Text != "salut" {
		t.Fatalf("items = %+v", it)
	}
	for _, want := range []string{headless.Banner(i18n.FR, p), "(reprise)", "AGENTS.md"} {
		if !strings.Contains(it[0].Text, want) {
			t.Errorf("bannière %q sans %q", it[0].Text, want)
		}
	}
}

func TestStreamingThenFinal(t *testing.T) {
	tr := NewTranscript(i18n.FR)
	for _, d := range []string{"a", "b", "c"} {
		tr.Apply(ev(t, event.AssistantDelta, event.AssistantDeltaPayload{Text: d}))
	}
	it := tr.Items()
	if len(it) != 1 || it[0].Text != "abc" || !it[0].Streaming {
		t.Fatalf("streaming = %+v", it)
	}
	tr.Apply(ev(t, event.AssistantMessage, event.AssistantMessagePayload{Text: "final"}))
	it = tr.Items()
	if len(it) != 1 || it[0].Text != "final" || it[0].Streaming {
		t.Fatalf("final = %+v", it)
	}
	tr.Apply(ev(t, event.AssistantMessage, event.AssistantMessagePayload{Text: "seul"}))
	if it = tr.Items(); len(it) != 2 || it[1].Text != "seul" {
		t.Fatalf("sans streaming = %+v", it)
	}
}

func TestToolLifecycle(t *testing.T) {
	tr := NewTranscript(i18n.FR)
	req := func(id, args string) {
		tr.Apply(ev(t, event.ToolCallRequested, map[string]any{"call_id": id, "tool": "bash", "args": map[string]any{"command": args}}))
	}
	res := func(id string, p event.ToolResultPayload) {
		p.CallID = id
		p.Tool = "bash"
		tr.Apply(ev(t, event.ToolResult, p))
	}
	req("1", "ls")
	if it := tr.Items(); it[0].Status != "pending" || it[0].Detail != "ls" || it[0].Tool != "bash" {
		t.Fatalf("pending = %+v", it[0])
	}
	res("1", event.ToolResultPayload{Content: "l1\nl2"})
	req("2", "rm")
	res("2", event.ToolResultPayload{Denied: "policy"})
	req("3", "x")
	res("3", event.ToolResultPayload{IsError: true, Content: strings.Repeat("é", 200)})
	it := tr.Items()
	if it[0].Status != "ok" || it[0].Detail != "ls · l1" {
		t.Errorf("ok = %+v", it[0])
	}
	if it[1].Status != "denied" {
		t.Errorf("denied = %+v", it[1])
	}
	if it[2].Status != "error" || len([]rune(it[2].Detail)) > len("x · ")+120 {
		t.Errorf("error = %+v", it[2])
	}
}

func TestTurnReasons(t *testing.T) {
	tr := NewTranscript(i18n.FR)
	if tr.Busy() {
		t.Fatal("busy au départ")
	}
	tr.Apply(ev(t, event.UserMessage, event.UserMessagePayload{Text: "x"}))
	if !tr.Busy() {
		t.Fatal("non busy après UserMessage")
	}
	tr.Apply(ev(t, event.TurnCompleted, event.TurnCompletedPayload{Reason: "cancelled"}))
	if tr.Busy() {
		t.Fatal("busy après TurnCompleted")
	}
	it := tr.Items()
	if last := it[len(it)-1]; last.Kind != ItemNotice || last.Text != i18n.T(i18n.FR, "tui.turn_reason.cancelled") {
		t.Fatalf("notice = %+v", last)
	}
	n := len(it)
	tr.Apply(ev(t, event.UserMessage, event.UserMessagePayload{Text: "y"}))
	tr.Apply(ev(t, event.TurnCompleted, event.TurnCompletedPayload{Reason: "done"}))
	if len(tr.Items()) != n+1 {
		t.Fatalf("done a créé une notice : %+v", tr.Items())
	}
}

func TestEnglishLabels(t *testing.T) {
	tr := NewTranscript(i18n.EN)
	tr.Apply(ev(t, event.SessionStarted, event.SessionStartedPayload{Resumed: true}))
	tr.Apply(ev(t, event.RedactionApplied, event.RedactionAppliedPayload{Counts: map[string]int{"a": 2, "b": 1}}))
	tr.Apply(ev(t, event.TurnCompleted, event.TurnCompletedPayload{Reason: "blocked"}))
	it := tr.Items()
	if !strings.Contains(it[0].Text, "(resumed)") {
		t.Errorf("banner = %q", it[0].Text)
	}
	if it[1].Kind != ItemNotice || it[1].Text != "3 value(s) masked" {
		t.Errorf("redaction = %+v", it[1])
	}
	if it[2].Text != i18n.T(i18n.EN, "tui.turn_reason.blocked") || strings.HasPrefix(it[2].Text, "!") {
		t.Errorf("reason = %+v", it[2])
	}
}

func TestUnknownEventIgnored(t *testing.T) {
	tr := NewTranscript(i18n.FR)
	tr.Apply(ev(t, event.Type("Inconnu"), map[string]any{}))
	tr.Apply(ev(t, event.PermissionResolved, map[string]any{}))
	if len(tr.Items()) != 0 {
		t.Fatalf("items = %+v", tr.Items())
	}
	bad := event.Event{Type: event.UserMessage, Payload: []byte(`{`)}
	tr.Apply(bad)
	if it := tr.Items(); len(it) != 1 || it[0].Kind != ItemError {
		t.Fatalf("décodage = %+v", it)
	}
}

func TestStreamingClosedOnTurnEnd(t *testing.T) {
	ends := map[string]event.Event{
		"turn":  ev(t, event.TurnCompleted, event.TurnCompletedPayload{Reason: "cancelled"}),
		"error": ev(t, event.Error, event.ErrorPayload{Code: "provider", Message: "x"}),
	}
	for name, end := range ends {
		tr := NewTranscript(i18n.FR)
		tr.Apply(ev(t, event.AssistantDelta, event.AssistantDeltaPayload{Text: "a"}))
		tr.Apply(end)
		tr.Apply(ev(t, event.UserMessage, event.UserMessagePayload{Text: "u"}))
		tr.Apply(ev(t, event.AssistantDelta, event.AssistantDeltaPayload{Text: "b"}))
		var as []Item
		for _, it := range tr.Items() {
			if it.Kind == ItemAssistant {
				as = append(as, it)
			}
		}
		if len(as) != 2 || as[0].Streaming || as[0].Text != "a" || as[1].Text != "b" || !as[1].Streaming {
			t.Errorf("%s : assistants = %+v", name, as)
		}
	}
}

// Avec un masquage, la ligne en cours d'un streaming est retenue ; un tour
// interrompu montre le texte entier, masqué.
func TestStreamingHoldbackWithRedact(t *testing.T) {
	tr := NewTranscript(i18n.FR)
	tr.SetRedact(func(s string) string { return strings.ReplaceAll(s, "SECRET", "[X]") })
	tr.Apply(ev(t, event.AssistantDelta, event.AssistantDeltaPayload{Text: "un\ndeux SEC"}))
	if got := tr.Items()[0].Text; got != "un" {
		t.Fatalf("streaming = %q, attendu « un »", got)
	}
	tr.Apply(ev(t, event.AssistantDelta, event.AssistantDeltaPayload{Text: "RET"}))
	tr.Apply(ev(t, event.TurnCompleted, event.TurnCompletedPayload{Reason: "cancelled"}))
	if got := tr.Items()[0].Text; got != "un\ndeux [X]" {
		t.Fatalf("après interruption = %q", got)
	}
}

func TestTranscriptShowsTyped(t *testing.T) {
	tr := NewTranscript(i18n.FR)
	tr.Apply(ev(t, event.UserMessage, event.UserMessagePayload{Text: "corps du skill", Typed: "/review main.go", Skill: "review"}))
	tr.Apply(ev(t, event.UserMessage, event.UserMessagePayload{Text: "simple"}))
	it := tr.Items()
	if len(it) != 2 || it[0].Text != "/review main.go" || it[1].Text != "simple" {
		t.Fatalf("items = %+v", it)
	}
}
