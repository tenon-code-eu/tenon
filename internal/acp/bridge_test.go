package acp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tenon-code-eu/tenon/internal/engine"
	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/i18n"
	"github.com/tenon-code-eu/tenon/internal/llm"
)

// scripted crée un backend dont la session joue script à chaque Submit.
func scripted(setup func(s *fakeSession), script func(ctx context.Context, f *fakeSession) error) *fakeBackend {
	return &fakeBackend{newFn: func() *fakeSession {
		s := newFakeSession("s1")
		s.submit = script
		if setup != nil {
			setup(s)
		}
		return s
	}}
}

// upd décode la notification session/update m.
func upd(t *testing.T, m msg) map[string]any {
	t.Helper()
	var p struct {
		SessionID string         `json:"sessionId"`
		Update    map[string]any `json:"update"`
	}
	if err := json.Unmarshal(m.Params, &p); err != nil {
		t.Fatal(err)
	}
	if p.SessionID != "s1" && p.SessionID != "old" {
		t.Fatalf("sessionId %q", p.SessionID)
	}
	return p.Update
}

// nextUpdate rend la prochaine session/update de type kind (les autres
// sont ignorées).
func (h *harness) nextUpdate(kind string) map[string]any {
	h.t.Helper()
	for {
		u := upd(h.t, h.next("session/update"))
		if u["sessionUpdate"] == kind {
			return u
		}
	}
}

func jsonEq(t *testing.T, got any, want string) {
	t.Helper()
	var a, b any
	ga, _ := json.Marshal(got)
	json.Unmarshal(ga, &a)
	if err := json.Unmarshal([]byte(want), &b); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("got  %s\nwant %s", ga, want)
	}
}

func done(f *fakeSession) error {
	f.emit(event.TurnCompleted, event.TurnCompletedPayload{Reason: "done"})
	return nil
}

func TestMessageChunk(t *testing.T) {
	b := scripted(nil, func(ctx context.Context, f *fakeSession) error {
		f.emit(event.AssistantDelta, event.AssistantDeltaPayload{Text: "hi"})
		f.emit(event.AssistantMessage, event.AssistantMessagePayload{Text: "hi"})
		return done(f)
	})
	h := newHarness(t, b)
	h.init()
	id := h.newSession()
	p := h.send("session/prompt", map[string]any{"sessionId": id, "prompt": []any{text("x")}})
	// la mise à jour précède la réponse
	m := h.recv()
	for m.Method == "session/update" && upd(t, m)["sessionUpdate"] == "available_commands_update" {
		m = h.recv()
	}
	if m.Method != "session/update" {
		t.Fatalf("want update first, got %+v", m)
	}
	jsonEq(t, upd(t, m), `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"hi"}}`)
	r := h.read()
	if r.ID != p || string(r.Result) != `{"stopReason":"end_turn"}` {
		t.Fatalf("got %+v %s", r.Error, r.Result)
	}
	// AssistantMessage n'est pas renvoyé en direct (déjà diffusé par les deltas)
	for _, m := range h.held {
		if u := upd(t, m); u["sessionUpdate"] == "agent_message_chunk" {
			t.Fatalf("duplicate chunk %v", u)
		}
	}
}

func TestToolCallLifecycle(t *testing.T) {
	root := t.TempDir()
	// le disque diffère des arguments : le diff ne le lit jamais
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("DISK"), 0o644); err != nil {
		t.Fatal(err)
	}
	b := scripted(func(s *fakeSession) {
		s.root = root
		s.old = map[string]string{"h1": "v1 SECRET"}
	}, func(ctx context.Context, f *fakeSession) error {
		f.emit(event.ToolCallRequested, event.ToolCallRequestedPayload{CallID: "c1", Tool: "write_file", Args: json.RawMessage(`{"path":"a.go","content":"v2"}`)})
		f.emit(event.ToolResult, event.ToolResultPayload{CallID: "c1", Tool: "write_file", Content: "ok", Path: filepath.Join(root, "a.go"), HashBefore: "h1", HashAfter: "h2"})
		f.emit(event.ToolCallRequested, event.ToolCallRequestedPayload{CallID: "c2", Tool: "write_file", Args: json.RawMessage(`{"path":"b.go","content":"new"}`)})
		f.emit(event.ToolResult, event.ToolResultPayload{CallID: "c2", Tool: "write_file", Content: "ok", Path: filepath.Join(root, "b.go"), HashAfter: "h3"})
		f.emit(event.ToolCallRequested, event.ToolCallRequestedPayload{CallID: "c3", Tool: "shell", Args: json.RawMessage(`{"command":"go test ./..."}`)})
		f.emit(event.ToolResult, event.ToolResultPayload{CallID: "c3", Tool: "shell", Content: "boom\x1b[31m!", IsError: true})
		return done(f)
	})
	h := newHarness(t, b)
	h.init()
	id := h.newSession()
	h.call("session/prompt", map[string]any{"sessionId": id, "prompt": []any{text("x")}})
	jsonEq(t, h.nextUpdate("tool_call"), `{"sessionUpdate":"tool_call","toolCallId":"t0-c1","title":"write_file · a.go","kind":"edit","status":"pending",
		"rawInput":{"path":"a.go","content":"v2"},"locations":[{"path":"`+root+`/a.go"}]}`)
	// ancien texte masqué, nouveau texte depuis les arguments
	jsonEq(t, h.nextUpdate("tool_call_update"), `{"sessionUpdate":"tool_call_update","toolCallId":"t0-c1","status":"completed",
		"content":[{"type":"diff","path":"`+root+`/a.go","oldText":"v1 [MASKED]","newText":"v2"}]}`)
	h.nextUpdate("tool_call")
	// nouveau fichier : pas d'ancien texte
	jsonEq(t, h.nextUpdate("tool_call_update"), `{"sessionUpdate":"tool_call_update","toolCallId":"t0-c2","status":"completed",
		"content":[{"type":"diff","path":"`+root+`/b.go","newText":"new"}]}`)
	jsonEq(t, h.nextUpdate("tool_call"), `{"sessionUpdate":"tool_call","toolCallId":"t0-c3","title":"shell · go test ./...","kind":"execute","status":"pending",
		"rawInput":{"command":"go test ./..."}}`)
	jsonEq(t, h.nextUpdate("tool_call_update"), `{"sessionUpdate":"tool_call_update","toolCallId":"t0-c3","status":"failed",
		"content":[{"type":"content","content":{"type":"text","text":"boom!"}}]}`)
}

