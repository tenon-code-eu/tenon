package engine

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/llm"
)

// evLog construit un journal d'événements persistés pour une session.
type evLog struct {
	t   *testing.T
	sid string
	seq uint64
	evs []event.Event
}

func (l *evLog) add(typ event.Type, payload any) *evLog {
	l.t.Helper()
	l.seq++
	ev, err := event.New(l.sid, l.seq, time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC), typ, payload)
	if err != nil {
		l.t.Fatal(err)
	}
	l.evs = append(l.evs, ev)
	return l
}

func TestRestoreRebuildsHistory(t *testing.T) {
	tc := llm.ToolCall{ID: "c1", Name: "read_file", Args: json.RawMessage(`{"path":"a.txt"}`)}
	l := &evLog{t: t, sid: "s_1"}
	l.add(event.SessionStarted, event.SessionStartedPayload{Provider: euInfo}).
		add(event.UserMessage, event.UserMessagePayload{Text: "lis a.txt"}).
		add(event.AssistantDelta, event.AssistantDeltaPayload{Text: "ignoré"}).
		add(event.AssistantMessage, event.AssistantMessagePayload{Text: "je lis", ToolCalls: []llm.ToolCall{tc}}).
		add(event.ToolCallRequested, event.ToolCallRequestedPayload{CallID: "c1", Tool: "read_file", Args: tc.Args}).
		add(event.ToolResult, event.ToolResultPayload{CallID: "c1", Tool: "read_file", Content: "contenu"}).
		add(event.AssistantMessage, event.AssistantMessagePayload{Text: "a.txt contient « contenu »"}).
		add(event.TurnCompleted, event.TurnCompletedPayload{Iterations: 2, Reason: "done"})

	h := newHarness(t, euInfo, [][]llm.Chunk{text("ok")})
	if err := h.eng.Restore("s_1", l.evs); err != nil {
		t.Fatal(err)
	}
	if _, err := h.submit(t, "s_1", "et ensuite ?", nil); err != nil {
		t.Fatal(err)
	}
	got := h.prov.reqs()[0].Messages
	want := []llm.Message{
		{Role: llm.System, Content: "Tu es un agent de test."},
		{Role: llm.User, Content: "lis a.txt"},
		{Role: llm.Assistant, Content: "je lis", ToolCalls: []llm.ToolCall{tc}},
		{Role: llm.Tool, Content: "contenu", ToolCallID: "c1"},
		{Role: llm.Assistant, Content: "a.txt contient « contenu »"},
		{Role: llm.User, Content: "et ensuite ?"},
	}
	if mustJSON(got) != mustJSON(want) {
		t.Fatalf("messages\n%s\nattendu\n%s", mustJSON(got), mustJSON(want))
	}
}

func TestRestoreClosesOrphanToolCalls(t *testing.T) {
	c1 := llm.ToolCall{ID: "c1", Name: "read_file", Args: json.RawMessage(`{"path":"a.txt"}`)}
	c2 := llm.ToolCall{ID: "c2", Name: "write_file", Args: json.RawMessage(`{"path":"b.txt","content":"x"}`)}
	c3 := llm.ToolCall{ID: "c3", Name: "read_file", Args: json.RawMessage(`{"path":"c.txt"}`)}
	l := &evLog{t: t, sid: "s_1"}
	l.add(event.SessionStarted, event.SessionStartedPayload{Provider: euInfo}).
		add(event.UserMessage, event.UserMessagePayload{Text: "go"}).
		add(event.AssistantMessage, event.AssistantMessagePayload{ToolCalls: []llm.ToolCall{c1, c2}}).
		add(event.ToolResult, event.ToolResultPayload{CallID: "c1", Content: "A"}).
		// Interruption : c2 sans résultat, puis un nouveau tour.
		add(event.UserMessage, event.UserMessagePayload{Text: "reprends"}).
		add(event.AssistantMessage, event.AssistantMessagePayload{ToolCalls: []llm.ToolCall{c3}})
	// c3 orphelin après le dernier événement.

	h := newHarness(t, euInfo, [][]llm.Chunk{text("ok")})
	if err := h.eng.Restore("s_1", l.evs); err != nil {
		t.Fatal(err)
	}
	if _, err := h.submit(t, "s_1", "encore", nil); err != nil {
		t.Fatal(err)
	}
	msgs := h.prov.reqs()[0].Messages
	wantRoles := []llm.Role{llm.System, llm.User, llm.Assistant, llm.Tool, llm.Tool, llm.User, llm.Assistant, llm.Tool, llm.User}
	if len(msgs) != len(wantRoles) {
		t.Fatalf("messages %s", mustJSON(msgs))
	}
	for i, r := range wantRoles {
		if msgs[i].Role != r {
			t.Fatalf("message %d : rôle %s, attendu %s : %s", i, msgs[i].Role, r, mustJSON(msgs))
		}
	}
	for _, c := range []struct {
		i       int
		id, txt string
	}{{3, "c1", "A"}, {4, "c2", "cancelled: session interrupted"}, {7, "c3", "cancelled: session interrupted"}} {
		if msgs[c.i].ToolCallID != c.id || msgs[c.i].Content != c.txt {
			t.Fatalf("message %d : %+v", c.i, msgs[c.i])
		}
	}
}

