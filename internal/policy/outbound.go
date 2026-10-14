package policy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"codeberg.org/tenon/tenon/internal/llm"
)

// ErrJurisdiction is returned when a non-EU provider has no recorded consent.
var ErrJurisdiction = errors.New("fournisseur hors UE sans consentement")

// Outbound gates and redacts every message sent to an LLM provider.
type Outbound struct {
	r *Redactor

	mu      sync.Mutex
	consent map[string]bool
}

// NewOutbound returns an Outbound using r for masking.
func NewOutbound(r *Redactor) *Outbound {
	return &Outbound{r: r, consent: map[string]bool{}}
}

// Consent records that the user accepted sending data to providerID.
func (o *Outbound) Consent(providerID string) {
	o.mu.Lock()
	o.consent[providerID] = true
	o.mu.Unlock()
}

// Check enforces the jurisdiction rule, then returns a redacted deep copy of
// msgs with the number of masks per type. The input is never modified.
// Only EU and local providers pass freely; any other jurisdiction (including
// an empty one) requires consent.
func (o *Outbound) Check(p llm.ProviderInfo, msgs []llm.Message) ([]llm.Message, map[string]int, error) {
	if p.Jurisdiction != llm.EU && p.Jurisdiction != llm.Local {
		o.mu.Lock()
		ok := o.consent[p.ID]
		o.mu.Unlock()
		if !ok {
			return nil, nil, fmt.Errorf("%w : %s", ErrJurisdiction, p.ID)
		}
	}
	counts := map[string]int{}
	add := func(c map[string]int) {
		for k, v := range c {
			counts[k] += v
		}
	}
	out := make([]llm.Message, len(msgs))
	for i, m := range msgs {
		nm := m
		var c map[string]int
		nm.Content, c = o.r.Redact(m.Content)
		add(c)
		if m.ToolCalls != nil {
			nm.ToolCalls = make([]llm.ToolCall, len(m.ToolCalls))
			for j, tc := range m.ToolCalls {
				ntc := tc
				ntc.Args, c = o.redactArgs(tc.Args)
				add(c)
				nm.ToolCalls[j] = ntc
			}
		}
		out[i] = nm
	}
	return out, counts, nil
}

// redactArgs masks raw JSON text. If that breaks valid JSON (a match spanning
// an escape sequence), it falls back to masking each decoded string.
func (o *Outbound) redactArgs(args json.RawMessage) (json.RawMessage, map[string]int) {
	if args == nil {
		return nil, nil
	}
	text, counts := o.r.Redact(string(args))
	if json.Valid(args) && !json.Valid([]byte(text)) {
		dec := json.NewDecoder(bytes.NewReader(args))
		dec.UseNumber()
		var v any
		if err := dec.Decode(&v); err == nil {
			counts = map[string]int{}
			v = o.walk(v, counts)
			var buf bytes.Buffer
			enc := json.NewEncoder(&buf)
			enc.SetEscapeHTML(false)
			if err := enc.Encode(v); err == nil {
				return json.RawMessage(bytes.TrimRight(buf.Bytes(), "\n")), counts
			}
		}
		return json.RawMessage(`"[REDACTED]"`), map[string]int{"SECRET": 1}
	}
	return json.RawMessage(text), counts
}

func (o *Outbound) walk(v any, counts map[string]int) any {
	merge := func(c map[string]int) {
		for k, n := range c {
			counts[k] += n
		}
	}
	switch t := v.(type) {
	case string:
		s, c := o.r.Redact(t)
		merge(c)
		return s
	case []any:
		for i := range t {
			t[i] = o.walk(t[i], counts)
		}
		return t
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			nk, c := o.r.Redact(k)
			merge(c)
			if xs, ok := x.(string); ok && len(xs) >= 8 && reSecretKey.MatchString(k) {
				m := o.r.maskSecret(xs)
				if m != xs {
					counts["SECRET"]++
				}
				out[nk] = m
				continue
			}
			out[nk] = o.walk(x, counts)
		}
		return out
	}
	return v
}