func TestEditDiff(t *testing.T) {
	b := scripted(func(s *fakeSession) {
		s.old = map[string]string{"h1": "a SECRET b a"}
	}, func(ctx context.Context, f *fakeSession) error {
		f.emit(event.ToolCallRequested, event.ToolCallRequestedPayload{CallID: "e1", Tool: "edit_file", Args: json.RawMessage(`{"path":"a.go","old_string":"b","new_string":"B"}`)})
		f.emit(event.ToolResult, event.ToolResultPayload{CallID: "e1", Tool: "edit_file", Content: "1 replacement(s)", Path: "/p/a.go", HashBefore: "h1"})
		f.emit(event.ToolCallRequested, event.ToolCallRequestedPayload{CallID: "e2", Tool: "edit_file", Args: json.RawMessage(`{"path":"a.go","old_string":"a","new_string":"z","replace_all":true}`)})
		f.emit(event.ToolResult, event.ToolResultPayload{CallID: "e2", Tool: "edit_file", Content: "2 replacement(s)", Path: "/p/a.go", HashBefore: "h1"})
		// old_string absent de l'ancien texte masqué : texte seul
		f.emit(event.ToolCallRequested, event.ToolCallRequestedPayload{CallID: "e3", Tool: "edit_file", Args: json.RawMessage(`{"path":"a.go","old_string":"nope","new_string":"x"}`)})
		f.emit(event.ToolResult, event.ToolResultPayload{CallID: "e3", Tool: "edit_file", Content: "1 replacement(s)", Path: "/p/a.go", HashBefore: "h1"})
		// point de reprise introuvable : texte seul
		f.emit(event.ToolCallRequested, event.ToolCallRequestedPayload{CallID: "e4", Tool: "edit_file", Args: json.RawMessage(`{"path":"a.go","old_string":"a","new_string":"x"}`)})
		f.emit(event.ToolResult, event.ToolResultPayload{CallID: "e4", Tool: "edit_file", Content: "done", Path: "/p/a.go", HashBefore: "zz"})
		return done(f)
	})
	h := newHarness(t, b)
	h.init()
	id := h.newSession()
	h.call("session/prompt", map[string]any{"sessionId": id, "prompt": []any{text("x")}})
	jsonEq(t, h.nextUpdate("tool_call_update"), `{"sessionUpdate":"tool_call_update","toolCallId":"t0-e1","status":"completed",
		"content":[{"type":"diff","path":"/p/a.go","oldText":"a [MASKED] b a","newText":"a [MASKED] B a"}]}`)
	jsonEq(t, h.nextUpdate("tool_call_update"), `{"sessionUpdate":"tool_call_update","toolCallId":"t0-e2","status":"completed",
		"content":[{"type":"diff","path":"/p/a.go","oldText":"a [MASKED] b a","newText":"z [MASKED] b z"}]}`)
	jsonEq(t, h.nextUpdate("tool_call_update"), `{"sessionUpdate":"tool_call_update","toolCallId":"t0-e3","status":"completed",
		"content":[{"type":"content","content":{"type":"text","text":"1 replacement(s)"}}]}`)
	jsonEq(t, h.nextUpdate("tool_call_update"), `{"sessionUpdate":"tool_call_update","toolCallId":"t0-e4","status":"completed",
		"content":[{"type":"content","content":{"type":"text","text":"done"}}]}`)
}

func TestToolKinds(t *testing.T) {
	for in, want := range map[string]string{
		"read_file": "read", "write_file": "edit", "edit_file": "edit", "glob": "search", "grep": "search",
		"shell": "execute", "agent": "think", "mcp__gh__issue": "other", "memory_write": "other", "": "other",
	} {
		if got := toolKind(in); got != want {
			t.Errorf("toolKind(%q) = %q, want %q", in, got, want)
		}
	}
}

// permScript émet une demande de permission p (après l'appel c1 write_file)
// puis attend la réponse et termine le tour.
func permScript(p event.PermissionRequestedPayload) func(ctx context.Context, f *fakeSession) error {
	return func(ctx context.Context, f *fakeSession) error {
		f.emit(event.ToolCallRequested, event.ToolCallRequestedPayload{CallID: "c1", Tool: p.Tool, Args: json.RawMessage(`{"path":"a.go"}`)})
		f.emit(event.PermissionRequested, p)
		select {
		case r := <-f.replies:
			f.replies <- r // gardé pour le test
			f.emit(event.PermissionResolved, event.PermissionResolvedPayload{PermissionID: p.PermissionID, Allowed: r.r.Allow, Remember: r.r.Remember, Network: r.r.Network})
		case <-ctx.Done():
			return ctx.Err()
		}
		f.emit(event.ToolResult, event.ToolResultPayload{CallID: "c1", Tool: p.Tool, Content: "ok"})
		return done(f)
	}
}

