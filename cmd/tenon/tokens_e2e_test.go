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

// compactionRun : fournisseur de session « tiny » (fenêtre de 20000 jetons)
// servi par main, et fournisseur de compaction dédié extra. Deux tours de
// 10000 puis 8000 jetons estimés forcent une compaction au second.
func compactionRun(t *testing.T, main *fakeLLM, extra string) (*env, string) {
	t.Helper()
	e := newEnvExtra(t, main, "eu", fmt.Sprintf(`
[[providers]]
id = "tiny"
name = "Tiny SAS"
base_url = %q
api_key_env = "TEST_KEY"
model = "m-tiny"
context_window = 20000
country = "FR"
jurisdiction = "eu"
`, main.srv.URL)+extra)
	code, _, errb := e.run("--lang", "en", "--provider", "tiny", "-p", strings.Repeat("a", 40000))
	if code != 0 {
		t.Fatalf("tour 1 : code=%d stderr=%q", code, errb)
	}
	code, _, errb = e.run("--lang", "en", "--provider", "tiny", "--resume", "last", "-p", strings.Repeat("b", 32000))
	if code != 0 {
		t.Fatalf("tour 2 : code=%d stderr=%q", code, errb)
	}
	if n := len(payloadsOf[event.ContextCompactedPayload](t, e.journalEvents(), event.ContextCompacted)); n != 1 {
		t.Fatalf("%d compactions, stderr=%q", n, errb)
	}
	return e, errb
}

// summaries compte les requêtes de résumé reçues par f.
func summaries(f *fakeLLM) int {
	n := 0
	for _, r := range f.requests() {
		if strings.Contains(r, "Faithfully summarize") {
			n++
		}
	}
	return n
}

func TestCompactionE2EDedicatedProvider(t *testing.T) {
	main := newFakeLLM(t, sseText("ok"))
	small := newFakeLLM(t, sseText("résumé"))
	compactionRun(t, main, fmt.Sprintf(`
[[providers]]
id = "petit"
name = "Petit SAS"
base_url = %q
api_key_env = "TEST_KEY"
model = "m-small"
context_window = 64000
country = "FR"
jurisdiction = "eu"

[routing]
compaction = "petit"
`, small.srv.URL))
	if len(small.requests()) != 1 || summaries(small) != 1 {
		t.Fatalf("petit : %d requêtes, %d résumés", len(small.requests()), summaries(small))
	}
	if summaries(main) != 0 || len(main.requests()) != 2 {
		t.Fatalf("session : %d requêtes, %d résumés", len(main.requests()), summaries(main))
	}
}

func TestCompactionE2ERefusedProviderUsesSession(t *testing.T) {
	main := newFakeLLM(t, sseText("ok"))
	us := newFakeLLM(t, sseText("jamais"))
	_, errb := compactionRun(t, main, fmt.Sprintf(`
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
	if !strings.Contains(errb, `routing: compaction provider "us" refused`) {
		t.Fatalf("avis absent : %q", errb)
	}
	if len(us.requests()) != 0 {
		t.Fatal("le fournisseur refusé a reçu une requête")
	}
	if summaries(main) != 1 || len(main.requests()) != 3 {
		t.Fatalf("session : %d requêtes, %d résumés", len(main.requests()), summaries(main))
	}
}