func TestRestoreContinuesSeq(t *testing.T) {
	l := &evLog{t: t, sid: "s_1", seq: 15}
	l.add(event.UserMessage, event.UserMessagePayload{Text: "a"}).
		add(event.AssistantMessage, event.AssistantMessagePayload{Text: "b"})
	h := newHarness(t, euInfo, [][]llm.Chunk{text("ok")})
	if err := h.eng.Restore("s_1", l.evs); err != nil {
		t.Fatal(err)
	}
	evs, err := h.submit(t, "s_1", "c", nil)
	if err != nil {
		t.Fatal(err)
	}
	if evs[0].Seq != 18 || evs[0].ID != "s_1-18" {
		t.Fatalf("premier événement %s seq %d", evs[0].ID, evs[0].Seq)
	}
}

func TestRestoreEmitsResumed(t *testing.T) {
	l := &evLog{t: t, sid: "s_1"}
	l.add(event.SessionStarted, event.SessionStartedPayload{Provider: euInfo}).
		add(event.UserMessage, event.UserMessagePayload{Text: "a"})
	h := newHarness(t, euInfo, [][]llm.Chunk{text("ok"), text("ok")})
	if err := h.eng.Restore("s_1", l.evs); err != nil {
		t.Fatal(err)
	}
	evs, err := h.submit(t, "s_1", "b", nil)
	if err != nil {
		t.Fatal(err)
	}
	if evs[0].Type != event.SessionStarted {
		t.Fatalf("events %v", types(evs))
	}
	if p := decode[event.SessionStartedPayload](t, evs[0]); !p.Resumed {
		t.Fatalf("SessionStarted %s", evs[0].Payload)
	}
	// Un seul SessionStarted par processus.
	evs, err = h.submit(t, "s_1", "c", nil)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(ofType(evs, event.SessionStarted)); n != 0 {
		t.Fatalf("%d SessionStarted au second tour", n)
	}
}

func TestRestoreForgetsRemember(t *testing.T) {
	w := `{"path":"out.txt","content":"x"}`
	allow := func(event.PermissionRequestedPayload) Reply { return Reply{Allow: true, Remember: true} }
	first := newHarness(t, euInfo, [][]llm.Chunk{call("c1", "write_file", w), text("fini")})
	if _, err := first.submit(t, "s_1", "écris", allow); err != nil {
		t.Fatal(err)
	}

	h := newHarness(t, euInfo, [][]llm.Chunk{call("c2", "write_file", w), text("fini")})
	if err := h.eng.Restore("s_1", first.sink.events); err != nil {
		t.Fatal(err)
	}
	evs, err := h.submit(t, "s_1", "réécris", allow)
	if err != nil {
		t.Fatal(err)
	}
	pr := ofType(evs, event.PermissionRequested)
	if len(pr) != 1 {
		t.Fatalf("events %v", types(evs))
	}
	// permN reprend après la dernière demande lue.
	if id := decode[event.PermissionRequestedPayload](t, pr[0]).PermissionID; id != "s_1-p2" {
		t.Fatalf("PermissionID %s", id)
	}
}

func TestRestoreActiveSession(t *testing.T) {
	h := newHarness(t, euInfo, [][]llm.Chunk{text("ok")})
	if _, err := h.submit(t, "s_1", "a", nil); err != nil {
		t.Fatal(err)
	}
	if err := h.eng.Restore("s_1", nil); !errors.Is(err, ErrSessionActive) {
		t.Fatalf("Restore après Submit : %v", err)
	}

	l := &evLog{t: t, sid: "s_2"}
	l.add(event.UserMessage, event.UserMessagePayload{Text: "a"})
	if err := h.eng.Restore("s_2", l.evs); err != nil {
		t.Fatal(err)
	}
	if err := h.eng.Restore("s_2", l.evs); !errors.Is(err, ErrSessionActive) {
		t.Fatalf("second Restore : %v", err)
	}
}