type permParams struct {
	SessionID string `json:"sessionId"`
	ToolCall  struct {
		ToolCallID string `json:"toolCallId"`
		Title      string `json:"title"`
	} `json:"toolCall"`
	Options []struct {
		OptionID string `json:"optionId"`
		Name     string `json:"name"`
		Kind     string `json:"kind"`
	} `json:"options"`
}

func (h *harness) permission() (json.RawMessage, permParams) {
	h.t.Helper()
	m := h.next("session/request_permission")
	var p permParams
	if err := json.Unmarshal(m.Params, &p); err != nil {
		h.t.Fatal(err)
	}
	return m.ID, p
}

func optionIDs(p permParams) []string {
	var ids []string
	for _, o := range p.Options {
		ids = append(ids, o.OptionID+":"+o.Kind)
	}
	return ids
}

func selected(opt string) any {
	return map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": opt}}
}

func (f *fakeSession) reply(t *testing.T) permReply {
	t.Helper()
	select {
	case r := <-f.replies:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("no Respond")
		return permReply{}
	}
}

func TestPermissionRoundTrip(t *testing.T) {
	b := scripted(nil, permScript(event.PermissionRequestedPayload{PermissionID: "p1", Kind: "tool", Tool: "write_file", Paths: []string{"/p/a.go"}}))
	h := newHarness(t, b)
	h.init()
	id := h.newSession()
	pr := h.send("session/prompt", map[string]any{"sessionId": id, "prompt": []any{text("x")}})
	rid, p := h.permission()
	if p.SessionID != "s1" || p.ToolCall.ToolCallID != "t0-c1" {
		t.Fatalf("params %+v", p)
	}
	if got := optionIDs(p); !reflect.DeepEqual(got, []string{"allow:allow_once", "allow_session:allow_always", "reject:reject_once"}) {
		t.Fatalf("options %v", got)
	}
	if p.Options[0].Name != i18n.T(i18n.EN, "acp.perm.allow") || p.Options[1].Name != i18n.T(i18n.EN, "acp.perm.allow_session") {
		t.Fatalf("names %+v", p.Options)
	}
	h.respond(rid, selected("allow_session"), nil)
	if r := b.sessions[0].reply(t); r != (permReply{"p1", engine.Reply{Allow: true, Remember: true}}) {
		t.Fatalf("reply %+v", r)
	}
	// autorisé : l'appel passe en cours (R2)
	jsonEq(t, h.nextUpdate("tool_call_update"), `{"sessionUpdate":"tool_call_update","toolCallId":"t0-c1","status":"in_progress"}`)
	if r := h.read(); r.ID != pr || string(r.Result) != `{"stopReason":"end_turn"}` {
		t.Fatalf("prompt %+v %s", r.Error, r.Result)
	}
}

func TestPermissionNetworkOption(t *testing.T) {
	b := scripted(nil, permScript(event.PermissionRequestedPayload{PermissionID: "p1", Kind: "tool", Tool: "shell", Command: "curl x", Sandboxed: true}))
	h := newHarness(t, b)
	h.init()
	id := h.newSession()
	h.send("session/prompt", map[string]any{"sessionId": id, "prompt": []any{text("x")}})
	rid, p := h.permission()
	if got := optionIDs(p); !reflect.DeepEqual(got, []string{"allow:allow_once", "allow_network:allow_once", "allow_session:allow_always", "reject:reject_once"}) {
		t.Fatalf("options %v", got)
	}
	if p.Options[1].Name != i18n.T(i18n.EN, "perm.allow_network") {
		t.Fatalf("name %q", p.Options[1].Name)
	}
	h.respond(rid, selected("allow_network"), nil)
	if r := b.sessions[0].reply(t); r.r != (engine.Reply{Allow: true, Network: true}) {
		t.Fatalf("reply %+v", r)
	}

	b2 := scripted(nil, permScript(event.PermissionRequestedPayload{PermissionID: "p1", Kind: "tool", Tool: "shell", Command: "curl x", Sandboxed: true, RuleNetwork: true}))
	h2 := newHarness(t, b2)
	h2.init()
	id = h2.newSession()
	h2.send("session/prompt", map[string]any{"sessionId": id, "prompt": []any{text("x")}})
	rid, p = h2.permission()
	if got := optionIDs(p); !reflect.DeepEqual(got, []string{"allow:allow_once", "allow_session:allow_always", "reject:reject_once"}) {
		t.Fatalf("rule network options %v", got)
	}
	if p.Options[0].Name != i18n.T(i18n.EN, "acp.perm.allow_rule_network") {
		t.Fatalf("allow name %q", p.Options[0].Name)
	}
	h2.respond(rid, selected("allow_session"), nil)
	if r := b2.sessions[0].reply(t); r.r != (engine.Reply{Allow: true, Remember: true}) {
		t.Fatalf("allow_session must not remember network: %+v", r)
	}
}

