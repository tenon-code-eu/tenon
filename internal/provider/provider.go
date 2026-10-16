// Package provider talks to LLM backends over HTTP.
package provider

import (
	"context"
	"errors"

	"github.com/tenon-code-eu/tenon/internal/llm"
)

// Provider streams completions from an LLM backend.
type Provider interface {
	Info() llm.ProviderInfo
	Stream(ctx context.Context, req llm.Request) (<-chan llm.Chunk, error)
}

var (
	// ErrAuth means the provider rejected the credentials (401, 403).
	ErrAuth = errors.New("authentication rejected by the provider")
	// ErrContextLength means the prompt exceeds the model context window.
	ErrContextLength = errors.New("context too long for the model")
	// ErrIncomplete means the stream ended without a completion marker.
	ErrIncomplete = errors.New("stream interrupted before the end of the response")
	// ErrUnavailable means the provider stayed unreachable after retries.
	ErrUnavailable = errors.New("provider unavailable")
)
