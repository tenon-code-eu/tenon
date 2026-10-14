package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"codeberg.org/tenon/tenon/internal/llm"
)

const maxRetries = 3

// Option configures an OpenAICompat provider.
type Option func(*OpenAICompat)

// WithHTTPClient sets the HTTP client.
func WithHTTPClient(c *http.Client) Option { return func(o *OpenAICompat) { o.client = c } }

// WithSleep overrides the wait function used between retries.
func WithSleep(f func(time.Duration)) Option { return func(o *OpenAICompat) { o.sleep = f } }

// OpenAICompat implements Provider for OpenAI-compatible chat completions.
type OpenAICompat struct {
	info   llm.ProviderInfo
	apiKey string
	client *http.Client
	sleep  func(time.Duration)
}

// NewOpenAICompat builds a provider for info.BaseURL.
func NewOpenAICompat(info llm.ProviderInfo, apiKey string, opts ...Option) *OpenAICompat {
	o := &OpenAICompat{info: info, apiKey: apiKey, client: &http.Client{}, sleep: time.Sleep}
	for _, opt := range opts {
		opt(o)
	}
	return o
}

// Info returns the provider description.
func (o *OpenAICompat) Info() llm.ProviderInfo { return o.info }

type wireFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}
type wireToolCall struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"`
	Function wireFunc `json:"function"`
}
type wireMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content"`
	ToolCalls  []wireToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}
type wireToolDef struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}
type wireRequest struct {
	Model    string        `json:"model"`
	Messages []wireMessage `json:"messages"`
	Tools    []wireToolDef `json:"tools,omitempty"`
	Stream   bool          `json:"stream"`
}

func orEmptyObject(b []byte) string {
	if len(bytes.TrimSpace(b)) == 0 {
		return "{}"
	}
	return string(b)
}

func buildBody(req llm.Request) ([]byte, error) {
	w := wireRequest{Model: req.Model, Stream: true}
	for _, m := range req.Messages {
		wm := wireMessage{Role: string(m.Role), Content: m.Content, ToolCallID: m.ToolCallID}
		for _, tc := range m.ToolCalls {
			wm.ToolCalls = append(wm.ToolCalls, wireToolCall{ID: tc.ID, Type: "function",
				Function: wireFunc{Name: tc.Name, Arguments: orEmptyObject(tc.Args)}})
		}
		w.Messages = append(w.Messages, wm)
	}
	for _, t := range req.Tools {
		var d wireToolDef
		d.Type = "function"
		d.Function.Name = t.Name
		d.Function.Description = t.Description
		d.Function.Parameters = json.RawMessage(orEmptyObject(t.Schema))
		w.Tools = append(w.Tools, d)
	}
	return json.Marshal(w)
}

// connect sends the request, retrying 429, 5xx and network errors before the
// first byte of the stream. It returns an open 200 response.
func (o *OpenAICompat) connect(ctx context.Context, body []byte) (*http.Response, error) {
	url := strings.TrimRight(o.info.BaseURL, "/") + "/chat/completions"
	var lastErr error
	for attempt := 0; ; attempt++ {
		hr, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		hr.Header.Set("Content-Type", "application/json")
		hr.Header.Set("Accept", "text/event-stream")
		if o.apiKey != "" {
			hr.Header.Set("Authorization", "Bearer "+o.apiKey)
		}
		wait := time.Second << attempt
		resp, err := o.client.Do(hr)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = err
		} else {
			switch {
			case resp.StatusCode == http.StatusOK:
				return resp, nil
			case resp.StatusCode == 401 || resp.StatusCode == 403:
				resp.Body.Close()
				return nil, ErrAuth
			case resp.StatusCode == 429 || resp.StatusCode >= 500:
				lastErr = fmt.Errorf("statut HTTP %d", resp.StatusCode)
				if s, perr := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After"))); perr == nil && s >= 0 {
					wait = time.Duration(s) * time.Second
				}
				resp.Body.Close()
			default:
				b, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
				resp.Body.Close()
				if resp.StatusCode == 400 {
					s := string(b)
					if strings.Contains(s, "context_length_exceeded") || strings.Contains(s, "maximum context length") {
						return nil, ErrContextLength
					}
				}
				return nil, fmt.Errorf("fournisseur: statut HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
			}
		}
		if attempt >= maxRetries {
			return nil, fmt.Errorf("%w: %v", ErrUnavailable, lastErr)
		}
		o.sleep(wait)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
}

type streamEvent struct {
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
}

// Stream starts a streamed completion.
func (o *OpenAICompat) Stream(ctx context.Context, req llm.Request) (<-chan llm.Chunk, error) {
	body, err := buildBody(req)
	if err != nil {
		return nil, err
	}
	resp, err := o.connect(ctx, body)
	if err != nil {
		return nil, err
	}
	ch := make(chan llm.Chunk, 8)
	go o.pump(ctx, resp, ch)
	return ch, nil
}

func (o *OpenAICompat) pump(ctx context.Context, resp *http.Response, ch chan<- llm.Chunk) {
	defer close(ch)
	defer resp.Body.Close()

	// A cancelled context closes the body so a blocked read returns.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			resp.Body.Close()
		case <-stop:
		}
	}()

	final := func(c llm.Chunk) {
		c.Done = true
		// Live context: block until the consumer reads, never drop the
		// terminal chunk. Only once ctx is cancelled is the wait bounded.
		select {
		case ch <- c:
			return
		case <-ctx.Done():
		}
		select {
		case ch <- c:
		case <-time.After(time.Second):
		}
	}

	type acc struct {
		id, name string
		args     strings.Builder
	}
	calls := map[int]*acc{}
	finished, cancelled := false, false

	rerr := readSSE(resp.Body, func(data string) bool {
		if ctx.Err() != nil {
			cancelled = true
			return false
		}
		if data == "[DONE]" {
			finished = true
			return false
		}
		var ev streamEvent
		if json.Unmarshal([]byte(data), &ev) != nil {
			return true
		}
		for _, c := range ev.Choices {
			if c.Delta.Content != "" {
				select {
				case ch <- llm.Chunk{Text: c.Delta.Content}:
				case <-ctx.Done():
					cancelled = true
					return false
				}
			}
			for _, tc := range c.Delta.ToolCalls {
				a := calls[tc.Index]
				if a == nil {
					a = &acc{}
					calls[tc.Index] = a
				}
				if tc.ID != "" {
					a.id = tc.ID
				}
				if tc.Function.Name != "" {
					a.name = tc.Function.Name
				}
				a.args.WriteString(tc.Function.Arguments)
			}
			if c.FinishReason != nil && *c.FinishReason != "" {
				finished = true
			}
		}
		return true
	})

	if cancelled || ctx.Err() != nil {
		final(llm.Chunk{Err: ctx.Err()})
		return
	}
	if rerr != nil {
		final(llm.Chunk{Err: fmt.Errorf("lecture du flux: %w", rerr)})
		return
	}
	if !finished {
		final(llm.Chunk{Err: ErrIncomplete})
		return
	}
	idx := make([]int, 0, len(calls))
	for i := range calls {
		idx = append(idx, i)
	}
	sort.Ints(idx)
	var out []llm.ToolCall
	for _, i := range idx {
		a := calls[i]
		out = append(out, llm.ToolCall{ID: a.id, Name: a.name, Args: json.RawMessage(orEmptyObject([]byte(a.args.String())))})
	}
	final(llm.Chunk{ToolCalls: out})
}
