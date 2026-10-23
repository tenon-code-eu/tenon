package headless

import (
	"testing"

	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/i18n"
	"github.com/tenon-code-eu/tenon/internal/llm"
)

// Ruling R15 : routing présent fait foi, même vide (local indisponible) ;
// un courant local n'est alors pas pris pour le fournisseur local.
func TestRoutingStartedEffectiveLocal(t *testing.T) {
	var r Routing
	r.Started(event.SessionStartedPayload{
		Provider: llm.ProviderInfo{ID: "vllm", Jurisdiction: llm.Local},
		Routing:  &event.RoutingInfo{Fallback: []string{"mistral"}},
	})
	if r.Local != "" {
		t.Fatalf("local = %q", r.Local)
	}
	if got := r.Classified(i18n.EN, event.SessionClassifiedPayload{Reason: "path"}); got != "classification: session is sensitive (path), no local provider" {
		t.Fatalf("avis : %q", got)
	}
	r = Routing{}
	r.Started(event.SessionStartedPayload{Provider: llm.ProviderInfo{ID: "ollama", Jurisdiction: llm.Local}})
	if r.Local != "ollama" {
		t.Fatalf("sans routing : %q", r.Local)
	}
}
