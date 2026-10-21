package policy

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/tenon-code-eu/tenon/internal/hostmatch"
	"github.com/tenon-code-eu/tenon/internal/llm"
	"github.com/tenon-code-eu/tenon/internal/orgpolicy"
	"github.com/tenon-code-eu/tenon/internal/profile"
)

// ErrJurisdiction is returned when a non-EU provider has no recorded consent.
var ErrJurisdiction = errors.New("non-EU provider without consent")

// ErrAllowlist est renvoyée quand l'hôte du fournisseur n'est pas dans une
// liste blanche (provider_allowlist).
var ErrAllowlist = errors.New("allowlist violation")

// Outbound gates and redacts every message sent to an LLM provider.
type Outbound struct {
	r *Redactor

	mu      sync.Mutex
	consent map[string]bool
	profile profile.Name
	hostPol *orgpolicy.Policy
	lists   []hostmatch.List
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

// SetProfile sets the compliance profile re-checked before every send
// (defense in depth: the CLI already refuses a non-compliant provider).
func (o *Outbound) SetProfile(n profile.Name) {
	o.mu.Lock()
	o.profile = n
	o.mu.Unlock()
}

// SetHostPolicy fixe la politique d'organisation dont la liste blanche d'hôtes
// est revérifiée avant chaque envoi (nil : aucune restriction).
func (o *Outbound) SetHostPolicy(p *orgpolicy.Policy) {
	o.mu.Lock()
	o.hostPol = p
	o.mu.Unlock()
}

// SetAllowlists fixe les listes blanches d'hôtes (globale, projet) revérifiées
// avant chaque envoi ; chacune doit accepter l'hôte (vide : aucune restriction).
func (o *Outbound) SetAllowlists(lists []hostmatch.List) {
	c := append([]hostmatch.List(nil), lists...)
	o.mu.Lock()
	o.lists = c
	o.mu.Unlock()
}

// Check enforces the compliance profile first (a recorded consent never
// bypasses it; its error wraps profile.ErrProfile), then the jurisdiction rule, then returns a redacted deep copy of
// msgs with the number of masks per type. The input is never modified.
// Only EU and local providers pass freely; any other jurisdiction (including
// an empty one) requires consent.
func (o *Outbound) Check(p llm.ProviderInfo, msgs []llm.Message) ([]llm.Message, map[string]int, error) {
	o.mu.Lock()
	n := o.profile
	hp := o.hostPol
	lists := append([]hostmatch.List(nil), o.lists...)
	o.mu.Unlock()
	if err := profile.CheckProvider(n, p); err != nil {
		return nil, nil, err
	}
	// Liste blanche d'hôtes de la politique : un consentement ne la contourne
	// pas (erreur distincte de ErrJurisdiction, donc aucune demande).
	if host := orgpolicy.HostOf(p.BaseURL); hp != nil && !hp.AllowsHost(host) {
		return nil, nil, fmt.Errorf("%w: provider %q (host %q) not allowed by policy %s", orgpolicy.ErrPolicy, p.ID, host, hp.ID)
	}
	// Listes blanches de fournisseurs : même règle, un consentement ne les
	// contourne pas.
	host := hostmatch.HostOf(p.BaseURL)
	for _, l := range lists {
		if !l.Allows(host) {
			return nil, nil, fmt.Errorf("%w: provider %q (host %q) not in provider_allowlist (%s)", ErrAllowlist, p.ID, host, l.Name)
		}
	}
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
