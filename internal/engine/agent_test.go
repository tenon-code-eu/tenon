package engine

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tenon-code-eu/tenon/internal/agents"
	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/i18n"
	"github.com/tenon-code-eu/tenon/internal/llm"
	"github.com/tenon-code-eu/tenon/internal/policy"
	"github.com/tenon-code-eu/tenon/internal/provider"
	"github.com/tenon-code-eu/tenon/internal/tools"
)

const agentCall = `{"description":"look","prompt":"read a.txt"}`

// newAgentEngine construit un harnais avec un catalogue d'agents issu de
// files (nom de fichier -> contenu) et les outils fichiers plus shell.
func newAgentEngine(t *testing.T, script [][]llm.Chunk, files map[string]string, opts ...func(*Deps)) *harness {
	t.Helper()
	dir := t.TempDir()
	for n, c := range files {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cat, _ := agents.Discover(i18n.EN, []agents.Dir{{Path: dir, Source: agents.Project}})
	var h *harness
	h = newHarness(t, euInfo, script, append([]func(*Deps){func(d *Deps) {
		d.Agents = cat
		d.Lang = i18n.EN
		d.Tools = tools.NewRegistry(append(tools.FS(d.ProjectRoot, 30000), tools.Shell(d.ProjectRoot, time.Second, 30000))...)
	}}, opts...)...)
	if err := os.WriteFile(filepath.Join(h.root, "a.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return h
}

func withRules(rules ...policy.Rule) func(*Deps) {
	return func(d *Deps) {
		p, err := policy.NewPermissions(d.ProjectRoot, rules)
		if err != nil {
			panic(err)
		}
		d.Permissions = p
	}
}

func parentOf(t *testing.T, e event.Event) string {
	t.Helper()
	var p struct {
		Parent string `json:"parent_call_id"`
	}
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		t.Fatal(err)
	}
	return p.Parent
}

func toolResultOf(t *testing.T, evs []event.Event, tool string) event.ToolResultPayload {
	t.Helper()
	for _, e := range ofType(evs, event.ToolResult) {
		if p := decode[event.ToolResultPayload](t, e); p.Tool == tool {
			return p
		}
	}
	t.Fatalf("no ToolResult for %s in %v", tool, types(evs))
	return event.ToolResultPayload{}
}

func defNames(r llm.Request) []string {
	var out []string
	for _, d := range r.Tools {
		out = append(out, d.Name)
	}
	return out
}

func hasName(r llm.Request, n string) bool {
	for _, d := range r.Tools {
		if d.Name == n {
			return true
		}
	}
	return false
}

func TestSubagentDelegation(t *testing.T) {
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
	type step struct {
		typ    event.Type
		parent string
	}
	var got []step
	for _, e := range evs {
		switch e.Type {
		case event.SessionStarted, event.UserMessage:
			continue
		}
		got = append(got, step{e.Type, parentOf(t, e)})
	}
	want := []step{
		{event.AssistantMessage, ""},
		{event.ToolCallRequested, ""},
		{event.SubagentStarted, ""},
		{event.AssistantMessage, "m1"},
		{event.ToolCallRequested, "m1"},
		{event.ToolResult, "m1"},
		{event.AssistantMessage, "m1"},
		{event.SubagentCompleted, ""},
		{event.ToolResult, ""},
		{event.AssistantMessage, ""},
		{event.TurnCompleted, ""},
	}
	// Les deltas du tour principal sont filtrés : seul leur parent compte.
	var filtered []step
	for _, s := range got {
		if s.typ == event.AssistantDelta {
			if s.parent != "" {
				t.Fatalf("AssistantDelta avec parent %q", s.parent)
			}
			continue
		}
		filtered = append(filtered, s)
	}
	if len(filtered) != len(want) {
		t.Fatalf("got %v\nwant %v", filtered, want)
	}
	for i := range want {
		if filtered[i] != want[i] {
			t.Fatalf("step %d: %v, want %v\nall %v", i, filtered[i], want[i], filtered)
		}
	}
	sc := decode[event.SubagentStartedPayload](t, ofType(evs, event.SubagentStarted)[0])
	if sc.CallID != "m1" || sc.Agent != "general-purpose" || sc.Description != "look" {
		t.Fatalf("SubagentStarted %+v", sc)
	}
	cp := decode[event.SubagentCompletedPayload](t, ofType(evs, event.SubagentCompleted)[0])
	if cp.CallID != "m1" || cp.Status != "ok" || cp.Iterations != 2 {
		t.Fatalf("SubagentCompleted %+v", cp)
	}
	if r := toolResultOf(t, evs, "agent"); r.Content != "a.txt says hi" || r.IsError || r.CallID != "m1" {
		t.Fatalf("ToolResult agent %+v", r)
	}
	if tc := lastTurn(t, evs); tc.Reason != "done" || tc.Iterations != 2 {
		t.Fatalf("TurnCompleted %+v", tc)
	}
}

func TestSubagentIsolation(t *testing.T) {
	h := newAgentEngine(t, [][]llm.Chunk{
		call("m1", "agent", agentCall),
		call("s1", "read_file", `{"path":"a.txt"}`),
		text("a.txt says hi"),
		text("done"),
	}, nil)
	if _, err := h.submit(t, "s_1", "SECRET-MAIN-TEXT", nil); err != nil {
		t.Fatal(err)
	}
	reqs := h.prov.reqs()
	if len(reqs) != 4 {
		t.Fatalf("%d requêtes", len(reqs))
	}
	sub := reqs[1]
	if len(sub.Messages) != 2 || sub.Messages[0].Role != llm.System || sub.Messages[1].Role != llm.User ||
		sub.Messages[1].Content != "read a.txt" {
		t.Fatalf("requête du sous-agent %+v", sub.Messages)
	}
	if !strings.Contains(sub.Messages[0].Content, i18n.T(i18n.EN, "agents.preamble")) ||
		strings.Contains(sub.Messages[0].Content, "SECRET-MAIN-TEXT") {
		t.Fatalf("prompt système %q", sub.Messages[0].Content)
	}
	if hasName(sub, "agent") {
		t.Fatalf("le sous-agent voit agent : %v", defNames(sub))
	}
	if !hasName(reqs[0], "agent") || !hasName(reqs[3], "agent") {
		t.Fatal("le tour principal doit proposer agent")
	}
}

func TestSubagentToolsRestricted(t *testing.T) {
	h := newAgentEngine(t, [][]llm.Chunk{
		call("m1", "agent", `{"agent":"reader","description":"look","prompt":"go"}`),
		call("s1", "shell", `{"command":"echo x"}`),
		text("could not"),
		text("done"),
	}, map[string]string{"reader.md": "---\nname: reader\ndescription: reads\ntools: Read\n---\nBody."})
	evs, err := h.submit(t, "s_1", "go", nil)
	if err != nil {
		t.Fatal(err)
	}
	reqs := h.prov.reqs()
	if got := defNames(reqs[1]); len(got) != 1 || got[0] != "read_file" {
		t.Fatalf("defs %v", got)
	}
	var found bool
	for _, e := range ofType(evs, event.ToolResult) {
		p := decode[event.ToolResultPayload](t, e)
		if p.Tool == "shell" {
			found = true
			if parentOf(t, e) != "m1" || !p.IsError || p.Content != "unknown tool: shell" {
				t.Fatalf("ToolResult %+v", p)
			}
		}
	}
	if !found {
		t.Fatal("pas de ToolResult shell")
	}
}

func TestSubagentNoNesting(t *testing.T) {
	h := newAgentEngine(t, [][]llm.Chunk{
		call("m1", "agent", agentCall),
		call("s1", "agent", agentCall),
		text("nope"),
		text("done"),
	}, nil)
	evs, err := h.submit(t, "s_1", "go", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ofType(evs, event.ToolResult) {
		p := decode[event.ToolResultPayload](t, e)
		if p.CallID == "s1" && (p.Content != "unknown tool: agent" || !p.IsError) {
			t.Fatalf("ToolResult %+v", p)
		}
	}
	if n := len(ofType(evs, event.SubagentStarted)); n != 1 {
		t.Fatalf("SubagentStarted x%d", n)
	}
}

func TestSubagentPermissionDenied(t *testing.T) {
	script := [][]llm.Chunk{
		call("m1", "agent", agentCall),
		call("s1", "shell", `{"command":"echo x"}`),
		text("denied"),
		text("done"),
	}
	h := newAgentEngine(t, script, nil)
	evs, err := h.submit(t, "s_1", "go", func(p event.PermissionRequestedPayload) Reply {
		if p.Tool != "shell" {
			t.Errorf("permission %+v", p)
		}
		return Reply{}
	})
	if err != nil {
		t.Fatal(err)
	}
	pr := ofType(evs, event.PermissionRequested)
	if len(pr) != 1 || parentOf(t, pr[0]) != "m1" {
		t.Fatalf("PermissionRequested %v", types(evs))
	}
	if p := toolResultOf(t, evs, "shell"); p.Denied != "user" || !p.IsError {
		t.Fatalf("ToolResult %+v", p)
	}
	if tc := lastTurn(t, evs); tc.Reason != "done" {
		t.Fatalf("TurnCompleted %+v", tc)
	}

	h = newAgentEngine(t, script, nil, withRules(policy.Rule{Tool: "shell", Match: "echo*", Decision: policy.Deny}))
	evs, err = h.submit(t, "s_1", "go", nil)
	if err != nil {
		t.Fatal(err)
	}
	if p := toolResultOf(t, evs, "shell"); p.Denied != "policy" {
		t.Fatalf("ToolResult %+v", p)
	}
	if len(ofType(evs, event.PermissionRequested)) != 0 {
		t.Fatal("aucune demande attendue avec une règle deny")
	}
}

func TestSubagentSessionApprovalShared(t *testing.T) {
	h := newAgentEngine(t, [][]llm.Chunk{
		call("m1", "shell", `{"command":"echo x"}`),
		call("m2", "agent", agentCall),
		call("s1", "shell", `{"command":"echo x"}`),
		text("ran"),
		text("done"),
	}, nil)
	evs, err := h.submit(t, "s_1", "go", func(event.PermissionRequestedPayload) Reply {
		return Reply{Allow: true, Remember: true}
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(ofType(evs, event.PermissionRequested)); n != 1 {
		t.Fatalf("PermissionRequested x%d, attendu 1", n)
	}
	var sub *event.ToolResultPayload
	for _, e := range ofType(evs, event.ToolResult) {
		if p := decode[event.ToolResultPayload](t, e); p.CallID == "s1" {
			sub = &p
		}
	}
	if sub == nil || sub.IsError || sub.Denied != "" {
		t.Fatalf("ToolResult du sous-agent %+v", sub)
	}
}

func TestSubagentMaxIterations(t *testing.T) {
	h := newAgentEngine(t, [][]llm.Chunk{
		call("m1", "agent", agentCall),
		call("s1", "read_file", `{"path":"a.txt"}`),
		call("s2", "read_file", `{"path":"a.txt"}`),
		text("done"),
	}, nil, func(d *Deps) { d.MaxIterations = 2 })
	evs, err := h.submit(t, "s_1", "go", nil)
	if err != nil {
		t.Fatal(err)
	}
	r := toolResultOf(t, evs, "agent")
	if !r.IsError || !strings.HasPrefix(r.Content, "sub-agent stopped: iteration limit reached") {
		t.Fatalf("ToolResult agent %+v", r)
	}
	cp := decode[event.SubagentCompletedPayload](t, ofType(evs, event.SubagentCompleted)[0])
	if cp.Status != "error" || cp.Iterations != 2 {
		t.Fatalf("SubagentCompleted %+v", cp)
	}
	if tc := lastTurn(t, evs); tc.Reason != "done" {
		t.Fatalf("TurnCompleted %+v", tc)
	}
}

func TestSubagentProviderError(t *testing.T) {
	h := newAgentEngine(t, [][]llm.Chunk{
		call("m1", "agent", agentCall),
		{{Text: "partial answer"}, {Done: true, Err: errors.New("boom")}},
		text("done"),
	}, nil)
	evs, err := h.submit(t, "s_1", "go", nil)
	if err != nil {
		t.Fatal(err)
	}
	errs := ofType(evs, event.Error)
	if len(errs) != 1 || parentOf(t, errs[0]) != "m1" {
		t.Fatalf("Error events %v", types(evs))
	}
	r := toolResultOf(t, evs, "agent")
	// Le texte partiel est repris après la raison de l'arrêt.
	if !r.IsError || !strings.HasPrefix(r.Content, "sub-agent stopped: ") || !strings.Contains(r.Content, "boom") {
		t.Fatalf("ToolResult agent %+v", r)
	}
	if strings.Contains(r.Content, "partial answer") {
		t.Fatalf("le texte d'un flux en erreur n'est pas conservé : %q", r.Content)
	}
	if tc := lastTurn(t, evs); tc.Reason != "done" {
		t.Fatalf("TurnCompleted %+v", tc)
	}
}

// Le texte partiel est le dernier texte d'assistant non vide du sous-agent.
func TestSubagentPartialTextIncluded(t *testing.T) {
	h := newAgentEngine(t, [][]llm.Chunk{
		call("m1", "agent", agentCall),
		{{Text: "found something"}, {Done: true, ToolCalls: []llm.ToolCall{{ID: "s1", Name: "read_file", Args: json.RawMessage(`{"path":"a.txt"}`)}}}},
		{{Done: true, Err: provider.ErrIncomplete}},
		text("done"),
	}, nil)
	evs, err := h.submit(t, "s_1", "go", nil)
	if err != nil {
		t.Fatal(err)
	}
	r := toolResultOf(t, evs, "agent")
	if !r.IsError || r.Content != "sub-agent stopped: incomplete\n\nfound something" {
		t.Fatalf("ToolResult agent %q", r.Content)
	}
}

func TestSubagentCancel(t *testing.T) {
	h := newAgentEngine(t, [][]llm.Chunk{
		call("m1", "agent", agentCall),
		call("s1", "shell", `{"command":"echo x"}`),
		text("ok"),
	}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var evs []event.Event
	done := make(chan struct{})
	collected := make(chan struct{})
	go func() {
		defer close(collected)
		for {
			select {
			case ev := <-h.sub:
				evs = append(evs, ev)
				if ev.Type == event.PermissionRequested {
					cancel()
				}
			case <-done:
				for {
					select {
					case ev := <-h.sub:
						evs = append(evs, ev)
					default:
						return
					}
				}
			}
		}
	}()
	err := h.eng.Submit(ctx, "s_1", "go")
	close(done)
	<-collected
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
	cp := ofType(evs, event.SubagentCompleted)
	if len(cp) != 1 || decode[event.SubagentCompletedPayload](t, cp[0]).Status != "cancelled" {
		t.Fatalf("SubagentCompleted %v", types(evs))
	}
	if tc := lastTurn(t, evs); tc.Reason != "cancelled" {
		t.Fatalf("TurnCompleted %+v", tc)
	}
	// La session reste utilisable : l'appel agent a été clos.
	if _, err := h.submit(t, "s_1", "again", nil); err != nil {
		t.Fatal(err)
	}
	reqs := h.prov.reqs()
	var closed bool
	for _, m := range reqs[len(reqs)-1].Messages {
		if m.Role == llm.Tool && m.ToolCallID == "m1" && strings.HasPrefix(m.Content, "cancelled: ") {
			closed = true
		}
	}
	if !closed {
		t.Fatalf("appel agent non clos : %+v", reqs[len(reqs)-1].Messages)
	}
}

func TestSubagentUnknownAndInvalid(t *testing.T) {
	for _, c := range []struct{ args, want string }{
		{`{"agent":"nope","description":"d","prompt":"p"}`, "unknown agent: nope"},
		{`{"description":"d","prompt":""}`, "invalid arguments: prompt is required"},
	} {
		h := newAgentEngine(t, [][]llm.Chunk{call("m1", "agent", c.args), text("done")}, nil)
		evs, err := h.submit(t, "s_1", "go", nil)
		if err != nil {
			t.Fatal(err)
		}
		if r := toolResultOf(t, evs, "agent"); !r.IsError || r.Content != c.want {
			t.Fatalf("ToolResult %+v, want %q", r, c.want)
		}
		if len(ofType(evs, event.SubagentStarted)) != 0 {
			t.Fatal("SubagentStarted inattendu")
		}
	}
}

func TestAgentDenyRule(t *testing.T) {
	h := newAgentEngine(t, [][]llm.Chunk{call("m1", "agent", agentCall), text("done")}, nil,
		withRules(policy.Rule{Tool: "agent", Match: "general-purpose", Decision: policy.Deny}))
	evs, err := h.submit(t, "s_1", "go", nil)
	if err != nil {
		t.Fatal(err)
	}
	if r := toolResultOf(t, evs, "agent"); r.Denied != "policy" || !r.IsError {
		t.Fatalf("ToolResult %+v", r)
	}
	if len(ofType(evs, event.SubagentStarted)) != 0 {
		t.Fatal("SubagentStarted inattendu")
	}
}

func TestNoAgentsNoTool(t *testing.T) {
	h := newHarness(t, euInfo, [][]llm.Chunk{text("ok")})
	if _, err := h.submit(t, "s_1", "go", nil); err != nil {
		t.Fatal(err)
	}
	if hasName(h.prov.reqs()[0], "agent") {
		t.Fatal("agent proposé sans catalogue")
	}
}
