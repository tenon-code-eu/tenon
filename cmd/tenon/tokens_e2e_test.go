package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/tenon-code-eu/tenon/internal/event"
)

// sseUsage est une réponse texte suivie d'un fragment d'usage réel.
func sseUsage(text string, in, out, cached int) []string {
	return append(sseText(text), fmt.Sprintf(
		`{"choices":[],"usage":{"prompt_tokens":%d,"completion_tokens":%d,"prompt_tokens_details":{"cached_tokens":%d}}}`, in, out, cached))
}

func TestTokensLineText(t *testing.T) {
	f := newFakeLLM(t, sseUsage("bonjour", 100, 20, 40))
	e := newEnv(t, f, "eu")
	code, out, errb := e.run("-p", "salut")
	if code != 0 || !strings.Contains(out, "bonjour") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errb)
	}
	if !strings.Contains(errb, "tokens: in 100 · out 20 · cached 40 · session 120\n") {
		t.Fatalf("ligne tokens absente : %q", errb)
	}
	// Sortie JSON : aucune ligne tokens ; l'usage est dans les événements.
	code, out, errb = e.run("-p", "salut", "--output", "json")
	if code != 0 || strings.Contains(errb, "tokens:") || strings.Contains(out, "tokens:") {
		t.Fatalf("json : code=%d stderr=%q", code, errb)
	}
	if !strings.Contains(out, `"type":"Usage"`) {
		t.Fatalf("événement Usage absent : %q", out)
	}
}

func TestBudgetStrictExit4(t *testing.T) {
	f := newFakeLLM(t, sseUsage("un", 100, 20, 0))
	e := newEnvExtra(t, f, "eu", "\n[budget]\nsession_tokens = 50\n")
	// Le premier tour dépasse le budget : il se termine normalement.
	code, _, errb := e.run("-p", "un", "--strict")
	if code != 0 {
		t.Fatalf("tour 1 : code=%d stderr=%q", code, errb)
	}
	if !strings.Contains(errb, "budget : 120 sur 50 tokens utilisés (240 %)") {
		t.Fatalf("avis de budget absent : %q", errb)
	}
	// Le suivant est refusé : code 4 sous --strict, 0 sinon.
	code, _, errb = e.run("-p", "deux", "--strict", "--resume", "last")
	if code != 4 || !strings.Contains(errb, "strict : tour terminé avec budget_exceeded") {
		t.Fatalf("strict : code=%d stderr=%q", code, errb)
	}
	code, _, errb = e.run("-p", "trois", "--resume", "last")
	if code != 0 || strings.Contains(errb, "strict") {
		t.Fatalf("sans strict : code=%d stderr=%q", code, errb)
	}
	if n := len(f.requests()); n != 1 {
		t.Fatalf("%d requêtes, 1 attendue", n)
	}
	var reasons []string
	for _, p := range payloadsOf[event.TurnCompletedPayload](t, e.journalEvents(), event.TurnCompleted) {
		reasons = append(reasons, p.Reason)
	}
	if strings.Join(reasons, ",") != "done,budget_exceeded,budget_exceeded" {
		t.Fatalf("raisons : %v", reasons)
	}
}

func TestCompactionProviderRefused(t *testing.T) {
	main := newFakeLLM(t, sseText("ok"))
	us := newFakeLLM(t, sseText("jamais"))
	e := newEnvExtra(t, main, "eu", fmt.Sprintf(`
[[providers]]
id = "us"
name = "US Inc"
base_url = %q
api_key_env = "TEST_KEY"
model = "m-us"
country = "US"
jurisdiction = "non-eu"

[routing]
compaction = "us"
`, us.srv.URL))
	code, _, errb := e.run("--lang", "en", "-p", "salut")
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	want := `routing: compaction provider "us" refused (outside the EU without --allow-non-eu): the session provider is used`
	if !strings.Contains(errb, want) {
		t.Fatalf("avis absent : %q", errb)
	}
	if len(us.requests()) != 0 {
		t.Fatal("le fournisseur refusé a reçu une requête")
	}
}

func TestCompactionProviderAccepted(t *testing.T) {
	main := newFakeLLM(t, sseText("ok"))
	small := newFakeLLM(t, sseText("résumé"))
	e := newEnvExtra(t, main, "eu", fmt.Sprintf(`
[[providers]]
id = "petit"
name = "Petit SAS"
base_url = %q
api_key_env = "TEST_KEY"
model = "m-small"
country = "FR"
jurisdiction = "eu"

[routing]
compaction = "petit"
subagents = "petit"
`, small.srv.URL))
	code, _, errb := e.run("--lang", "en", "-p", "salut")
	if code != 0 || strings.Contains(errb, "refused") {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	if len(main.requests()) != 1 || len(small.requests()) != 0 {
		t.Fatalf("main=%d small=%d", len(main.requests()), len(small.requests()))
	}
}