func TestInstructionsInSessionStarted(t *testing.T) {
	h := newHarness(t, euInfo, [][]llm.Chunk{text("ok")}, func(d *Deps) {
		d.Instructions = []string{"AGENTS.md"}
	})
	evs, err := h.submit(t, "s_1", "a", nil)
	if err != nil {
		t.Fatal(err)
	}
	p := decode[event.SessionStartedPayload](t, evs[0])
	if len(p.Instructions) != 1 || p.Instructions[0] != "AGENTS.md" || p.Resumed {
		t.Fatalf("SessionStarted %s", evs[0].Payload)
	}
}

func TestRestoreSkillMessage(t *testing.T) {
	l := &evLog{t: t, sid: "s_1"}
	l.add(event.SessionStarted, event.SessionStartedPayload{Provider: euInfo}).
		add(event.UserMessage, event.UserMessagePayload{Text: "T", Typed: "/review", Skill: "review", SkillSHA256: "ab"}).
		add(event.TurnCompleted, event.TurnCompletedPayload{Iterations: 1, Reason: "done"})
	h := newHarness(t, euInfo, [][]llm.Chunk{text("ok")})
	if err := h.eng.Restore("s_1", l.evs); err != nil {
		t.Fatal(err)
	}
	if _, err := h.submit(t, "s_1", "suite", nil); err != nil {
		t.Fatal(err)
	}
	got := h.prov.reqs()[0].Messages
	if len(got) < 2 || got[1].Role != llm.User || got[1].Content != "T" {
		t.Fatalf("messages %s", mustJSON(got))
	}
}

// subagentLog rejoue la délégation de TestSubagentDelegation et renvoie les
// événements réellement émis.
func subagentLog(t *testing.T) []event.Event {
	t.Helper()
	h := newAgentEngine(t, [][]llm.Chunk{
		call("m1", "agent", agentCall),
		call("s1", "read_file", `{"path":"a.txt"}`),
		text("a.txt says hi"),
		text("done"),
	}, nil)
	evs, err := h.submit(t, "s_1", "go", nil)
	if err != nil {
		t.Fatal(err)
	}
	return evs
}

func TestRestoreWithSubagent(t *testing.T) {
	evs := subagentLog(t)
	h := newAgentEngine(t, [][]llm.Chunk{text("ok")}, nil)
	if err := h.eng.Restore("s_1", evs); err != nil {
		t.Fatal(err)
	}
	if _, err := h.submit(t, "s_1", "next", nil); err != nil {
		t.Fatal(err)
	}
	got := h.prov.reqs()[0].Messages
	if len(got) == 0 || got[0].Role != llm.System {
		t.Fatalf("messages %s", mustJSON(got))
	}
	agentTC := llm.ToolCall{ID: "m1", Name: "agent", Args: json.RawMessage(agentCall)}
	want := []llm.Message{
		got[0],
		{Role: llm.User, Content: "go"},
		{Role: llm.Assistant, ToolCalls: []llm.ToolCall{agentTC}},
		{Role: llm.Tool, Content: "a.txt says hi", ToolCallID: "m1"},
		{Role: llm.Assistant, Content: "done"},
		{Role: llm.User, Content: "next"},
	}
	if mustJSON(got) != mustJSON(want) {
		t.Fatalf("messages\n%s\nattendu\n%s", mustJSON(got), mustJSON(want))
	}
}

func TestRestoreInterruptedSubagent(t *testing.T) {
	evs := subagentLog(t)
	var cut []event.Event
	for _, e := range evs {
		cut = append(cut, e)
		if e.Type == event.ToolCallRequested && parentOf(t, e) == "m1" {
			break
		}
	}
	h := newAgentEngine(t, [][]llm.Chunk{text("ok")}, nil)
	if err := h.eng.Restore("s_1", cut); err != nil {
		t.Fatal(err)
	}
	if _, err := h.submit(t, "s_1", "next", nil); err != nil {
		t.Fatal(err)
	}
	got := h.prov.reqs()[0].Messages
	agentTC := llm.ToolCall{ID: "m1", Name: "agent", Args: json.RawMessage(agentCall)}
	want := []llm.Message{
		got[0],
		{Role: llm.User, Content: "go"},
		{Role: llm.Assistant, ToolCalls: []llm.ToolCall{agentTC}},
		{Role: llm.Tool, Content: interruptedMsg, ToolCallID: "m1"},
		{Role: llm.User, Content: "next"},
	}
	if mustJSON(got) != mustJSON(want) {
		t.Fatalf("messages\n%s\nattendu\n%s", mustJSON(got), mustJSON(want))
	}
}

