package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tenon-code-eu/tenon/internal/llm"
)

func sse(w http.ResponseWriter, lines ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, l := range lines {
		fmt.Fprintf(w, "data: %s\n\n", l)
	}
}

func newTest(t *testing.T, h http.HandlerFunc) (*OpenAICompat, *[]time.Duration) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	var sleeps []time.Duration
	p := NewOpenAICompat(llm.ProviderInfo{ID: "t", BaseURL: srv.URL, Model: "m"}, "k",
		WithSleep(func(ctx context.Context, d time.Duration) error {
			sleeps = append(sleeps, d)
			return ctx.Err()
		}))
	return p, &sleeps
}

func collect(t *testing.T, ch <-chan llm.Chunk) (string, llm.Chunk) {
	t.Helper()
	var text string
	var last llm.Chunk
	n := 0
	for c := range ch {
		text += c.Text
		last = c
		n++
		if c.Done && n > 0 {
			// Done must be the final chunk; channel must close next.
			if _, ok := <-ch; ok {
				t.Fatal("chunk after Done")
			}
			break
		}
	}
	return text, last
}

var req = llm.Request{Model: "m", Messages: []llm.Message{{Role: llm.User, Content: "hi"}}}

func TestStreamText(t *testing.T) {
	var auth string
	var body map[string]any
	p, _ := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &body)
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path %s", r.URL.Path)
		}
		sse(w, `{"choices":[{"delta":{"content":"Bon"}}]}`,
			`{"choices":[{"delta":{"content":"jour"}}]}`, `[DONE]`)
	})
	ch, err := p.Stream(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	text, last := collect(t, ch)
	if text != "Bonjour" || !last.Done || last.Err != nil {
		t.Fatalf("text=%q last=%+v", text, last)
	}
	if auth != "Bearer k" || body["stream"] != true {
		t.Fatalf("auth=%q body=%v", auth, body)
	}
}