func TestJurisdictionPermission(t *testing.T) {
	b := scripted(nil, permScript(event.PermissionRequestedPayload{PermissionID: "p1", Kind: "jurisdiction",
		Provider: &llm.ProviderInfo{ID: "openai", Name: "OpenAI", Jurisdiction: llm.NonEU}}))
	h := newHarness(t, b)
	h.init()
	id := h.newSession()
	h.send("session/prompt", map[string]any{"sessionId": id, "prompt": []any{text("x")}})
	rid, p := h.permission()
	want := i18n.T(i18n.EN, "acp.perm.jurisdiction", "OpenAI", i18n.T(i18n.EN, "banner.jurisdiction.non_eu"))
	if p.ToolCall.Title != want || !strings.Contains(want, "OpenAI") || !strings.Contains(want, "non-EU") || p.ToolCall.ToolCallID != "p1" {
		t.Fatalf("toolCall %+v want title %q", p.ToolCall, want)
	}
	if got := optionIDs(p); !reflect.DeepEqual(got, []string{"allow:allow_once", "reject:reject_once"}) {
		t.Fatalf("options %v", got)
	}
	h.respond(rid, selected("reject"), nil)
	if r := b.sessions[0].reply(t); r != (permReply{"p1", engine.Reply{}}) {
		t.Fatalf("reply %+v", r)
	}
	if r := h.read(); r.Error != nil {
		t.Fatal(r.Error)
	}
}

// TestJurisdictionRefusal vérifie la réponse transmise au moteur pour chaque
// issue de refus.
func TestJurisdictionRefusal(t *testing.T) {
	for name, answer := range map[string]func(h *harness, id json.RawMessage){
		"reject": func(h *harness, id json.RawMessage) { h.respond(id, selected("reject"), nil) },
		"cancelled": func(h *harness, id json.RawMessage) {
			h.respond(id, map[string]any{"outcome": map[string]any{"outcome": "cancelled"}}, nil)
		},
		"error":   func(h *harness, id json.RawMessage) { h.respond(id, nil, &Error{Code: -32603, Message: "boom"}) },
		"unknown": func(h *harness, id json.RawMessage) { h.respond(id, selected("allow_everything"), nil) },
	} {
		t.Run(name, func(t *testing.T) {
			b := scripted(nil, permScript(event.PermissionRequestedPayload{PermissionID: "p1", Kind: "jurisdiction",
				Provider: &llm.ProviderInfo{ID: "openai", Name: "OpenAI", Jurisdiction: llm.NonEU}}))
			h := newHarness(t, b)
			h.init()
			id := h.newSession()
			h.send("session/prompt", map[string]any{"sessionId": id, "prompt": []any{text("x")}})
			rid, _ := h.permission()
			answer(h, rid)
			if r := b.sessions[0].reply(t); r != (permReply{"p1", engine.Reply{}}) {
				t.Fatalf("reply %+v", r)
			}
		})
	}
}

func TestCancelDuringPermission(t *testing.T) {
	b := scripted(nil, func(ctx context.Context, f *fakeSession) error {
		f.emit(event.ToolCallRequested, event.ToolCallRequestedPayload{CallID: "c1", Tool: "shell", Args: json.RawMessage(`{"command":"ls"}`)})
		f.emit(event.PermissionRequested, event.PermissionRequestedPayload{PermissionID: "p1", Kind: "tool", Tool: "shell", Command: "ls"})
		<-ctx.Done()
		return ctx.Err() // pas de TurnCompleted
	})
	h := newHarness(t, b)
	h.init()
	id := h.newSession()
	pr := h.send("session/prompt", map[string]any{"sessionId": id, "prompt": []any{text("x")}})
	h.permission() // le client ne répond pas
	h.notify("session/cancel", map[string]any{"sessionId": id})
	if r := b.sessions[0].reply(t); r != (permReply{"p1", engine.Reply{}}) {
		t.Fatalf("reply %+v", r)
	}
	if r := h.read(); r.ID != pr || r.Error != nil || string(r.Result) != `{"stopReason":"cancelled"}` {
		t.Fatalf("prompt %+v %s", r.Error, r.Result)
	}
}

func TestStopReasons(t *testing.T) {
	for in, want := range map[string]string{
		"done": "end_turn", "max_iterations": "max_turn_requests", "incomplete": "max_tokens", "cancelled": "cancelled",
		"blocked": "end_turn", "context_too_large": "end_turn", "budget_exceeded": "end_turn",
		"blocked_by_hook": "end_turn", "stopped_by_hook": "end_turn", "": "end_turn",
	} {
		if got := stopReason(in); got != want {
			t.Errorf("stopReason(%q) = %q, want %q", in, got, want)
		}
	}

	for _, c := range []struct {
		reason, stop string
		hook         *event.HookCompletedPayload
		notice       string // texte attendu ; "" : aucune notice
	}{
		{reason: "done", stop: "end_turn"},
		{reason: "max_iterations", stop: "max_turn_requests"},
		{reason: "incomplete", stop: "max_tokens"},
		{reason: "cancelled", stop: "cancelled"},
		{reason: "blocked", stop: "end_turn", notice: i18n.T(i18n.EN, "turn.blocked")},
		{reason: "context_too_large", stop: "end_turn", notice: i18n.T(i18n.EN, "err.context_too_large")},
		{reason: "budget_exceeded", stop: "end_turn", notice: i18n.T(i18n.EN, "tui.turn_reason.budget_exceeded")},
		{reason: "stopped_by_hook", stop: "end_turn", hook: &event.HookCompletedPayload{HookID: "h", Event: "Stop", Status: "ok", Stop: true, StopReason: "enough\nnow"},
			notice: i18n.T(i18n.EN, "headless.hook_stopped", "enough now")},
		{reason: "blocked_by_hook", stop: "end_turn", hook: &event.HookCompletedPayload{HookID: "guard", Event: "UserPromptSubmit", Status: "blocked", Reason: "no"},
			notice: i18n.T(i18n.EN, "headless.hook_blocked", "guard", "no")},
		{reason: "blocked_by_hook", stop: "end_turn", notice: i18n.T(i18n.EN, "turn.blocked_by_hook")},
	} {
		t.Run(c.reason, func(t *testing.T) {
			b := scripted(nil, func(ctx context.Context, f *fakeSession) error {
				if c.hook != nil {
					f.emit(event.HookCompleted, *c.hook)
				}
				f.emit(event.TurnCompleted, event.TurnCompletedPayload{Reason: c.reason})
				return nil
			})
			h := newHarness(t, b)
			h.init()
			id := h.newSession()
			r := h.call("session/prompt", map[string]any{"sessionId": id, "prompt": []any{text("x")}})
			if string(r.Result) != `{"stopReason":"`+c.stop+`"}` {
				t.Fatalf("result %s %+v", r.Result, r.Error)
			}
			var notices []string
			for _, m := range h.held {
				if u := upd(t, m); u["sessionUpdate"] == "agent_thought_chunk" {
					notices = append(notices, u["content"].(map[string]any)["text"].(string))
				}
			}
			var want []string
			if c.notice != "" {
				want = []string{noticePrefix + c.notice}
			}
			if !reflect.DeepEqual(notices, want) {
				t.Fatalf("notices %q, want %q", notices, want)
			}
		})
	}
}