func TestRestorePermCounterIncludesNested(t *testing.T) {
	l := &evLog{t: t, sid: "s_1"}
	l.add(event.UserMessage, event.UserMessagePayload{Text: "a"}).
		add(event.PermissionRequested, event.PermissionRequestedPayload{PermissionID: "s_1-p3", ParentCallID: "m1"})
	h := newHarness(t, euInfo, [][]llm.Chunk{text("ok")})
	if err := h.eng.Restore("s_1", l.evs); err != nil {
		t.Fatal(err)
	}
	h.eng.session("s_1").turn.Lock()
	n := h.eng.session("s_1").permN
	h.eng.session("s_1").turn.Unlock()
	if n != 3 {
		t.Fatalf("permN %d, attendu 3 (prochain ID s_1-p4)", n)
	}
}

func TestRestoreHookContext(t *testing.T) {
	dir := t.TempDir()
	hs := withHooks(t,
		hk{"SessionStart", "", jsonHook(t, ctxOut("SessionStart", "START"))},
		hk{"UserPromptSubmit", "", cmd(hookScript(t, `in=$(cat); if [[ "$in" == *secret* ]]; then echo no >&2; exit 2; fi; echo CTX`))},
		hk{"Stop", "", cmd(hookScript(t, "cat >/dev/null\n"+onceFlag(filepath.Join(dir, "flag"), `echo more >&2; exit 2`)))})
	h := newHarness(t, euInfo, [][]llm.Chunk{text("a1"), text("a2"), text("b1")}, hs)
	for _, in := range []string{"hi", "secret", "bye"} {
		if _, err := h.submit(t, "s_1", in, nil); err != nil {
			t.Fatal(err)
		}
	}
	live := h.eng.sessions["s_1"].history
	js := mustJSON(live)
	if !strings.Contains(js, "[hook context]") || !strings.Contains(js, "Stop hook feedback: more") || strings.Contains(js, "secret") {
		t.Fatalf("historique vivant %s", js)
	}
	if len(ofType(h.sink.events, event.HookStarted)) == 0 || len(ofType(h.sink.events, event.HookCompleted)) == 0 {
		t.Fatal("aucun événement de hook dans le journal")
	}
	h2 := newHarness(t, euInfo, [][]llm.Chunk{text("ok")})
	if err := h2.eng.Restore("s_1", h.sink.events); err != nil {
		t.Fatal(err)
	}
	if got := mustJSON(h2.eng.sessions["s_1"].history); got != js {
		t.Fatalf("historique restauré\n%s\nattendu\n%s", got, js)
	}
}

func TestRestoreSkipsSubagentStopFeedback(t *testing.T) {
	tc := llm.ToolCall{ID: "m1", Name: "agent", Args: json.RawMessage(agentCall)}
	l := &evLog{t: t, sid: "s_1"}
	l.add(event.SessionStarted, event.SessionStartedPayload{Provider: euInfo}).
		add(event.UserMessage, event.UserMessagePayload{Text: "go"}).
		add(event.AssistantMessage, event.AssistantMessagePayload{ToolCalls: []llm.ToolCall{tc}}).
		add(event.AssistantMessage, event.AssistantMessagePayload{Text: "sub first", ParentCallID: "m1"}).
		add(event.UserMessage, event.UserMessagePayload{Text: "Stop hook feedback: once", Hook: "SubagentStop", ParentCallID: "m1"}).
		add(event.AssistantMessage, event.AssistantMessagePayload{Text: "sub second", ParentCallID: "m1"}).
		add(event.ToolResult, event.ToolResultPayload{CallID: "m1", Tool: "agent", Content: "sub second"}).
		add(event.AssistantMessage, event.AssistantMessagePayload{Text: "main done"})
	h := newHarness(t, euInfo, [][]llm.Chunk{text("ok")})
	if err := h.eng.Restore("s_1", l.evs); err != nil {
		t.Fatal(err)
	}
	want := []llm.Message{
		{Role: llm.System, Content: "Tu es un agent de test."},
		{Role: llm.User, Content: "go"},
		{Role: llm.Assistant, ToolCalls: []llm.ToolCall{tc}},
		{Role: llm.Tool, Content: "sub second", ToolCallID: "m1"},
		{Role: llm.Assistant, Content: "main done"},
	}
	if got := h.eng.sessions["s_1"].history; mustJSON(got) != mustJSON(want) {
		t.Fatalf("historique\n%s\nattendu\n%s", mustJSON(got), mustJSON(want))
	}
}
