package engine

import (
	"context"
	"sync"

	"github.com/tenon-code-eu/tenon/internal/llm"
)

// fakeProvider rejoue un script : chaque appel à Stream renvoie l'entrée
// suivante (la dernière une fois le script épuisé) et enregistre la requête.
type fakeProvider struct {
	info   llm.ProviderInfo
	script [][]llm.Chunk

	mu       sync.Mutex
	requests []llm.Request
}

func (f *fakeProvider) Info() llm.ProviderInfo { return f.info }

func (f *fakeProvider) Stream(ctx context.Context, req llm.Request) (<-chan llm.Chunk, error) {
	f.mu.Lock()
	i := len(f.requests)
	f.requests = append(f.requests, req)
	f.mu.Unlock()
	if i >= len(f.script) {
		i = len(f.script) - 1
	}
	var chunks []llm.Chunk
	if i >= 0 {
		chunks = f.script[i]
	}
	ch := make(chan llm.Chunk)
	go func() {
		defer close(ch)
		for _, c := range chunks {
			select {
			case ch <- c:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

func (f *fakeProvider) reqs() []llm.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]llm.Request(nil), f.requests...)
}