func TestSubmitError(t *testing.T) {
	b := scripted(nil, func(ctx context.Context, f *fakeSession) error { return errors.New("provider down") })
	h := newHarness(t, b)
	h.init()
	id := h.newSession()
	r := h.call("session/prompt", map[string]any{"sessionId": id, "prompt": []any{text("x")}})
	if r.Error == nil || r.Error.Code != CodeInternal || r.Error.Message != "provider down" {
		t.Fatalf("got %+v %s", r.Error, r.Result)
	}
}

func TestSubmitErrorMasked(t *testing.T) {
	b := scripted(nil, func(ctx context.Context, f *fakeSession) error { return errors.New("auth failed: key SECRET") })
	h := newHarness(t, b)
	h.init()
	id := h.newSession()
	r := h.call("session/prompt", map[string]any{"sessionId": id, "prompt": []any{text("x")}})
	if r.Error == nil || r.Error.Message != "auth failed: key [MASKED]" {
		t.Fatalf("got %+v %s", r.Error, r.Result)
	}
}

func TestNoticesAndCommands(t *testing.T) {
	b := &fakeBackend{newFn: func() *fakeSession {
		s := newFakeSession("s1")
		s.notices = []string{"profile: strict", "editor MCP servers ignored ([acp] accept_editor_mcp)"}
		s.cmds = []Command{{Name: "fix", Description: "Fix it"}, {Name: "review", Description: "Review"}}
		return s
	}}
	h := newHarness(t, b)
	h.init()
	h.send("session/new", map[string]any{"cwd": "/p", "mcpServers": []any{}})
	// ordre strict : réponse, notices, commandes
	if m := h.recv(); m.Method != "" || m.Result == nil {
		t.Fatalf("want the response first, got %+v", m)
	}
	jsonEq(t, upd(t, h.recv()), `{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":"tenon: profile: strict"}}`)
	jsonEq(t, upd(t, h.recv()), `{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":"tenon: editor MCP servers ignored ([acp] accept_editor_mcp)"}}`)
	jsonEq(t, upd(t, h.recv()), `{"sessionUpdate":"available_commands_update","availableCommands":[{"name":"fix","description":"Fix it"},{"name":"review","description":"Review"}]}`)
}

func TestLoadReplay(t *testing.T) {
	root := t.TempDir()
	b := &fakeBackend{loadFn: func(id string) (*fakeSession, []event.Event) {
		s := newFakeSession(id)
		s.root = root
		var evs []event.Event
		add := func(typ event.Type, p any) {
			ev, _ := event.New(id, uint64(len(evs)+1), time.Now(), typ, p)
			evs = append(evs, ev)
		}
		add(event.SessionStarted, event.SessionStartedPayload{})
		add(event.UserMessage, event.UserMessagePayload{Text: "expanded skill", Typed: "/fix it"})
		add(event.AssistantMessage, event.AssistantMessagePayload{Text: "sub text", ParentCallID: "a1"})
		add(event.ToolCallRequested, event.ToolCallRequestedPayload{CallID: "c1", Tool: "read_file", Args: json.RawMessage(`{"path":"a.go"}`)})
		add(event.ToolResult, event.ToolResultPayload{CallID: "c1", Tool: "read_file", Content: "pkg a"})
		add(event.ToolCallRequested, event.ToolCallRequestedPayload{CallID: "c2", Tool: "shell", Args: json.RawMessage(`{"command":"x"}`)})
		add(event.ToolResult, event.ToolResultPayload{CallID: "c2", Tool: "shell", Content: "no", IsError: true})
		add(event.AssistantMessage, event.AssistantMessagePayload{Text: "done"})
		add(event.TurnCompleted, event.TurnCompletedPayload{Reason: "done"})
		return s, evs
	}}
	h := newHarness(t, b)
	h.init()
	h.send("session/load", map[string]any{"sessionId": "old", "cwd": root, "mcpServers": []any{}})
	if m := h.recv(); m.Method != "" || string(m.Result) != "{}" {
		t.Fatalf("want the response first, got %+v", m)
	}
	jsonEq(t, upd(t, h.recv()), `{"sessionUpdate":"available_commands_update","availableCommands":[]}`)
	var got []string
	for i := 0; i < 6; i++ {
		b, _ := json.Marshal(upd(t, h.recv()))
		got = append(got, string(b))
	}
	want := []string{
		`{"content":{"text":"/fix it","type":"text"},"sessionUpdate":"user_message_chunk"}`,
		`{"kind":"read","locations":[{"path":"` + root + `/a.go"}],"rawInput":{"path":"a.go"},"sessionUpdate":"tool_call","status":"pending","title":"read_file · a.go","toolCallId":"t1-c1"}`,
		`{"content":[{"content":{"text":"pkg a","type":"text"},"type":"content"}],"sessionUpdate":"tool_call_update","status":"completed","toolCallId":"t1-c1"}`,
		`{"kind":"execute","rawInput":{"command":"x"},"sessionUpdate":"tool_call","status":"pending","title":"shell · x","toolCallId":"t1-c2"}`,
		`{"content":[{"content":{"text":"no","type":"text"},"type":"content"}],"sessionUpdate":"tool_call_update","status":"failed","toolCallId":"t1-c2"}`,
		`{"content":{"text":"done","type":"text"},"sessionUpdate":"agent_message_chunk"}`,
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("update %d\ngot  %s\nwant %s", i, got[i], want[i])
		}
	}
}

