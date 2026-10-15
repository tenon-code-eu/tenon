package engine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"codeberg.org/tenon/tenon/internal/event"
	"codeberg.org/tenon/tenon/internal/llm"
	"codeberg.org/tenon/tenon/internal/tools"
)

// fakeTool renvoie les résultats de res, dans l'ordre (le dernier se répète).
type fakeTool struct {
	res []tools.Result
	n   int
}

func (f *fakeTool) Name() string            { return "fake_read" }
func (f *fakeTool) Description() string     { return "outil factice" }
func (f *fakeTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (f *fakeTool) Risk() tools.Risk        { return tools.Read }
func (f *fakeTool) Target(json.RawMessage) (tools.Target, error) {
	return tools.Target{}, nil
}
func (f *fakeTool) Run(context.Context, json.RawMessage) (tools.Result, error) {
	i := f.n
	if i >= len(f.res) {
		i = len(f.res) - 1
	}
	f.n++
	return f.res[i], nil
}

func withFake(ft *fakeTool) func(*Deps) {
	return func(d *Deps) { d.Tools = tools.NewRegistry(ft) }
}

func TestUnavailableEmittedAfterSessionStarted(t *testing.T) {
	h := newHarness(t, euInfo, [][]llm.Chunk{text("ok")}, func(d *Deps) {
		d.UnavailableMCP = []event.McpServerDownPayload{{Server: "a", Error: "boom"}, {Server: "b", Error: "nope"}}
	})
	evs, err := h.submit(t, "s_1", "salut", nil)
	if err != nil {
		t.Fatal(err)
	}
	if evs[0].Type != event.SessionStarted || evs[1].Type != event.McpServerDown || evs[2].Type != event.McpServerDown {
		t.Fatalf("events %v", types(evs))
	}
	if p := decode[event.McpServerDownPayload](t, evs[1]); p.Server != "a" || p.Error != "boom" {
		t.Fatalf("payload %+v", p)
	}
	if n := len(ofType(evs, event.McpServerDown)); n != 2 {
		t.Fatalf("%d McpServerDown", n)
	}
	// Pas de répétition au second tour.
	h.prov.script = append(h.prov.script, text("ok"))
	evs, err = h.submit(t, "s_1", "encore", nil)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(ofType(evs, event.McpServerDown)); n != 0 {
		t.Fatalf("%d McpServerDown au second tour", n)
	}
}

func TestUnavailableEmittedOnResume(t *testing.T) {
	l := &evLog{t: t, sid: "s_1"}
	l.add(event.SessionStarted, event.SessionStartedPayload{Provider: euInfo}).
		add(event.UserMessage, event.UserMessagePayload{Text: "a"})
	h := newHarness(t, euInfo, [][]llm.Chunk{text("ok")}, func(d *Deps) {
		d.UnavailableMCP = []event.McpServerDownPayload{{Server: "a", Error: "boom"}}
	})
	if err := h.eng.Restore("s_1", l.evs); err != nil {
		t.Fatal(err)
	}
	evs, err := h.submit(t, "s_1", "b", nil)
	if err != nil {
		t.Fatal(err)
	}
	if evs[0].Type != event.SessionStarted || evs[1].Type != event.McpServerDown {
		t.Fatalf("events %v", types(evs))
	}
}

func TestServerDownAfterToolResult(t *testing.T) {
	ft := &fakeTool{res: []tools.Result{{Content: "serveur injoignable", IsError: true, ServerDown: "srv"}}}
	h := newHarness(t, euInfo, [][]llm.Chunk{call("c1", "fake_read", `{}`), text("fin")}, withFake(ft))
	evs, err := h.submit(t, "s_1", "go", nil)
	if err != nil {
		t.Fatal(err)
	}
	tr := ofType(evs, event.ToolResult)
	dn := ofType(evs, event.McpServerDown)
	if len(tr) != 1 || len(dn) != 1 || dn[0].Seq != tr[0].Seq+1 {
		t.Fatalf("events %v", types(evs))
	}
	if p := decode[event.McpServerDownPayload](t, dn[0]); p.Server != "srv" || p.Error != "serveur injoignable" {
		t.Fatalf("payload %+v", p)
	}
}

func TestServerDownNotDuplicated(t *testing.T) {
	ft := &fakeTool{res: []tools.Result{{Content: "down", IsError: true, ServerDown: "srv"}, {Content: "ok"}}}
	h := newHarness(t, euInfo, [][]llm.Chunk{
		call("c1", "fake_read", `{}`), call("c2", "fake_read", `{}`), text("fin"),
	}, withFake(ft))
	evs, err := h.submit(t, "s_1", "go", nil)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(ofType(evs, event.ToolResult)); n != 2 {
		t.Fatalf("%d ToolResult", n)
	}
	if n := len(ofType(evs, event.McpServerDown)); n != 1 {
		t.Fatalf("%d McpServerDown", n)
	}
}

func TestRestoreIgnoresServerDown(t *testing.T) {
	l := &evLog{t: t, sid: "s_1"}
	l.add(event.SessionStarted, event.SessionStartedPayload{Provider: euInfo}).
		add(event.McpServerDown, event.McpServerDownPayload{Server: "a", Error: "x"}).
		add(event.UserMessage, event.UserMessagePayload{Text: "a"}).
		add(event.McpServerDown, event.McpServerDownPayload{Server: "b", Error: "y"})
	h := newHarness(t, euInfo, [][]llm.Chunk{text("ok")})
	if err := h.eng.Restore("s_1", l.evs); err != nil {
		t.Fatal(err)
	}
	if _, err := h.submit(t, "s_1", "b", nil); err != nil {
		t.Fatal(err)
	}
	got := h.prov.reqs()[0].Messages
	want := []llm.Message{
		{Role: llm.System, Content: "Tu es un agent de test."},
		{Role: llm.User, Content: "a"},
		{Role: llm.User, Content: "b"},
	}
	if mustJSON(got) != mustJSON(want) {
		t.Fatalf("history %s", mustJSON(got))
	}
}

func TestToolDeniedPropagated(t *testing.T) {
	ft := &fakeTool{res: []tools.Result{{Content: "refusé", IsError: true, Denied: "policy"}}}
	h := newHarness(t, euInfo, [][]llm.Chunk{call("c1", "fake_read", `{}`), text("fin")}, withFake(ft))
	evs, err := h.submit(t, "s_1", "go", nil)
	if err != nil {
		t.Fatal(err)
	}
	tr := ofType(evs, event.ToolResult)
	if len(tr) != 1 {
		t.Fatalf("events %v", types(evs))
	}
	if p := decode[event.ToolResultPayload](t, tr[0]); p.Denied != "policy" {
		t.Fatalf("Denied=%q", p.Denied)
	}
}

func TestSessionStartedListsMCP(t *testing.T) {
	servers := []event.MCPServerInfo{{ID: "gh", Jurisdiction: llm.NonEU, Country: "US"}}
	h := newHarness(t, euInfo, [][]llm.Chunk{text("ok")}, func(d *Deps) { d.MCP = servers })
	evs, err := h.submit(t, "s_1", "salut", nil)
	if err != nil {
		t.Fatal(err)
	}
	if p := decode[event.SessionStartedPayload](t, evs[0]); mustJSON(p.MCP) != mustJSON(servers) {
		t.Fatalf("payload %+v", p)
	}
	// Sans serveur, le champ est omis du JSON.
	h = newHarness(t, euInfo, [][]llm.Chunk{text("ok")})
	evs, err = h.submit(t, "s_1", "salut", nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(evs[0].Payload), "mcp") {
		t.Fatalf("payload %s", evs[0].Payload)
	}
}
