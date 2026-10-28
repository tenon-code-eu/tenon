package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
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

	root    string
	notices []string
	old     map[string]string
	// submit, si non nil, remplace le corps de Submit (événements scriptés).
	submit  func(ctx context.Context, f *fakeSession) error
	replies chan permReply
	seq     uint64
	// onClose, si non nil, s'exécute dans Close (tour annulé qui émet).
	onClose func(f *fakeSession)
	// breakPayload, si non vide, rend illisible toute charge utile qui le
	// contient (masquage qui casse le JSON).
	breakPayload string
	// noticesGate, si non nil, bloque Notices (session qui démarre) jusqu'à
	// sa fermeture.
	noticesGate chan struct{}
	// meta est renvoyé par Meta (_meta.tenon).
	meta map[string]any
}

type permReply struct {
	id string
	r  engine.Reply
}

// emit pousse un événement scripté sur le canal de la session.
func (f *fakeSession) emit(t event.Type, payload any) {
	f.mu.Lock()
	f.seq++
	seq := f.seq
	f.mu.Unlock()
	ev, err := event.New(f.id, seq, time.Now(), t, payload)
	if err != nil {
		panic(err)
	}
	f.ch <- ev
}

func newFakeSession(id string) *fakeSession {
	return &fakeSession{id: id, root: "/p", ch: make(chan event.Event, 64), entered: make(chan struct{}, 4), replies: make(chan permReply, 8)}
}
func (f *fakeSession) ID() string                    { return f.id }
func (f *fakeSession) ProjectRoot() string           { return f.root }
func (f *fakeSession) Subscribe() <-chan event.Event { return f.ch }
func (f *fakeSession) Submit(ctx context.Context, in engine.Input) error {
	f.mu.Lock()
	f.inputs = append(f.inputs, in)
	f.mu.Unlock()
	f.entered <- struct{}{}
	if f.submit != nil {
		return f.submit(ctx, f)
	}
	if f.release != nil {
		select {
		case <-f.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
func (f *fakeSession) Respond(id string, r engine.Reply) error {
	f.replies <- permReply{id, r}
	return nil
}
func (f *fakeSession) Notices() []string {
	if f.noticesGate != nil {
		<-f.noticesGate
	}
	return f.notices
}
func (f *fakeSession) Meta() map[string]any   { return f.meta }
func (f *fakeSession) Commands() []Command    { return f.cmds }
func (f *fakeSession) Redact(s string) string { return strings.ReplaceAll(s, "SECRET", "[MASKED]") }
func (f *fakeSession) RedactPayload(s string) string {
	if f.breakPayload != "" && strings.Contains(s, f.breakPayload) {
		return s[:len(s)/2]
	}
	return f.Redact(s)
}
func (f *fakeSession) OldText(h string) (string, bool) {
	s, ok := f.old[h]
	return s, ok
}
func (f *fakeSession) Close() {
	if f.onClose != nil {
		f.onClose(f)
	}
	f.mu.Lock()
	f.closed++
	f.mu.Unlock()
}

type fakeBackend struct {
	mu       sync.Mutex
	sessions []*fakeSession
	newFn    func() *fakeSession
	loadFn   func(id string) (*fakeSession, []event.Event)
	err      error
	client   *Implementation

	list    []SessionInfo
	listErr error
	listCwd string
}

func (b *fakeBackend) ListSessions(ctx context.Context, cwd string) ([]SessionInfo, error) {
	b.mu.Lock()
	b.listCwd = cwd
	b.mu.Unlock()
	return b.list, b.listErr
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
	var s *fakeSession
	var replay []event.Event
	if b.loadFn != nil {
		s, replay = b.loadFn(id)
	} else {
		s = newFakeSession(id)
	}
	b.mu.Lock()
	b.sessions = append(b.sessions, s)
	b.mu.Unlock()
	return s, replay, nil
}

// harness pilote un Server via des pipes. Les lignes sortantes sont lues
// par une goroutine ; read rend la prochaine réponse et met de côté les
// notifications et requêtes de l'agent, que next rend par méthode.
type harness struct {
	t      *testing.T
	in     io.WriteCloser
	lines  chan msg
	done   chan error
	cancel context.CancelFunc
	nextID int

	mu     sync.Mutex
	held   []msg // messages avec méthode non encore consommés
	all    []msg // toutes les lignes, dans l'ordre
	server *Server
}

// msg est une ligne sortante quelconque.
type msg struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *Error          `json:"error"`
}

func newHarness(t *testing.T, b Backend) *harness {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	h := &harness{t: t, in: inW, lines: make(chan msg, 256), done: make(chan error, 1), cancel: cancel}
	h.server = NewServer(b, "9.9", i18n.EN)
	go func() {
		err := h.server.Serve(ctx, inR, outW)
		outW.Close()
		h.done <- err
	}()
	go func() {
		sc := bufio.NewScanner(outR)
		sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
		for sc.Scan() {
			var m msg
			if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
				t.Errorf("bad line %q: %v", sc.Bytes(), err)
				continue
			}
			h.mu.Lock()
			h.all = append(h.all, m)
			h.mu.Unlock()
			h.lines <- m
		}
		close(h.lines)
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

// respond répond à une requête de l'agent.
func (h *harness) respond(id json.RawMessage, result any, rerr *Error) {
	m := map[string]any{"jsonrpc": "2.0", "id": id}
	if rerr != nil {
		m["error"] = rerr
	} else {
		m["result"] = result
	}
	b, _ := json.Marshal(m)
	h.in.Write(append(b, '\n'))
}

type reply struct {
	ID     int
	Result json.RawMessage
	Error  *Error
}

func (h *harness) recv() msg {
	h.t.Helper()
	select {
	case m, ok := <-h.lines:
		if !ok {
			return msg{ID: json.RawMessage("-1")}
		}
		return m
	case <-time.After(5 * time.Second):
		h.t.Fatal("timeout waiting for a line")
		return msg{}
	}
}

// read rend la prochaine réponse ; les messages avec méthode sont gardés.
func (h *harness) read() reply {
	h.t.Helper()
	for {
		m := h.recv()
		if m.Method != "" {
			h.mu.Lock()
			h.held = append(h.held, m)
			h.mu.Unlock()
			continue
		}
		var id int
		json.Unmarshal(m.ID, &id)
		return reply{ID: id, Result: m.Result, Error: m.Error}
	}
}

// next rend le prochain message de méthode method (gardé ou à venir) ; les
// réponses lues entre-temps sont perdues (à éviter).
func (h *harness) next(method string) msg {
	h.t.Helper()
	h.mu.Lock()
	for i, m := range h.held {
		if m.Method == method {
			h.held = append(h.held[:i:i], h.held[i+1:]...)
			h.mu.Unlock()
			return m
		}
	}
	h.mu.Unlock()
	for {
		m := h.recv()
		if m.Method == method {
			return m
		}
		if m.Method != "" {
			h.mu.Lock()
			h.held = append(h.held, m)
			h.mu.Unlock()
			continue
		}
		h.t.Fatalf("response %s while waiting for %s", m.ID, method)
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
	want := `{"protocolVersion":1,"agentCapabilities":{"loadSession":true,"promptCapabilities":{"image":false,"audio":false,"embeddedContext":true},"mcpCapabilities":{"http":true,"sse":false},"sessionCapabilities":{"list":{}}},"authMethods":[],"agentInfo":{"name":"tenon","version":"9.9"}}`
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
	if r.Error == nil || r.Error.Code != CodeInternal || r.Error.Message != "profile strict-local: refused" {
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
	// coupure sur tout blanc
	h.call("session/prompt", map[string]any{"sessionId": id, "prompt": []any{text("/fix\tthe\ntest")}})
	if got := b.sessions[0].inputs[2]; got.Skill != "fix" || got.Text != "the\ntest" {
		t.Fatalf("whitespace split: %+v", got)
	}
	h.call("session/prompt", map[string]any{"sessionId": id, "prompt": []any{text("/fix")}})
	if got := b.sessions[0].inputs[3]; got.Skill != "fix" || got.Text != "" {
		t.Fatalf("bare command: %+v", got)
	}
}

func TestResourceURIEscaped(t *testing.T) {
	b := &fakeBackend{}
	h := newHarness(t, b)
	h.init()
	id := h.newSession()
	h.call("session/prompt", map[string]any{"sessionId": id, "prompt": []any{
		map[string]any{"type": "resource", "resource": map[string]any{"uri": `file:///p/a"><x>&.go`, "text": "t"}},
	}})
	want := "<resource uri=\"file:///p/a&#34;&gt;&lt;x&gt;&amp;.go\">\nt\n</resource>"
	if got := b.sessions[0].inputs[0].Text; got != want {
		t.Fatalf("got %q\nwant %q", got, want)
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
	if r.Error == nil || r.Error.Code != CodeInternal || r.Error.Message != "turn in progress" {
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

// waitEntry attend que cond soit vraie pour l'entrée id (sous son verrou).
func (h *harness) waitEntry(id string, cond func(e *entry) bool) {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		h.server.mu.Lock()
		e := h.server.sessions[id]
		h.server.mu.Unlock()
		if e != nil {
			e.mu.Lock()
			ok := cond(e)
			e.mu.Unlock()
			if ok {
				return
			}
		}
		if time.Now().After(deadline) {
			h.t.Fatal("entry condition not reached")
		}
		time.Sleep(time.Millisecond)
	}
}

// TestCancelBeforeTurnStarts : un session/cancel reçu pour un prompt dont le
// tour n'a pas commencé (session qui démarre) annule ce prompt ; le suivant
// tourne normalement.
func TestCancelBeforeTurnStarts(t *testing.T) {
	gate := make(chan struct{})
	b := &fakeBackend{newFn: func() *fakeSession {
		s := newFakeSession("s1")
		s.noticesGate = gate
		return s
	}}
	h := newHarness(t, b)
	h.init()
	id := h.newSession()
	pid := h.send("session/prompt", map[string]any{"sessionId": id, "prompt": []any{text("x")}})
	h.waitEntry(id, func(e *entry) bool { return e.pending == 1 })
	h.notify("session/cancel", map[string]any{"sessionId": id})
	h.waitEntry(id, func(e *entry) bool { return e.cancelReq })
	close(gate)
	if r := h.read(); r.ID != pid || string(r.Result) != `{"stopReason":"cancelled"}` {
		t.Fatalf("prompt: %+v %s", r.Error, r.Result)
	}
	if r := h.call("session/prompt", map[string]any{"sessionId": id, "prompt": []any{text("y")}}); string(r.Result) != `{"stopReason":"end_turn"}` {
		t.Fatalf("next prompt: %+v %s", r.Error, r.Result)
	}
	s := b.sessions[0]
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.inputs) != 1 || s.inputs[0].Text != "y" {
		t.Fatalf("inputs %+v", s.inputs)
	}
}

// Un session/cancel sans prompt en attente ni tour en cours n'annule pas le
// prompt suivant.
func TestStaleCancelIgnored(t *testing.T) {
	h := newHarness(t, &fakeBackend{})
	h.init()
	id := h.newSession()
	h.notify("session/cancel", map[string]any{"sessionId": id})
	if r := h.call("session/prompt", map[string]any{"sessionId": id, "prompt": []any{text("y")}}); string(r.Result) != `{"stopReason":"end_turn"}` {
		t.Fatalf("prompt: %+v %s", r.Error, r.Result)
	}
}

func TestLoadSession(t *testing.T) {
	b := &fakeBackend{}
	h := newHarness(t, b)
	h.init()
	r := h.call("session/load", map[string]any{"sessionId": "old", "cwd": "/p", "mcpServers": []any{}})
	if r.Error != nil || string(r.Result) != "{}" {
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

func TestInitializeSessionCapabilities(t *testing.T) {
	h := newHarness(t, &fakeBackend{})
	r := h.call("initialize", map[string]any{"protocolVersion": 1})
	var got struct {
		AgentCapabilities struct {
			SessionCapabilities json.RawMessage `json:"sessionCapabilities"`
		} `json:"agentCapabilities"`
	}
	if err := json.Unmarshal(r.Result, &got); err != nil || string(got.AgentCapabilities.SessionCapabilities) != `{"list":{}}` {
		t.Fatalf("got %s (%v)", r.Result, err)
	}
}

func TestListSessions(t *testing.T) {
	b := &fakeBackend{list: []SessionInfo{
		{SessionID: "s2", Cwd: "/p", Title: "b", UpdatedAt: time.Date(2026, 10, 28, 12, 0, 0, 0, time.FixedZone("x", 7200))},
		{SessionID: "s1", Cwd: "/p", Title: "a", UpdatedAt: time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)},
	}}
	h := newHarness(t, b)
	h.init()
	r := h.call("session/list", map[string]any{"cwd": "/p"})
	if r.Error != nil {
		t.Fatal(r.Error)
	}
	jsonEq(t, r.Result, `{"sessions":[{"sessionId":"s2","cwd":"/p","title":"b","updatedAt":"2026-10-28T10:00:00Z"},{"sessionId":"s1","cwd":"/p","title":"a","updatedAt":"2026-10-28T09:00:00Z"}]}`)
	if b.listCwd != "/p" {
		t.Fatalf("cwd %q", b.listCwd)
	}
	// Aucune session : tableau vide, jamais null.
	h2 := newHarness(t, &fakeBackend{})
	h2.init()
	r = h2.call("session/list", map[string]any{"cwd": "/p"})
	jsonEq(t, r.Result, `{"sessions":[]}`)
	// cwd relatif ou absent : -32602.
	for _, p := range []map[string]any{{"cwd": "rel"}, {}} {
		if r := h.call("session/list", p); r.Error == nil || r.Error.Code != CodeInvalidParams {
			t.Fatalf("%v: got %+v", p, r.Error)
		}
	}
	// Erreur du backend : -32603.
	h3 := newHarness(t, &fakeBackend{listErr: errors.New("boom")})
	h3.init()
	if r := h3.call("session/list", map[string]any{"cwd": "/p"}); r.Error == nil || r.Error.Code != CodeInternal {
		t.Fatalf("got %+v", r.Error)
	}
}

func TestNewSessionMeta(t *testing.T) {
	meta := map[string]any{"provider": "mistral", "sandbox": map[string]any{"mode": "auto"}}
	b := &fakeBackend{newFn: func() *fakeSession { s := newFakeSession("s1"); s.meta = meta; return s }}
	b.loadFn = func(id string) (*fakeSession, []event.Event) { s := newFakeSession(id); s.meta = meta; return s, nil }
	h := newHarness(t, b)
	h.init()
	r := h.call("session/new", map[string]any{"cwd": "/p", "mcpServers": []any{}})
	jsonEq(t, r.Result, `{"sessionId":"s1","_meta":{"tenon":{"provider":"mistral","sandbox":{"mode":"auto"}}}}`)
	r = h.call("session/load", map[string]any{"sessionId": "old", "cwd": "/p", "mcpServers": []any{}})
	jsonEq(t, r.Result, `{"_meta":{"tenon":{"provider":"mistral","sandbox":{"mode":"auto"}}}}`)
}