func TestStreamToolCallAccumulated(t *testing.T) {
	p, _ := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		sse(w,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"read_file","arguments":"{\"pa"}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"th\":\"a"}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":".go\"}"}}]},"finish_reason":"tool_calls"}]}`,
			`[DONE]`)
	})
	ch, _ := p.Stream(context.Background(), req)
	_, last := collect(t, ch)
	want := []llm.ToolCall{{ID: "call_1", Name: "read_file", Args: json.RawMessage(`{"path":"a.go"}`)}}
	if last.Err != nil || !reflect.DeepEqual(last.ToolCalls, want) {
		t.Fatalf("%+v", last)
	}
}

func TestRetryOn429ThenOK(t *testing.T) {
	var n int32
	p, sleeps := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) == 1 {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(429)
			return
		}
		sse(w, `{"choices":[{"delta":{"content":"ok"}}]}`, `[DONE]`)
	})
	ch, err := p.Stream(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	text, _ := collect(t, ch)
	if text != "ok" || !reflect.DeepEqual(*sleeps, []time.Duration{2 * time.Second}) {
		t.Fatalf("text=%q sleeps=%v", text, *sleeps)
	}
}

func TestRetryExhaustedReturnsUnavailable(t *testing.T) {
	var n int32
	p, sleeps := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		w.WriteHeader(500)
	})
	_, err := p.Stream(context.Background(), req)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err=%v", err)
	}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}
	if !reflect.DeepEqual(*sleeps, want) || n != 4 {
		t.Fatalf("sleeps=%v n=%d", *sleeps, n)
	}
}

func TestAuthErrorNoRetry(t *testing.T) {
	var n int32
	p, sleeps := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		w.WriteHeader(401)
	})
	_, err := p.Stream(context.Background(), req)
	if !errors.Is(err, ErrAuth) || n != 1 || len(*sleeps) != 0 {
		t.Fatalf("err=%v n=%d", err, n)
	}
}

func TestContextLength(t *testing.T) {
	p, _ := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		io.WriteString(w, `{"error":{"code":"context_length_exceeded"}}`)
	})
	_, err := p.Stream(context.Background(), req)
	if !errors.Is(err, ErrContextLength) {
		t.Fatalf("err=%v", err)
	}
}

func TestStreamCutIsIncomplete(t *testing.T) {
	p, _ := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		sse(w, `{"choices":[{"delta":{"content":"Bon"}}]}`)
	})
	ch, _ := p.Stream(context.Background(), req)
	text, last := collect(t, ch)
	if text != "Bon" || !errors.Is(last.Err, ErrIncomplete) || !last.Done {
		t.Fatalf("text=%q last=%+v", text, last)
	}
}

func TestRequestMapping(t *testing.T) {
	var body struct {
		Messages []map[string]any `json:"messages"`
		Tools    []map[string]any `json:"tools"`
	}
	p, _ := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &body)
		sse(w, `[DONE]`)
	})
	r := llm.Request{Model: "m",
		Messages: []llm.Message{
			{Role: llm.Assistant, ToolCalls: []llm.ToolCall{{ID: "c1", Name: "f"}}},
			{Role: llm.Tool, Content: "res", ToolCallID: "c1"},
		},
		Tools: []llm.ToolDef{{Name: "f", Description: "d", Schema: json.RawMessage(`{"type":"object"}`)}},
	}
	ch, err := p.Stream(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	collect(t, ch)
	tc := body.Messages[0]["tool_calls"].([]any)[0].(map[string]any)
	fn := tc["function"].(map[string]any)
	if tc["type"] != "function" || fn["arguments"] != "{}" || fn["name"] != "f" {
		t.Fatalf("%v", tc)
	}
	if body.Messages[1]["tool_call_id"] != "c1" || body.Tools[0]["type"] != "function" {
		t.Fatalf("%v", body)
	}
}

func TestCancelClosesChannel(t *testing.T) {
	release := make(chan struct{})
	p, _ := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		sse(w, `{"choices":[{"delta":{"content":"a"}}]}`)
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	defer close(release)
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := p.Stream(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	<-ch
	cancel()
	var last llm.Chunk
	for c := range ch {
		last = c
	}
	if !errors.Is(last.Err, context.Canceled) || !strings.Contains(fmt.Sprint(last.Done), "true") {
		t.Fatalf("%+v", last)
	}
}

func TestSlowConsumerStillGetsFinalChunk(t *testing.T) {
	p, _ := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i < 8; i++ {
			sse(w, `{"choices":[{"delta":{"content":"x"}}]}`)
		}
		sse(w,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"f","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`,
			`[DONE]`)
	})
	ch, err := p.Stream(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	text, last := collect(t, ch)
	if len(text) != 8 || !last.Done || last.Err != nil || len(last.ToolCalls) != 1 || last.ToolCalls[0].ID != "call_1" {
		t.Fatalf("text=%d last=%+v", len(text), last)
	}
}

func TestRetryAfterCapped(t *testing.T) {
	var n int32
	p, sleeps := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) == 1 {
			w.Header().Set("Retry-After", "86400")
			w.WriteHeader(429)
			return
		}
		sse(w, `{"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`, "[DONE]")
	})
	ch, err := p.Stream(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	collect(t, ch)
	if !reflect.DeepEqual(*sleeps, []time.Duration{60 * time.Second}) {
		t.Fatalf("sleeps=%v", *sleeps)
	}
}

func TestForbiddenIsAuth(t *testing.T) {
	var n int32
	p, sleeps := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		w.WriteHeader(403)
	})
	_, err := p.Stream(context.Background(), req)
	if !errors.Is(err, ErrAuth) || n != 1 || len(*sleeps) != 0 {
		t.Fatalf("err=%v n=%d", err, n)
	}
}

func TestNetworkErrorRetried(t *testing.T) {
	var n int32
	p, sleeps := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) == 1 {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				conn.Close()
			}
			return
		}
		sse(w, `{"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`, "[DONE]")
	})
	ch, err := p.Stream(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if text, _ := collect(t, ch); text != "ok" || n != 2 || len(*sleeps) != 1 {
		t.Fatalf("text=%q n=%d sleeps=%v", text, n, *sleeps)
	}
}