// TestLoadClosesIdleSession : recharger une session inactive de la
// connexion la ferme avant LoadSession (son moteur tient le verrou) ; un
// prompt reçu pendant le rechargement vise une session inconnue.
func TestLoadClosesIdleSession(t *testing.T) {
	loading := make(chan struct{})
	proceed := make(chan struct{})
	closedAtLoad := -1
	b := &fakeBackend{}
	b.loadFn = func(id string) (*fakeSession, []event.Event) {
		live := b.sessions[0]
		live.mu.Lock()
		closedAtLoad = live.closed
		live.mu.Unlock()
		close(loading)
		<-proceed
		return newFakeSession(id), nil
	}
	h := newHarness(t, b)
	h.init()
	id := h.newSession()
	lid := h.send("session/load", map[string]any{"sessionId": id, "cwd": "/p", "mcpServers": []any{}})
	<-loading
	if r := h.call("session/prompt", map[string]any{"sessionId": id, "prompt": []any{text("x")}}); r.Error == nil || r.Error.Code != CodeUnknownSession {
		t.Fatalf("prompt during load: %+v", r)
	}
	close(proceed)
	if r := h.read(); r.ID != lid || r.Error != nil || string(r.Result) != "{}" {
		t.Fatalf("load: %+v %s", r.Error, r.Result)
	}
	if closedAtLoad != 1 {
		t.Fatalf("live session closed %d time(s) before LoadSession", closedAtLoad)
	}
	if r := h.call("session/prompt", map[string]any{"sessionId": id, "prompt": []any{text("x")}}); r.Error != nil {
		t.Fatalf("prompt after load: %+v", r.Error)
	}
}

func TestLoadBusySession(t *testing.T) {
	b := &fakeBackend{newFn: func() *fakeSession {
		s := newFakeSession("s1")
		s.release = make(chan struct{})
		return s
	}}
	h := newHarness(t, b)
	h.init()
	id := h.newSession()
	h.send("session/prompt", map[string]any{"sessionId": id, "prompt": []any{text("x")}})
	<-b.sessions[0].entered
	r := h.call("session/load", map[string]any{"sessionId": id, "cwd": "/p", "mcpServers": []any{}})
	if r.Error == nil || r.Error.Code != CodeInternal || r.Error.Message != "turn in progress" {
		t.Fatalf("got %+v", r.Error)
	}
	if b.sessions[0].closed != 0 || len(b.sessions) != 1 {
		t.Fatalf("live session touched: closed %d, sessions %d", b.sessions[0].closed, len(b.sessions))
	}
	close(b.sessions[0].release)
}

func TestUsageUpdate(t *testing.T) {
	b := scripted(nil, func(ctx context.Context, f *fakeSession) error {
		f.emit(event.Usage, event.UsagePayload{Purpose: "turn", Input: 100, Output: 20, Cached: 5})
		f.emit(event.Usage, event.UsagePayload{Purpose: "subagent", Input: 7, Output: 1, ParentCallID: "a1"})
		f.emit(event.Usage, event.UsagePayload{Purpose: "turn", Input: 150, Output: 30})
		return done(f)
	})
	h := newHarness(t, b)
	h.init()
	id := h.newSession()
	h.call("session/prompt", map[string]any{"sessionId": id, "prompt": []any{text("x")}})
	jsonEq(t, h.nextUpdate("usage_update"), `{"sessionUpdate":"usage_update","used":120,"_meta":{"tenon":{"input":100,"output":20,"cached":5,"sessionTokens":120}}}`)
	jsonEq(t, h.nextUpdate("usage_update"), `{"sessionUpdate":"usage_update","used":180,"_meta":{"tenon":{"input":150,"output":30,"cached":0,"sessionTokens":308}}}`)
}

