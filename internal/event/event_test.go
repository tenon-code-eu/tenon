package event

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestNewMarshalsPayload(t *testing.T) {
	ts := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	e, err := New("s_1", 1, ts, UserMessage, UserMessagePayload{Text: "salut"})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(e)
	want := `{"id":"s_1-1","session":"s_1","seq":1,"ts":"2026-10-14T10:00:00Z","type":"UserMessage","payload":{"text":"salut"}}`
	if string(got) != want {
		t.Fatalf("got %s", got)
	}
}

func TestWithParent(t *testing.T) {
	payloads := []any{
		AssistantMessagePayload{Text: "x"},
		ToolCallRequestedPayload{CallID: "a", Args: json.RawMessage(`{}`)},
		ToolResultPayload{CallID: "a"},
		PermissionRequestedPayload{PermissionID: "p"},
		PermissionResolvedPayload{PermissionID: "p"},
		ContextCompactedPayload{Summary: "s"},
		RedactionAppliedPayload{Counts: map[string]int{"k": 1}},
		JurisdictionWarningPayload{},
		ErrorPayload{Code: "c"},
		McpServerDownPayload{Server: "s"},
	}
	for _, p := range payloads {
		orig, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(orig), "parent_call_id") {
			t.Errorf("%T: parent_call_id present without parent", p)
		}
		got, _ := json.Marshal(WithParent(p, "c1"))
		if !strings.Contains(string(got), `"parent_call_id":"c1"`) {
			t.Errorf("%T: missing parent_call_id: %s", p, got)
		}
		same, _ := json.Marshal(WithParent(p, ""))
		if string(same) != string(orig) {
			t.Errorf("%T: empty parent changed output: %s", p, same)
		}
	}
	tc := TurnCompletedPayload{}
	if WithParent(tc, "c1") != any(tc) {
		t.Error("TurnCompletedPayload must stay unchanged")
	}
}
