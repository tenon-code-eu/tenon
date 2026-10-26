package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type handlerFunc func(ctx context.Context, method string, params json.RawMessage) (any, error)

func (f handlerFunc) Handle(ctx context.Context, m string, p json.RawMessage) (any, error) {
	return f(ctx, m, p)
}

// rig: client side of two pipes around a Conn.
type rig struct {
	toAgent   *io.PipeWriter // client writes
	fromAgent *bufio.Reader  // client reads
	done      chan error
	conn      *Conn
	cancel    context.CancelFunc
}

func newRig(t *testing.T, h Handler) *rig {
	t.Helper()
	ar, cw := io.Pipe()
	cr, aw := io.Pipe()
	c := NewConn(ar, aw, h)
	ctx, cancel := context.WithCancel(context.Background())
	r := &rig{toAgent: cw, fromAgent: bufio.NewReader(cr), done: make(chan error, 1), conn: c, cancel: cancel}
	go func() { r.done <- c.Serve(ctx) }()
	t.Cleanup(func() { cancel(); cw.Close(); cr.Close() })
	return r
}

func (r *rig) send(t *testing.T, line string) {
	t.Helper()
	if _, err := io.WriteString(r.toAgent, line+"\n"); err != nil {
		t.Fatal(err)
	}
}

func (r *rig) readLine(t *testing.T) string {
	t.Helper()
	ch := make(chan string, 1)
	go func() { s, _ := r.fromAgent.ReadString('\n'); ch <- s }()
	select {
	case s := <-ch:
		if !strings.HasSuffix(s, "\n") {
			t.Fatalf("line not terminated: %q", s)
		}
		return strings.TrimSuffix(s, "\n")
	case <-time.After(3 * time.Second):
		t.Fatal("timeout reading line")
		return ""
	}
}

func echo() Handler {
	return handlerFunc(func(ctx context.Context, m string, p json.RawMessage) (any, error) {
		return map[string]any{"ok": true}, nil
	})
}

func TestRequestResponse(t *testing.T) {
	r := newRig(t, echo())
	r.send(t, `{"jsonrpc":"2.0","id":1,"method":"m","params":{}}`)
	if got := r.readLine(t); got != `{"jsonrpc":"2.0","id":1,"result":{"ok":true}}` {
		t.Fatalf("got %s", got)
	}
	r.send(t, `{"jsonrpc":"2.0","id":"abc","method":"m"}`)
	if got := r.readLine(t); got != `{"jsonrpc":"2.0","id":"abc","result":{"ok":true}}` {
		t.Fatalf("got %s", got)
	}
}

func TestErrorResponse(t *testing.T) {
	r := newRig(t, handlerFunc(func(ctx context.Context, m string, p json.RawMessage) (any, error) {
		if m == "a" {
			return nil, &Error{Code: CodeInvalidParams, Message: "bad", Data: "d"}
		}
		return nil, errors.New("boom")
	}))
	r.send(t, `{"jsonrpc":"2.0","id":1,"method":"a"}`)
	var resp struct {
		ID    int    `json:"id"`
		Error *Error `json:"error"`
	}
	if err := json.Unmarshal([]byte(r.readLine(t)), &resp); err != nil || resp.Error == nil || resp.Error.Code != -32602 || resp.Error.Message != "bad" {
		t.Fatalf("%+v %v", resp, err)
	}
	r.send(t, `{"jsonrpc":"2.0","id":2,"method":"b"}`)
	resp.Error = nil
	if err := json.Unmarshal([]byte(r.readLine(t)), &resp); err != nil || resp.Error == nil || resp.Error.Code != -32603 || resp.Error.Message != "boom" {
		t.Fatalf("%+v %v", resp, err)
	}
}

func TestNotificationNoReply(t *testing.T) {
	var n atomic.Int32
	called := make(chan struct{}, 1)
	r := newRig(t, handlerFunc(func(ctx context.Context, m string, p json.RawMessage) (any, error) {
		n.Add(1)
		called <- struct{}{}
		return "ignored", nil
	}))
	r.send(t, `{"jsonrpc":"2.0","method":"note","params":{}}`)
	<-called
	// A following request proves nothing was written before its response.
	r.send(t, `{"jsonrpc":"2.0","id":7,"method":"m"}`)
	<-called
	if got := r.readLine(t); !strings.Contains(got, `"id":7`) {
		t.Fatalf("unexpected line before response: %s", got)
	}
}

func TestParseError(t *testing.T) {
	r := newRig(t, echo())
	r.send(t, `{not json`)
	if got := r.readLine(t); !strings.HasPrefix(got, `{"jsonrpc":"2.0","id":null,"error":{"code":-32700,`) {
		t.Fatalf("got %s", got)
	}
	r.send(t, `{"jsonrpc":"2.0","id":1,"method":"m"}`)
	if got := r.readLine(t); !strings.Contains(got, `"result"`) {
		t.Fatalf("not serving after parse error: %s", got)
	}
}

