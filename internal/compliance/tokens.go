package compliance

import (
	"math"
	"sort"

	"github.com/tenon-code-eu/tenon/internal/audit"
	"github.com/tenon-code-eu/tenon/internal/event"
)

// TokenCounts regroupe les tokens d'un périmètre. EstimatedShare est la part
// (0 à 1) d'entrée + sortie qui vient d'estimations et non du fournisseur ;
// Saved est la somme des ContextShaped.saved_tokens.
type TokenCounts struct {
	Input          int     `json:"input"`
	Output         int     `json:"output"`
	Cached         int     `json:"cached"`
	EstimatedShare float64 `json:"estimated_share"`
	Saved          int     `json:"saved"`
}

// SessionTokens : tokens d'une session (sous-agents et compaction compris).
type SessionTokens struct {
	Session string `json:"session"`
	TokenCounts
}

// ProviderTokens : tokens d'un fournisseur et modèle (celui qui a répondu).
type ProviderTokens struct {
	ID    string `json:"id"`
	Model string `json:"model"`
	TokenCounts
}

// Energy est l'énergie et le CO₂ estimés ; présent seulement quand les deux
// facteurs sont déclarés (jamais de valeur par défaut).
type Energy struct {
	Wh    float64 `json:"wh"`
	GCO2e float64 `json:"gco2e"`
}

// Tokens est la section tokens du rapport ; omise sans événement Usage.
type Tokens struct {
	Total     TokenCounts      `json:"total"`
	Sessions  []SessionTokens  `json:"sessions"`
	Providers []ProviderTokens `json:"providers"`
	Energy    *Energy          `json:"energy,omitempty"`
}

// tokenAcc accumule un périmètre ; est compte les tokens estimés.
type tokenAcc struct {
	in, out, cached, est, saved int
}

func (a *tokenAcc) counts() TokenCounts {
	c := TokenCounts{Input: a.in, Output: a.out, Cached: a.cached, Saved: a.saved}
	if t := a.in + a.out; t > 0 {
		c.EstimatedShare = math.Round(float64(a.est)/float64(t)*1e4) / 1e4
	}
	return c
}

// tokenState suit les tokens pendant la lecture du journal.
type tokenState struct {
	total     tokenAcc
	sessions  map[string]*tokenAcc
	providers map[[2]string]*tokenAcc
	// pending : économies d'une vue façonnée, rattachées au fournisseur de
	// l'appel suivant (même session, même sous-agent).
	pending map[sessKey]int
	seen    bool
}

func newTokenState() *tokenState {
	return &tokenState{sessions: map[string]*tokenAcc{}, providers: map[[2]string]*tokenAcc{}, pending: map[sessKey]int{}}
}

func acc[K comparable](m map[K]*tokenAcc, k K) *tokenAcc {
	a := m[k]
	if a == nil {
		a = &tokenAcc{}
		m[k] = a
	}
	return a
}

func (s *tokenState) line(b *builder, l audit.Line) {
	switch l.Type {
	case event.Usage:
		var p event.UsagePayload
		if !decode(l, &p) {
			return
		}
		s.seen = true
		pk := [2]string{b.o.clean(p.Provider), b.o.clean(p.Model)}
		pend := sessKey{l.Session, p.ParentCallID}
		saved := s.pending[pend]
		delete(s.pending, pend)
		for _, a := range []*tokenAcc{&s.total, acc(s.sessions, l.Session), acc(s.providers, pk)} {
			a.in += p.Input
			a.out += p.Output
			a.cached += p.Cached
			if p.Estimated {
				a.est += p.Input + p.Output
			}
		}
		// Les économies déjà comptées pour la session et le total le sont à
		// l'arrivée de ContextShaped ; ici seulement le fournisseur.
		acc(s.providers, pk).saved += saved
	case event.ContextShaped:
		var p event.ContextShapedPayload
		if !decode(l, &p) {
			return
		}
		s.seen = true
		s.total.saved += p.SavedTokens
		acc(s.sessions, l.Session).saved += p.SavedTokens
		s.pending[sessKey{l.Session, p.ParentCallID}] += p.SavedTokens
	}
}

// tokens publie la section ; nil sans Usage (anciens journaux).
func (s *tokenState) section(o Options) *Tokens {
	if !s.seen {
		return nil
	}
	t := &Tokens{Total: s.total.counts(), Sessions: []SessionTokens{}, Providers: []ProviderTokens{}}
	for sid, a := range s.sessions {
		t.Sessions = append(t.Sessions, SessionTokens{Session: o.clean(sid), TokenCounts: a.counts()})
	}
	sort.Slice(t.Sessions, func(i, j int) bool { return t.Sessions[i].Session < t.Sessions[j].Session })
	for k, a := range s.providers {
		t.Providers = append(t.Providers, ProviderTokens{ID: k[0], Model: k[1], TokenCounts: a.counts()})
	}
	sort.Slice(t.Providers, func(i, j int) bool {
		a, c := t.Providers[i], t.Providers[j]
		if a.ID != c.ID {
			return a.ID < c.ID
		}
		return a.Model < c.Model
	})
	if o.WhPerMTok > 0 && o.GCO2PerKWh > 0 {
		wh := float64(s.total.in+s.total.out) / 1e6 * o.WhPerMTok
		t.Energy = &Energy{Wh: round6(wh), GCO2e: round6(wh / 1000 * o.GCO2PerKWh)}
	}
	return t
}

// round6 écarte le bruit de virgule flottante.
func round6(f float64) float64 { return math.Round(f*1e6) / 1e6 }