func TestSubagentEvents(t *testing.T) {
	b := scripted(nil, func(ctx context.Context, f *fakeSession) error {
		f.emit(event.ToolCallRequested, event.ToolCallRequestedPayload{CallID: "a1", Tool: "agent", Args: json.RawMessage(`{"description":"look"}`)})
		f.emit(event.AssistantMessage, event.AssistantMessagePayload{Text: "sub says", ParentCallID: "a1"})
		f.emit(event.ToolCallRequested, event.ToolCallRequestedPayload{CallID: "s1c", Tool: "grep", Args: json.RawMessage(`{"pattern":"x"}`), ParentCallID: "a1"})
		f.emit(event.PermissionRequested, event.PermissionRequestedPayload{PermissionID: "p1", Kind: "tool", Tool: "grep", ParentCallID: "a1"})
		f.reply(t)
		f.emit(event.ToolResult, event.ToolResultPayload{CallID: "s1c", Tool: "grep", Content: "hit", ParentCallID: "a1"})
		f.emit(event.ToolResult, event.ToolResultPayload{CallID: "a1", Tool: "agent", Content: "found"})
		return done(f)
	})
	h := newHarness(t, b)
	h.init()
	id := h.newSession()
	h.send("session/prompt", map[string]any{"sessionId": id, "prompt": []any{text("x")}})
	rid, p := h.permission()
	if p.ToolCall.ToolCallID != "t0-a1/s1c" {
		t.Fatalf("sub-agent permission toolCallId %q", p.ToolCall.ToolCallID)
	}
	h.respond(rid, selected("allow"), nil)
	h.read()
	var kinds []string
	for _, m := range h.held {
		u := upd(t, m)
		kinds = append(kinds, u["sessionUpdate"].(string)+":"+asString(u["toolCallId"]))
	}
	want := []string{"available_commands_update:", "tool_call:t0-a1", "tool_call:t0-a1/s1c", "tool_call_update:t0-a1/s1c", "tool_call_update:t0-a1"}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("updates %v, want %v", kinds, want)
	}
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

// TestPumpBetweenPrompts : un événement émis hors tour est relayé (R8).
func TestPumpBetweenPrompts(t *testing.T) {
	b := &fakeBackend{}
	h := newHarness(t, b)
	h.init()
	h.newSession()
	b.sessions[0].emit(event.McpServerDown, event.McpServerDownPayload{Server: "gh", Error: "gone"})
	jsonEq(t, h.nextUpdate("agent_thought_chunk"), `{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":"tenon: `+i18n.T(i18n.EN, "mcp.down", "gh", "gone")+`"}}`)
}

// TestCloseDrains : une session qui émet encore pendant Close (envoi
// bloquant, tampon plein) ne bloque pas la fermeture : la pompe draine.
func TestCloseDrains(t *testing.T) {
	b := &fakeBackend{newFn: func() *fakeSession {
		s := newFakeSession("s1")
		s.onClose = func(f *fakeSession) {
			for i := 0; i < 3*cap(f.ch); i++ {
				f.emit(event.AssistantDelta, event.AssistantDeltaPayload{Text: "x"})
			}
		}
		return s
	}}
	h := newHarness(t, b)
	h.init()
	h.newSession()
	go func() {
		for range h.lines {
		}
	}()
	h.in.Close()
	select {
	case <-h.done:
	case <-time.After(5 * time.Second):
		t.Fatal("Serve hung while closing the session")
	}
}

// TestMaskLiveEvents : chaque charge utile est masquée avant d'atteindre
// l'éditeur (le moteur ne masque que vers le fournisseur) ; une charge que
// le masquage rend illisible n'est pas envoyée, et une demande de
// permission illisible est refusée sans être posée.
func TestMaskLiveEvents(t *testing.T) {
	b := scripted(func(s *fakeSession) { s.breakPayload = "BREAK" }, func(ctx context.Context, f *fakeSession) error {
		f.emit(event.AssistantDelta, event.AssistantDeltaPayload{Text: "BREAK"})
		f.emit(event.ToolCallRequested, event.ToolCallRequestedPayload{CallID: "c1", Tool: "shell", Args: json.RawMessage(`{"command":"echo SECRET"}`)})
		f.emit(event.PermissionRequested, event.PermissionRequestedPayload{PermissionID: "p1", Kind: "tool", Tool: "shell", Command: "echo SECRET", Args: json.RawMessage(`{"command":"echo SECRET"}`)})
		r := f.reply(t)
		f.emit(event.PermissionResolved, event.PermissionResolvedPayload{PermissionID: "p1", Allowed: r.r.Allow})
		f.emit(event.ToolResult, event.ToolResultPayload{CallID: "c1", Tool: "shell", Content: "SECRET"})
		f.emit(event.PermissionRequested, event.PermissionRequestedPayload{PermissionID: "p2", Kind: "tool", Tool: "shell", Command: "BREAK"})
		if r := f.reply(t); r != (permReply{"p2", engine.Reply{}}) {
			t.Errorf("unreadable permission: %+v", r)
		}
		f.emit(event.HookCompleted, event.HookCompletedPayload{HookID: "h", Status: "blocked", Reason: "SECRET"})
		f.emit(event.McpServerDown, event.McpServerDownPayload{Server: "gh", Error: "SECRET"})
		return done(f)
	})
	h := newHarness(t, b)
	h.init()
	id := h.newSession()
	pid := h.send("session/prompt", map[string]any{"sessionId": id, "prompt": []any{text("x")}})
	rid, p := h.permission()
	if p.ToolCall.Title != "shell · echo [MASKED]" {
		t.Fatalf("permission title %q", p.ToolCall.Title)
	}
	h.respond(rid, selected("allow"), nil)
	if r := h.read(); r.ID != pid || r.Error != nil {
		t.Fatalf("prompt %+v", r)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	asked := 0
	for _, m := range h.all {
		line, _ := json.Marshal(m)
		if strings.Contains(string(line), "SECRET") || strings.Contains(string(line), "BREAK") {
			t.Fatalf("unmasked line %s", line)
		}
		if m.Method == "session/request_permission" {
			asked++
		}
	}
	if asked != 1 {
		t.Fatalf("%d permission request(s)", asked)
	}
}

// TestMaskReplay : les événements rejoués par session/load sont masqués ;
// un événement illisible une fois masqué n'est pas rejoué.
func TestMaskReplay(t *testing.T) {
	b := &fakeBackend{loadFn: func(id string) (*fakeSession, []event.Event) {
		s := newFakeSession(id)
		s.breakPayload = "BREAK"
		var evs []event.Event
		add := func(typ event.Type, p any) {
			ev, _ := event.New(id, uint64(len(evs)+1), time.Now(), typ, p)
			evs = append(evs, ev)
		}
		add(event.UserMessage, event.UserMessagePayload{Text: "key SECRET"})
		add(event.AssistantMessage, event.AssistantMessagePayload{Text: "BREAK"})
		add(event.ToolCallRequested, event.ToolCallRequestedPayload{CallID: "c1", Tool: "read_file", Args: json.RawMessage(`{"path":"SECRET.txt"}`)})
		add(event.ToolResult, event.ToolResultPayload{CallID: "c1", Tool: "read_file", Content: "SECRET"})
		add(event.AssistantMessage, event.AssistantMessagePayload{Text: "done"})
		return s, evs
	}}
	h := newHarness(t, b)
	h.init()
	h.send("session/load", map[string]any{"sessionId": "old", "cwd": "/p", "mcpServers": []any{}})
	h.recv()
	h.recv() // available_commands_update
	var got []string
	for i := 0; i < 4; i++ {
		b, _ := json.Marshal(upd(t, h.recv()))
		got = append(got, string(b))
	}
	want := []string{
		`{"content":{"text":"key [MASKED]","type":"text"},"sessionUpdate":"user_message_chunk"}`,
		`{"kind":"read","locations":[{"path":"/p/[MASKED].txt"}],"rawInput":{"path":"[MASKED].txt"},"sessionUpdate":"tool_call","status":"pending","title":"read_file · [MASKED].txt","toolCallId":"t1-c1"}`,
		`{"content":[{"content":{"text":"[MASKED]","type":"text"},"type":"content"}],"sessionUpdate":"tool_call_update","status":"completed","toolCallId":"t1-c1"}`,
		`{"content":{"text":"done","type":"text"},"sessionUpdate":"agent_message_chunk"}`,
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("update %d\ngot  %s\nwant %s", i, got[i], want[i])
		}
	}
}

