package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/tenon-code-eu/tenon/internal/engine"
	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/i18n"
)

type fakeSession struct {
	id      string
	cmds    []Command
	mu      sync.Mutex
	inputs  []engine.Input
	closed  int
	entered chan struct{}
	release chan struct{} // si non nil, Submit bloque jusqu'à release ou ctx
	ch      chan event.Event
}

func newFakeSession(id string) *fakeSession {
	return &fakeSession{id: id, ch: make(chan event.Event, 8), entered: make(chan struct{}, 4)}
}
func (f *fakeSession) ID() string                    { return f.id }
func (f *fakeSession) ProjectRoot() string           { return "/p" }
func (f *fakeSession) Subscribe() <-chan event.Event { return f.ch }
func (f *fakeSession) Submit(ctx context.Context, in engine.Input) error {
	f.mu.Lock()
	f.inputs = append(f.inputs, in)
	f.mu.Unlock()
	f.entered <- struct{}{}
	if f.release != nil {
		select {
		case <-f.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
func (f *fakeSession) Respond(string, engine.Reply) error { return nil }
func (f *fakeSession) Notices() []string                  { return nil }
func (f *fakeSession) Commands() []Command                { return f.cmds }
func (f *fakeSession) OldText(string) (string, bool)      { return "", false }
func (f *fakeSession) Close() {
	f.mu.Lock()
	f.closed++
	f.mu.Unlock()
}

type fakeBackend struct {
	mu       sync.Mutex
	sessions []*fakeSession
	newFn    func() *fakeSession
	err      error
	client   *Implementation
}

func (b *fakeBackend) NewSession(ctx context.Context, cwd string, mcp []MCPServer, client *Implementation) (Session, error) {
	if b.err != nil {
		return nil, b.err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.client = client
	var s *fakeSession
	if b.newFn != nil {
		s = b.newFn()
	} else {
		s = newFakeSession("s1")
	}
	b.sessions = append(b.sessions, s)
	return s, nil
}
func (b *fakeBackend) LoadSession(ctx context.Context, id, cwd string, mcp []MCPServer, client *Implementation) (Session, []event.Event, error) {
	s := newFakeSession(id)
	b.mu.Lock()
	b.sessions = append(b.sessions, s)
	b.mu.Unlock()
	return s, nil, nil
}

// harness pilote un Server via des pipes.
type harness struct {
	t      *testing.T
	in     io.WriteCloser
	out    *bufio.Scanner
	done   chan error
	cancel context.CancelFunc
	nextID int
}

func newHarness(t *testing.T, b Backend) *harness {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	h := &harness{t: t, in: inW, out: bufio.NewScanner(outR), done: make(chan error, 1), cancel: cancel}
	go func() {
		err := NewServer(b, "9.9", i18n.EN).Serve(ctx, inR, outW)
		outW.Close()
		h.done <- err
	}()
	t.Cleanup(func() { cancel(); inW.Close() })
	return h
}

func (h *harness) send(method string, params any) int {
	h.nextID++
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": h.nextID, "method": method, "params": params})
	h.in.Write(append(b, '\n'))
	return h.nextID
}

func (h *harness) notify(method string, params any) {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	h.in.Write(append(b, '\n'))
}

type reply struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *Error          `json:"error"`
}

func (h *harness) read() reply {
	h.t.Helper()
	ch := make(chan reply, 1)
	go func() {
		if h.out.Scan() {
			var r reply
			json.Unmarshal(h.out.Bytes(), &r)
			ch <- r
		} else {
			ch <- reply{ID: -1}
		}
	}()
	select {
	case r := <-ch:
		return r
	case <-time.After(5 * time.Second):
		h.t.Fatal("timeout waiting for reply")
		return reply{}
	}
}

func (h *harness) call(method string, params any) reply {
	h.t.Helper()
	id := h.send(method, params)
	r := h.read()
	if r.ID != id {
		h.t.Fatalf("id %d, want %d", r.ID, id)
	}
	return r
}

func (h *harness) init() {
	h.t.Helper()
	if r := h.call("initialize", map[string]any{"protocolVersion": 1, "clientInfo": map[string]any{"name": "zed", "version": "1"}}); r.Error != nil {
		h.t.Fatalf("initialize: %v", r.Error)
	}
}

func (h *harness) newSession() string {
	h.t.Helper()
	r := h.call("session/new", map[string]any{"cwd": "/p", "mcpServers": []any{}})
	if r.Error != nil {
		h.t.Fatalf("session/new: %v", r.Error)
	}
	var res struct{ SessionID string }
	json.Unmarshal(r.Result, &res)
	return res.SessionID
}

func text(s string) map[string]any { return map[string]any{"type": "text", "text": s} }

func TestInitialize(t *testing.T) {
	h := newHarness(t, &fakeBackend{})
	r := h.call("initialize", map[string]any{"protocolVersion": 1})
	if r.Error != nil {
		t.Fatal(r.Error)
	}
	want := `{"protocolVersion":1,"agentCapabilities":{"loadSession":true,"promptCapabilities":{"image":false,"audio":false,"embeddedContext":true},"mcpCapabilities":{"http":true,"sse":false}},"authMethods":[],"agentInfo":{"name":"tenon","version":"9.9"}}`
	var a, b any
	json.Unmarshal(r.Result, &a)
	json.Unmarshal([]byte(want), &b)
	ga, _ := json.Marshal(a)
	gb, _ := json.Marshal(b)
	if string(ga) != string(gb) {
		t.Fatalf("got %s\nwant %s", r.Result, want)
	}
	h2 := newHarness(t, &fakeBackend{})
	r = h2.call("initialize", map[string]any{"protocolVersion": 2})
	if r.Error == nil || r.Error.Code != CodeInvalidParams {
		t.Fatalf("want -32602, got %+v", r.Error)
	}
}

func TestNewSessionRelativeCwd(t *testing.T) {
	h := newHarness(t, &fakeBackend{})
	h.init()
	r := h.call("session/new", map[string]any{"cwd": "rel/dir", "mcpServers": []any{}})
	if r.Error == nil || r.Error.Code != CodeInvalidParams {
		t.Fatalf("got %+v", r.Error)
	}
}

func TestNewSessionBackendError(t *testing.T) {
	h := newHarness(t, &fakeBackend{err: errors.New("profile strict-local: refused")})
	h.init()
	r := h.call("session/new", map[string]any{"cwd": "/p", "mcpServers": []any{}})
	if r.Error == nil || r.Error.Code != CodeTenon || r.Error.Message != "profile strict-local: refused" {
		t.Fatalf("got %+v", r.Error)
	}
}

func TestNewSessionPassesClientInfo(t *testing.T) {
	b := &fakeBackend{}
	h := newHarness(t, b)
	h.init()
	h.newSession()
	if b.client == nil || b.client.Name != "zed" {
		t.Fatalf("client = %+v", b.client)
	}
}

func TestPromptText(t *testing.T) {
	b := &fakeBackend{}
	h := newHarness(t, b)
	h.init()
	id := h.newSession()
	r := h.call("session/prompt", map[string]any{"sessionId": id, "prompt": []any{
		text("a"), text("b"),
		map[string]any{"type": "resource_link", "uri": "file:///p/a.go", "name": "a.go"},
		map[string]any{"type": "resource_link", "uri": "https://x.io/y", "name": "y"},
		map[string]any{"type": "resource", "resource": map[string]any{"uri": "file:///p/b.go", "text": "pkg b"}},
	}})
	if r.Error != nil {
		t.Fatal(r.Error)
	}
	if string(r.Result) != `{"stopReason":"end_turn"}` {
		t.Fatalf("result %s", r.Result)
	}
	got := b.sessions[0].inputs[0].Text
	want := "a\n\nb\n\n@/p/a.go\n\n@https://x.io/y\n\n<resource uri=\"file:///p/b.go\">\npkg b\n</resource>"
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestPromptImageRefused(t *testing.T) {
	h := newHarness(t, &fakeBackend{})
	h.init()
	id := h.newSession()
	r := h.call("session/prompt", map[string]any{"sessionId": id, "prompt": []any{map[string]any{"type": "image", "data": "x", "mimeType": "image/png"}}})
	if r.Error == nil || r.Error.Code != CodeInvalidParams {
		t.Fatalf("got %+v", r.Error)
	}
}

func TestPromptSkill(t *testing.T) {
	b := &fakeBackend{newFn: func() *fakeSession {
		s := newFakeSession("s1")
		s.cmds = []Command{{Name: "fix", Description: "d"}}
		return s
	}}
	h := newHarness(t, b)
	h.init()
	id := h.newSession()
	h.call("session/prompt", map[string]any{"sessionId": id, "prompt": []any{text("/fix the test")}})
	want := engine.Input{Skill: "fix", Text: "the test", Typed: "/fix the test"}
	if got := b.sessions[0].inputs[0]; got != want {
		t.Fatalf("got %+v want %+v", got, want)
	}
	h.call("session/prompt", map[string]any{"sessionId": id, "prompt": []any{text("/nope x")}})
	if got := b.sessions[0].inputs[1]; got.Skill != "" || got.Text != "/nope x" {
		t.Fatalf("unknown command: %+v", got)
	}
}

func TestUnknownSession(t *testing.T) {
	h := newHarness(t, &fakeBackend{})
	h.init()
	r := h.call("session/prompt", map[string]any{"sessionId": "zzz", "prompt": []any{text("x")}})
	if r.Error == nil || r.Error.Code != CodeUnknownSession {
		t.Fatalf("got %+v", r.Error)
	}
}

func TestTurnInProgress(t *testing.T) {
	b := &fakeBackend{newFn: func() *fakeSession {
		s := newFakeSession("s1")
		s.release = make(chan struct{})
		return s
	}}
	h := newHarness(t, b)
	h.init()
	id := h.newSession()
	first := h.send("session/prompt", map[string]any{"sessionId": id, "prompt": []any{text("one")}})
	<-b.sessions[0].entered
	r := h.call("session/prompt", map[string]any{"sessionId": id, "prompt": []any{text("two")}})
	if r.Error == nil || r.Error.Code != CodeTenon || r.Error.Message != "turn in progress" {
		t.Fatalf("got %+v", r.Error)
	}
	close(b.sessions[0].release)
	if r := h.read(); r.ID != first || r.Error != nil {
		t.Fatalf("first: %+v", r)
	}
	// le créneau est libéré
	b.sessions[0].release = nil
	if r := h.call("session/prompt", map[string]any{"sessionId": id, "prompt": []any{text("three")}}); r.Error != nil {
		t.Fatalf("third: %+v", r.Error)
	}
}

func TestCancelDuringPrompt(t *testing.T) {
	b := &fakeBackend{newFn: func() *fakeSession {
		s := newFakeSession("s1")
		s.release = make(chan struct{})
		return s
	}}
	h := newHarness(t, b)
	h.init()
	id := h.newSession()
	p := h.send("session/prompt", map[string]any{"sessionId": id, "prompt": []any{text("one")}})
	<-b.sessions[0].entered
	h.notify("session/cancel", map[string]any{"sessionId": id})
	r := h.read()
	if r.ID != p || r.Error != nil || string(r.Result) != `{"stopReason":"cancelled"}` {
		t.Fatalf("got %+v %s", r.Error, r.Result)
	}
	// cancel sans tour ou session inconnue : ignoré
	h.notify("session/cancel", map[string]any{"sessionId": id})
	h.notify("session/cancel", map[string]any{"sessionId": "zzz"})
}

func TestLoadSession(t *testing.T) {
	b := &fakeBackend{}
	h := newHarness(t, b)
	h.init()
	r := h.call("session/load", map[string]any{"sessionId": "old", "cwd": "/p", "mcpServers": []any{}})
	if r.Error != nil || string(r.Result) != "null" {
		t.Fatalf("got %+v %s", r.Error, r.Result)
	}
	if r := h.call("session/prompt", map[string]any{"sessionId": "old", "prompt": []any{text("x")}}); r.Error != nil {
		t.Fatal(r.Error)
	}
}

func TestUnknownMethod(t *testing.T) {
	h := newHarness(t, &fakeBackend{})
	r := h.call("nope", nil)
	if r.Error == nil || r.Error.Code != CodeMethodNotFound {
		t.Fatalf("got %+v", r.Error)
	}
}

func TestServeClosesSessions(t *testing.T) {
	b := &fakeBackend{}
	n := 0
	b.newFn = func() *fakeSession { n++; return newFakeSession(string(rune('a' + n))) }
	h := newHarness(t, b)
	h.init()
	h.newSession()
	h.newSession()
	h.in.Close()
	select {
	case err := <-h.done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return")
	}
	if len(b.sessions) != 2 {
		t.Fatalf("sessions %d", len(b.sessions))
	}
	for i, s := range b.sessions {
		if s.closed != 1 {
			t.Fatalf("session %d closed %d times", i, s.closed)
		}
	}
}
