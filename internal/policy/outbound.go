package policy

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/tenon-code-eu/tenon/internal/llm"
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

// redactArgs masks tool-call arguments with RedactJSON. Arguments that were
// valid JSON stay valid; if masking cannot guarantee that, the whole value is
// replaced (fail closed).
func (o *Outbound) redactArgs(args json.RawMessage) (json.RawMessage, map[string]int) {
	if args == nil {
		return nil, nil
	}
	text, counts := o.r.RedactJSON(string(args))
	if json.Valid(args) && !json.Valid([]byte(text)) {
		return json.RawMessage(`"[REDACTED]"`), map[string]int{"SECRET": 1}
	}
	return json.RawMessage(text), counts
}

func (o *Outbound) walk(v any, counts map[string]int) any { return o.r.walk(v, counts) }
