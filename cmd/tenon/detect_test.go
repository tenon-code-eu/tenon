package main

import (
	"context"
	"net/http"
	"testing"

	"github.com/tenon-code-eu/tenon/internal/config"
	"github.com/tenon-code-eu/tenon/internal/i18n"
	"github.com/tenon-code-eu/tenon/internal/provider"
)

// detectLocal passe un client sans proxy sous strict-local, et le client par
// défaut (nil) sinon.
func TestDetectLocalNoProxy(t *testing.T) {
	var got *http.Client
	old := detectFuncs["ollama"]
	t.Cleanup(func() { detectFuncs["ollama"] = old })
	detectFuncs["ollama"] = func(_ context.Context, c *http.Client, _, _ string) (provider.Detected, error) {
		got = c
		return provider.Detected{Model: "m", ContextWindow: 32000}, nil
	}
	pc := config.Provider{ID: "ollama", Name: "Ollama", BaseURL: "http://192.168.0.5:11434/v1", Detect: "ollama"}
	if _, _, err := detectLocal(context.Background(), i18n.EN, pc, true); err != nil {
		t.Fatal(err)
	}
	if tr, ok := got.Transport.(*http.Transport); got == nil || !ok || tr.Proxy != nil {
		t.Fatalf("strict-local : client %#v, Proxy doit être nil", got)
	}
	if _, _, err := detectLocal(context.Background(), i18n.EN, pc, false); err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("sans profil : client %#v, attendu nil", got)
	}
}