func TestUsageProviderMeta(t *testing.T) {
	b := scripted(nil, func(ctx context.Context, f *fakeSession) error {
		f.emit(event.Usage, event.UsagePayload{Provider: "mistral", Purpose: "turn", Input: 10, Output: 2})
		return done(f)
	})
	h := newHarness(t, b)
	h.init()
	id := h.newSession()
	h.call("session/prompt", map[string]any{"sessionId": id, "prompt": []any{text("x")}})
	jsonEq(t, h.nextUpdate("usage_update"), `{"sessionUpdate":"usage_update","used":12,"_meta":{"tenon":{"input":10,"output":2,"cached":0,"sessionTokens":12,"provider":"mistral"}}}`)
}

// Les fournisseurs reprennent call_1 à chaque tour : l'identifiant ACP porte
// le numéro du tour (messages de l'agent principal), sous-agents compris.
func TestToolCallIDsPerTurn(t *testing.T) {
	b := scripted(nil, func(ctx context.Context, f *fakeSession) error {
		f.emit(event.UserMessage, event.UserMessagePayload{Text: "x"})
		f.emit(event.ToolCallRequested, event.ToolCallRequestedPayload{CallID: "call_1", Tool: "agent", Args: json.RawMessage(`{"description":"d"}`)})
		// Message reçu par le sous-agent : pas un nouveau tour.
		f.emit(event.UserMessage, event.UserMessagePayload{Text: "sub", ParentCallID: "call_1"})
		f.emit(event.ToolCallRequested, event.ToolCallRequestedPayload{CallID: "call_1", Tool: "grep", Args: json.RawMessage(`{"pattern":"x"}`), ParentCallID: "call_1"})
		f.emit(event.ToolResult, event.ToolResultPayload{CallID: "call_1", Tool: "grep", Content: "hit", ParentCallID: "call_1"})
		f.emit(event.ToolResult, event.ToolResultPayload{CallID: "call_1", Tool: "agent", Content: "ok"})
		return done(f)
	})
	h := newHarness(t, b)
	h.init()
	id := h.newSession()
	h.call("session/prompt", map[string]any{"sessionId": id, "prompt": []any{text("un")}})
	h.call("session/prompt", map[string]any{"sessionId": id, "prompt": []any{text("deux")}})
	var ids []string
	for _, m := range h.held {
		if u := upd(t, m); u["sessionUpdate"] == "tool_call" || u["sessionUpdate"] == "tool_call_update" {
			ids = append(ids, u["sessionUpdate"].(string)+":"+asString(u["toolCallId"]))
		}
	}
	want := []string{
		"tool_call:t1-call_1", "tool_call:t1-call_1/call_1", "tool_call_update:t1-call_1/call_1", "tool_call_update:t1-call_1",
		"tool_call:t2-call_1", "tool_call:t2-call_1/call_1", "tool_call_update:t2-call_1/call_1", "tool_call_update:t2-call_1",
	}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("ids %v, want %v", ids, want)
	}
}