func TestRedirectNotFollowed(t *testing.T) {
	var hit, n int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hit, 1)
		sse(w, `{"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`, "[DONE]")
	}))
	t.Cleanup(target.Close)
	p, sleeps := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		http.Redirect(w, r, target.URL+"/chat/completions", http.StatusTemporaryRedirect)
	})
	_, err := p.Stream(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "307") || n != 1 || hit != 0 || len(*sleeps) != 0 {
		t.Fatalf("err=%v n=%d hit=%d sleeps=%v", err, n, hit, *sleeps)
	}
}

func TestCancelDuringRetryWait(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(503)
	}))
	t.Cleanup(srv.Close)
	p := NewOpenAICompat(llm.ProviderInfo{ID: "t", BaseURL: srv.URL, Model: "m"}, "k")
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	start := time.Now()
	_, err := p.Stream(ctx, req)
	if !errors.Is(err, context.Canceled) || time.Since(start) > 2*time.Second {
		t.Fatalf("err=%v après %v", err, time.Since(start))
	}
}

func TestUnavailableKeepsProviderMessage(t *testing.T) {
	p, _ := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		_, _ = w.Write([]byte("{\"message\": \"Requests rate limit exceeded\",\n \"type\": \"rate_limited\"}"))
	})
	_, err := p.Stream(context.Background(), req)
	if !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), `HTTP status 429: {"message": "Requests rate limit exceeded", "type": "rate_limited"}`) {
		t.Fatalf("err = %v", err)
	}
}

// Sous strict-local (WithoutProxy), le client ignore HTTP_PROXY/HTTPS_PROXY :
// une adresse privée acceptée par le profil ne doit pas sortir par un proxy.
func TestWithoutProxy(t *testing.T) {
	o := NewOpenAICompat(llm.ProviderInfo{BaseURL: "http://10.1.2.3:8000/v1"}, "k", WithoutProxy())
	tr, ok := o.client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T, want *http.Transport", o.client.Transport)
	}
	if tr.Proxy != nil {
		t.Error("Proxy non nil sous WithoutProxy")
	}
	if o.client.CheckRedirect == nil {
		t.Error("refus des redirections perdu")
	}
	// Sans l'option : transport par défaut, comportement inchangé.
	if d := NewOpenAICompat(llm.ProviderInfo{}, "k"); d.client.Transport != nil {
		t.Errorf("transport par défaut modifié : %T", d.client.Transport)
	}
}

// Un flux d'appels d'outils seuls (aucun texte avant le chunk final) émet un
// battement de cœur au premier événement SSE : le délai du premier chunk du
// moteur s'arrête sans que le flux compte comme commencé.
func TestStreamToolCallHeartbeat(t *testing.T) {
	p, _ := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		sse(w,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"f","arguments":"{\"a"}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\":1}"}}]},"finish_reason":"tool_calls"}]}`,
			`[DONE]`)
	})
	ch, err := p.Stream(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	var got []llm.Chunk
	for c := range ch {
		got = append(got, c)
	}
	if len(got) != 2 || !got[0].Heartbeat || got[0].Text != "" || got[0].Done ||
		got[1].Heartbeat || !got[1].Done || len(got[1].ToolCalls) != 1 {
		t.Fatalf("chunks %+v", got)
	}
}

// Un flux de texte n'émet aucun battement de cœur.
func TestStreamTextNoHeartbeat(t *testing.T) {
	p, _ := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		sse(w, `{"choices":[{"delta":{"content":"a"}}]}`, `{"choices":[{"delta":{}}]}`, `[DONE]`)
	})
	ch, _ := p.Stream(context.Background(), req)
	for c := range ch {
		if c.Heartbeat {
			t.Fatalf("battement %+v", c)
		}
	}
}
