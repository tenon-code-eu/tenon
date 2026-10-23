// Package routing ordonne les fournisseurs candidats et décide quelles
// erreurs déclenchent un repli.
package routing

import (
	"context"
	"errors"
	"net"
	"syscall"
	"time"

	"github.com/tenon-code-eu/tenon/internal/llm"
	"github.com/tenon-code-eu/tenon/internal/provider"
)

// ErrNoFirstChunk signale qu'aucune réponse n'est arrivée avant le délai.
var ErrNoFirstChunk = errors.New("no response before timeout")

// FirstChunkTimeout est le délai maximal avant le premier chunk (ou
// battement de cœur, llm.Chunk.Heartbeat), compté après le retour de Stream.
// Le moteur ne l'applique que si un autre candidat peut prendre le relais
// dans le même envoi.
const FirstChunkTimeout = 60 * time.Second

// Candidate est un fournisseur identifié.
type Candidate struct {
	ID       string
	Provider provider.Provider
	// Window est la fenêtre de contexte du fournisseur, en jetons estimés
	// (0 : celle de la session).
	Window int
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
	// Un candidat « local » qui ne l'est pas est ignoré : une session
	// sensible ne doit jamais atteindre un fournisseur distant.
	if local != nil && local.Provider != nil && local.Provider.Info().Jurisdiction == llm.Local {
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

// Retryable classe l'erreur : indisponibilité, erreur de connexion ou délai
// avant le premier chunk sont réessayables ; authentification, contexte
// trop long, flux interrompu, annulation et autres 4xx ne le sont pas.
// Elle ne sait pas si une sortie a déjà été produite (une net.Error en
// cours de flux renvoie true) : l'appelant ne doit l'appeler que si aucun
// chunk n'a encore été reçu.
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
