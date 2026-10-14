// Package provider talks to LLM backends over HTTP.
package provider

import (
	"context"
	"errors"

	"codeberg.org/tenon/tenon/internal/llm"
)

// Provider streams completions from an LLM backend.
type Provider interface {
	Info() llm.ProviderInfo
	Stream(ctx context.Context, req llm.Request) (<-chan llm.Chunk, error)
}

var (
	// ErrAuth means the provider rejected the credentials (401, 403).
	ErrAuth = errors.New("authentification refusée par le fournisseur")
	// ErrContextLength means the prompt exceeds the model context window.
	ErrContextLength = errors.New("contexte trop long pour le modèle")
	// ErrIncomplete means the stream ended without a completion marker.
	ErrIncomplete = errors.New("flux interrompu avant la fin de la réponse")
	// ErrUnavailable means the provider stayed unreachable after retries.
	ErrUnavailable = errors.New("fournisseur indisponible")
)
