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
		UserMessagePayload{Text: "x"},
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

func TestHookStartedPayloadJSON(t *testing.T) {
	p := HookStartedPayload{RunID: "s_1-4", HookID: "h1", Event: "PreToolUse", Type: "command", Command: "echo hi", CallID: "c1"}
	got, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"run_id":"s_1-4","hook_id":"h1","event":"PreToolUse","type":"command","command":"echo hi","call_id":"c1"}`
	if string(got) != want {
		t.Fatalf("got %s", got)
	}
	minimal, _ := json.Marshal(HookStartedPayload{RunID: "r", HookID: "h", Event: "Stop", Type: "prompt", Prompt: "p"})
	wantMin := `{"run_id":"r","hook_id":"h","event":"Stop","type":"prompt","prompt":"p"}`
	if string(minimal) != wantMin {
		t.Fatalf("got %s", minimal)
	}
}

func TestHookCompletedPayloadJSON(t *testing.T) {
	// Champs obligatoires présents même vides ; les autres omis quand vides.
	empty, err := json.Marshal(HookCompletedPayload{RunID: "r", HookID: "h", Event: "Stop", Status: "ok"})
	if err != nil {
		t.Fatal(err)
	}
	wantEmpty := `{"run_id":"r","hook_id":"h","event":"Stop","status":"ok","duration_ms":0}`
	if string(empty) != wantEmpty {
		t.Fatalf("got %s", empty)
	}
	code := 2
	full, err := json.Marshal(HookCompletedPayload{
		RunID: "r", HookID: "h", Event: "PreToolUse", Status: "blocked",
		ExitCode: &code, Decision: "deny", Reason: "no", UpdatedInput: json.RawMessage(`{"a":1}`),
		SystemMessage: "sm", Stop: true, StopReason: "sr", Error: "e", DurationMS: 12,
		Stdout: "out", Stderr: "err", CallID: "c1", ParentCallID: "p1",
	})
	if err != nil {
		t.Fatal(err)
	}
	wantFull := `{"run_id":"r","hook_id":"h","event":"PreToolUse","status":"blocked","exit_code":2,"decision":"deny","reason":"no","updated_input":{"a":1},"system_message":"sm","stop":true,"stop_reason":"sr","error":"e","duration_ms":12,"stdout":"out","stderr":"err","call_id":"c1","parent_call_id":"p1"}`
	if string(full) != wantFull {
		t.Fatalf("got %s", full)
	}
	zero := 0
	withZero, _ := json.Marshal(HookCompletedPayload{RunID: "r", HookID: "h", Event: "Stop", Status: "ok", ExitCode: &zero})
	if !strings.Contains(string(withZero), `"exit_code":0`) {
		t.Fatalf("exit_code 0 must be kept: %s", withZero)
	}
}

func TestHookFieldsOmittedWhenEmpty(t *testing.T) {
	um, _ := json.Marshal(UserMessagePayload{Text: "x"})
	if string(um) != `{"text":"x"}` {
		t.Fatalf("UserMessagePayload changed: %s", um)
	}
	um, _ = json.Marshal(UserMessagePayload{Text: "x", HookContext: "ctx", Blocked: true, Hook: "Stop"})
	wantUM := `{"text":"x","hook_context":"ctx","blocked":true,"hook":"Stop"}`
	if string(um) != wantUM {
		t.Fatalf("got %s", um)
	}
	cc, _ := json.Marshal(ContextCompactedPayload{Summary: "s"})
	if strings.Contains(string(cc), "hook_context") {
		t.Fatalf("hook_context present when empty: %s", cc)
	}
	cc, _ = json.Marshal(ContextCompactedPayload{Summary: "s", HookContext: "hc"})
	if !strings.Contains(string(cc), `"hook_context":"hc"`) {
		t.Fatalf("got %s", cc)
	}
	pr, _ := json.Marshal(PermissionRequestedPayload{PermissionID: "p"})
	if strings.Contains(string(pr), "hook_reason") {
		t.Fatalf("hook_reason present when empty: %s", pr)
	}
	pr, _ = json.Marshal(PermissionRequestedPayload{PermissionID: "p", HookReason: "r"})
	if !strings.Contains(string(pr), `"hook_reason":"r"`) {
		t.Fatalf("got %s", pr)
	}
}

func TestWithParentHookPayloads(t *testing.T) {
	for _, p := range []any{
		HookStartedPayload{RunID: "r", HookID: "h", Event: "PreToolUse", Type: "command"},
		HookCompletedPayload{RunID: "r", HookID: "h", Event: "PreToolUse", Status: "ok"},
	} {
		got, _ := json.Marshal(WithParent(p, "c1"))
		if !strings.Contains(string(got), `"parent_call_id":"c1"`) {
			t.Errorf("%T: missing parent_call_id: %s", p, got)
		}
		orig, _ := json.Marshal(p)
		same, _ := json.Marshal(WithParent(p, ""))
		if string(same) != string(orig) {
			t.Errorf("%T: empty parent changed output: %s", p, same)
		}
	}
}

func TestHookEventTypes(t *testing.T) {
	if HookStarted != "HookStarted" || HookCompleted != "HookCompleted" {
		t.Fatalf("got %q %q", HookStarted, HookCompleted)
	}
}

func TestRewoundEventType(t *testing.T) {
	if Rewound != "Rewound" {
		t.Fatalf("got %q", Rewound)
	}
}

func TestRewoundPayloadJSON(t *testing.T) {
	full, err := json.Marshal(RewoundPayload{
		ToSeq: 3, Mode: "code",
		Files: []RewoundFile{
			{Path: "a.go", Action: "restored", Status: "ok", Hash: "h1"},
			{Path: "b.go", Action: "deleted", Status: "error", Error: "busy"},
		},
		UntrackedCalls: 2, Note: "n",
	})
	if err != nil {
		t.Fatal(err)
	}
	wantFull := `{"to_seq":3,"mode":"code","files":[{"path":"a.go","action":"restored","status":"ok","hash":"h1"},{"path":"b.go","action":"deleted","status":"error","error":"busy"}],"untracked_calls":2,"note":"n"}`
	if string(full) != wantFull {
		t.Fatalf("got %s", full)
	}
	minimal, err := json.Marshal(RewoundPayload{ToSeq: 3, Mode: "code"})
	if err != nil {
		t.Fatal(err)
	}
	if string(minimal) != `{"to_seq":3,"mode":"code"}` {
		t.Fatalf("got %s", minimal)
	}
}

func TestToolResultCheckpointFieldsOmittedWhenFalse(t *testing.T) {
	old := ToolResultPayload{CallID: "c", Tool: "write_file", Content: "ok", Path: "a.go", HashBefore: "b", HashAfter: "a"}
	got, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"call_id":"c","tool":"write_file","content":"ok","is_error":false,"path":"a.go","hash_before":"b","hash_after":"a"}`
	if string(got) != want {
		t.Fatalf("old payload changed:\n got %s\nwant %s", got, want)
	}
}

func TestToolResultCheckpointFieldsPresentWhenTrue(t *testing.T) {
	p := ToolResultPayload{CallID: "c", Tool: "bash", NoCheckpoint: true, Untracked: true}
	got, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"call_id":"c","tool":"bash","content":"","is_error":false,"path":"","hash_before":"","hash_after":"","no_checkpoint":true,"untracked":true}`
	if string(got) != want {
		t.Fatalf("got %s", got)
	}
}
