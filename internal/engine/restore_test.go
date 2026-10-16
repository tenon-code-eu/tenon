package engine

import (
	"encoding/json"
	"errors"
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
	}{{3, "c1", "A"}, {4, "c2", "annulé : session interrompue"}, {7, "c3", "annulé : session interrompue"}} {
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
