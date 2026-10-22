// Package routing ordonne les fournisseurs candidats et décide quelles
// erreurs déclenchent un repli.
package routing

import (
	"context"
	"errors"
	"net"
	"syscall"
	"time"

	"github.com/tenon-code-eu/tenon/internal/provider"
)

// ErrNoFirstChunk signale qu'aucune réponse n'est arrivée avant le délai.
var ErrNoFirstChunk = errors.New("no response before timeout")

// FirstChunkTimeout est le délai maximal avant le premier chunk.
const FirstChunkTimeout = 60 * time.Second

// Candidate est un fournisseur identifié.
type Candidate struct {
	ID       string
	Provider provider.Provider
}

// Router ordonne les candidats.
type Router struct {
	current  Candidate
	local    *Candidate
	fallback []Candidate
}

// New construit un Router ; les replis égaux au courant ou en double sont ignorés.
func New(current Candidate, local *Candidate, fallback []Candidate) *Router {
	r := &Router{current: current}
	if local != nil {
		l := *local
		r.local = &l
	}
	seen := map[string]bool{current.ID: true}
	for _, c := range fallback {
		if seen[c.ID] {
			continue
		}
		seen[c.ID] = true
		r.fallback = append(r.fallback, c)
	}
	return r
}

// Candidates renvoie l'ordre d'essai. Une session sensible n'a que le
// fournisseur local (jamais de repli distant).
func (r *Router) Candidates(sensitive bool) []Candidate {
	if sensitive {
		if r.local == nil {
			return []Candidate{}
		}
		return []Candidate{*r.local}
	}
	out := make([]Candidate, 0, 1+len(r.fallback))
	out = append(out, r.current)
	return append(out, r.fallback...)
}

// Local renvoie le fournisseur local, s'il existe.
func (r *Router) Local() (Candidate, bool) {
	if r.local == nil {
		return Candidate{}, false
	}
	return *r.local, true
}

// Retryable indique si l'erreur justifie un repli : indisponibilité,
// erreur de connexion ou délai avant le premier chunk. Jamais pour
// l'authentification, le contexte trop long, un flux interrompu (sortie
// déjà produite), une annulation ou les autres erreurs 4xx.
func Retryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrNoFirstChunk) {
		return true
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, provider.ErrAuth) || errors.Is(err, provider.ErrContextLength) ||
		errors.Is(err, provider.ErrIncomplete) {
		return false
	}
	if errors.Is(err, provider.ErrUnavailable) || errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne)
}