func TestCallFromAgent(t *testing.T) {
	r := newRig(t, echo())
	errc := make(chan error, 1)
	var res struct{ V int }
	go func() { errc <- r.conn.Call(context.Background(), "fs/read", map[string]string{"a": "b"}, &res) }()
	var req struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	if err := json.Unmarshal([]byte(r.readLine(t)), &req); err != nil || req.Method != "fs/read" || len(req.ID) == 0 {
		t.Fatalf("%+v %v", req, err)
	}
	r.send(t, `{"jsonrpc":"2.0","id":99999,"result":{"V":1}}`) // unknown id, ignored
	r.send(t, `{"jsonrpc":"2.0","id":`+string(req.ID)+`,"result":{"V":42}}`)
	if err := <-errc; err != nil || res.V != 42 {
		t.Fatalf("%v %+v", err, res)
	}
}

func TestCallErrorResponse(t *testing.T) {
	r := newRig(t, echo())
	errc := make(chan error, 1)
	go func() { errc <- r.conn.Call(context.Background(), "x", nil, nil) }()
	var req struct {
		ID json.RawMessage `json:"id"`
	}
	json.Unmarshal([]byte(r.readLine(t)), &req)
	r.send(t, `{"jsonrpc":"2.0","id":`+string(req.ID)+`,"error":{"code":-32000,"message":"nope"}}`)
	var e *Error
	if err := <-errc; !errors.As(err, &e) || e.Code != -32000 {
		t.Fatalf("got %v", err)
	}
}

func TestCallCancelled(t *testing.T) {
	r := newRig(t, echo())
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- r.conn.Call(ctx, "x", nil, nil) }()
	var req struct {
		ID json.RawMessage `json:"id"`
	}
	json.Unmarshal([]byte(r.readLine(t)), &req)
	cancel()
	if err := <-errc; !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	r.send(t, `{"jsonrpc":"2.0","id":`+string(req.ID)+`,"result":{}}`) // late, dropped
	r.send(t, `{"jsonrpc":"2.0","id":1,"method":"m"}`)
	if got := r.readLine(t); !strings.Contains(got, `"id":1`) {
		t.Fatalf("got %s", got)
	}
}

func TestOneJSONPerLine(t *testing.T) {
	s := "a\nb c\xffd\r\n"
	r := newRig(t, handlerFunc(func(ctx context.Context, m string, p json.RawMessage) (any, error) { return s, nil }))
	r.send(t, `{"jsonrpc":"2.0","id":1,"method":"m"}`)
	line := r.readLine(t)
	if strings.ContainsAny(line, "\n\r") || !json.Valid([]byte(line)) {
		t.Fatalf("bad line %q", line)
	}
	go r.conn.Notify("n", map[string]string{"t": s})
	line = r.readLine(t)
	if strings.ContainsAny(line, "\n\r") || !json.Valid([]byte(line)) {
		t.Fatalf("bad line %q", line)
	}
}

func TestEOF(t *testing.T) {
	r := newRig(t, echo())
	r.toAgent.Close()
	select {
	case err := <-r.done:
		if err != nil {
			t.Fatalf("got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not return")
	}
}

func TestPendingCallFailsAfterServe(t *testing.T) {
	r := newRig(t, echo())
	errc := make(chan error, 1)
	go func() { errc <- r.conn.Call(context.Background(), "x", nil, nil) }()
	r.readLine(t)
	r.toAgent.Close()
	select {
	case err := <-errc:
		if err == nil {
			t.Fatal("want error")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Call hung")
	}
	if err := r.conn.Call(context.Background(), "y", nil, nil); err == nil {
		t.Fatal("Call after Serve must fail")
	}
}

func TestAfterReply(t *testing.T) {
	var r *rig
	r = newRig(t, handlerFunc(func(ctx context.Context, m string, p json.RawMessage) (any, error) {
		return AfterReply{Result: map[string]any{"sessionId": "s"}, Then: func() {
			r.conn.Notify("session/update", map[string]any{"n": 1})
		}}, nil
	}))
	r.send(t, `{"jsonrpc":"2.0","id":1,"method":"session/new","params":{}}`)
	if got := r.readLine(t); got != `{"jsonrpc":"2.0","id":1,"result":{"sessionId":"s"}}` {
		t.Fatalf("first line %s", got)
	}
	if got := r.readLine(t); got != `{"jsonrpc":"2.0","method":"session/update","params":{"n":1}}` {
		t.Fatalf("second line %s", got)
	}
}
