package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/tenon-code-eu/tenon/internal/config"
	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/i18n"
	"github.com/tenon-code-eu/tenon/internal/llm"
	"github.com/tenon-code-eu/tenon/internal/orgpolicy"
	"github.com/tenon-code-eu/tenon/internal/provider"
)

// paie est le contenu du fichier classé des tests de routage.
const paie = "nom;salaire\nDupont;48213\n"

// localProvider renvoie la déclaration TOML d'un fournisseur local id servi
// par f (modèle et fenêtre fixés : aucune détection).
func localProvider(id string, f *fakeLLM) string {
	return fmt.Sprintf(`
[[providers]]
id = %q
name = "Local"
base_url = %q
model = "local-model"
context_window = 64000
jurisdiction = "local"
`, id, f.srv.URL)
}

// journal renvoie le contenu de l'unique fichier de session.
func (e *env) journal() string {
	e.t.Helper()
	files, err := e.sessionFiles()
	if err != nil || len(files) != 1 {
		e.t.Fatalf("sessions : %v %v", files, err)
	}
	b, err := os.ReadFile(filepath.Join(e.sessionsDir(), files[0].Name()))
	if err != nil {
		e.t.Fatal(err)
	}
	return string(b)
}

// journalEvents décode les événements du journal de session.
func (e *env) journalEvents() []event.Event {
	e.t.Helper()
	return decodeEvents(e.t, e.journal())
}

func payloadsOf[T any](t *testing.T, evs []event.Event, typ event.Type) []T {
	t.Helper()
	var out []T
	for _, ev := range evs {
		if ev.Type != typ {
			continue
		}
		var p T
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

func TestRoutingE2ESensitive(t *testing.T) {
	remote := newFakeLLM(t, sseCall("read_file", `{"path":"rh/paie.csv"}`), sseText("distant"))
	local := newFakeLLM(t, sseText("lu en local"))
	e := newEnvExtra(t, remote, "eu", localProvider("loc", local)+`
[classification]
paths = ["rh/**"]

[routing]
local = "loc"
`)
	e.writeFile(filepath.Join(e.wd, "rh", "paie.csv"), paie)

	code, out, errb := e.run("--lang", "en", "-p", "lis rh/paie.csv")
	if code != 0 || !strings.Contains(out, "lu en local") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errb)
	}
	if !strings.Contains(errb, "classification: session is sensitive (path), routed to loc\n") {
		t.Fatalf("avis absent : %q", errb)
	}
	if r := remote.requests(); len(r) != 1 || strings.Contains(strings.Join(r, ""), "48213") {
		t.Fatalf("requêtes distantes : %q", r)
	}
	if r := local.requests(); len(r) != 1 || !strings.Contains(r[0], "48213") {
		t.Fatalf("requêtes locales : %q", r)
	}
	evs := e.journalEvents()
	st := payloadsOf[event.SessionStartedPayload](t, evs, event.SessionStarted)
	if len(st) != 1 || st[0].Routing == nil || st[0].Routing.Local != "loc" || len(st[0].Routing.Fallback) != 0 {
		t.Fatalf("SessionStarted.routing : %+v", st)
	}
	cl := payloadsOf[event.SessionClassifiedPayload](t, evs, event.SessionClassified)
	if len(cl) != 1 || cl[0].Reason != "path" || cl[0].Path != "rh/paie.csv" {
		t.Fatalf("SessionClassified : %+v", cl)
	}
	sel := payloadsOf[event.ProviderSelectedPayload](t, evs, event.ProviderSelected)
	if len(sel) != 2 || sel[0].Provider.ID != "test" || sel[0].Reason != "default" ||
		sel[1].Provider.ID != "loc" || sel[1].Reason != "sensitive" {
		t.Fatalf("ProviderSelected : %+v", sel)
	}
}

func TestRoutingE2ENoLocal(t *testing.T) {
	remote := newFakeLLM(t, sseCall("read_file", `{"path":"rh/paie.csv"}`), sseText("refus compris"))
	e := newEnvExtra(t, remote, "eu", `
[classification]
paths = ["rh/**"]
`)
	e.writeFile(filepath.Join(e.wd, "rh", "paie.csv"), paie)

	// Lecture refusée (ToolResult.denied) : code 3, comme tout refus d'action.
	code, out, errb := e.run("--lang", "en", "-p", "lis rh/paie.csv")
	if code != 3 || !strings.Contains(out, "refus compris") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errb)
	}
	r := remote.requests()
	if len(r) != 2 || strings.Contains(strings.Join(r, ""), "48213") {
		t.Fatalf("requêtes : %q", r)
	}
	if !strings.Contains(r[1], "classified content requires a local provider (routing.local)") {
		t.Fatalf("refus absent : %q", r[1])
	}
	st := payloadsOf[event.SessionStartedPayload](t, e.journalEvents(), event.SessionStarted)
	if len(st) != 1 || st[0].Routing != nil {
		t.Fatalf("SessionStarted.routing : %+v", st)
	}
}

func TestRoutingE2EFallback(t *testing.T) {
	r1 := newFakeLLM(t, sseText("jamais"))
	// r1 reste indisponible au-delà des nouvelles tentatives (sans attente).
	var hits atomic.Int32
	r1.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	r2 := newFakeLLM(t, sseText("réponse de secours"))
	e := newEnvExtra(t, r1, "eu", fmt.Sprintf(`
[[providers]]
id = "secours"
name = "Secours"
base_url = %q
api_key_env = "TEST_KEY"
model = "m2"
country = "FR"
jurisdiction = "eu"

[routing]
fallback = ["secours"]
`, r2.srv.URL))

	code, out, errb := e.run("--lang", "en", "-p", "salut")
	if code != 0 || !strings.Contains(out, "réponse de secours") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errb)
	}
	if !strings.Contains(errb, "routing: test failed (provider unavailable: HTTP status 503), using secours\n") {
		t.Fatalf("avis de bascule absent : %q", errb)
	}
	if hits.Load() == 0 || len(r2.requests()) != 1 {
		t.Fatalf("r1=%d r2=%d", hits.Load(), len(r2.requests()))
	}
	evs := e.journalEvents()
	st := payloadsOf[event.SessionStartedPayload](t, evs, event.SessionStarted)
	if len(st) != 1 || st[0].Routing == nil || st[0].Routing.Local != "" || strings.Join(st[0].Routing.Fallback, ",") != "secours" {
		t.Fatalf("SessionStarted.routing : %+v", st)
	}
	sel := payloadsOf[event.ProviderSelectedPayload](t, evs, event.ProviderSelected)
	if len(sel) != 2 || sel[1].Provider.ID != "secours" || sel[1].Reason != "fallback" {
		t.Fatalf("ProviderSelected : %+v", sel)
	}
}

// Review Focus 3 : --provider <local> sans [routing] : la session est déjà
// locale, la lecture classée est permise, sans repli.
func TestRoutingE2EExplicitLocal(t *testing.T) {
	local := newFakeLLM(t, sseCall("read_file", `{"path":"rh/paie.csv"}`), sseText("lu en local"))
	e := newEnvExtra(t, nil, "eu", fmt.Sprintf(`
[[providers]]
id = "ollama"
base_url = %q
model = "llama"
context_window = 64000
jurisdiction = "local"

[classification]
paths = ["rh/**"]
`, local.srv.URL))
	e.writeFile(filepath.Join(e.wd, "rh", "paie.csv"), paie)

	code, out, errb := e.run("--lang", "en", "--provider", "ollama", "-p", "lis rh/paie.csv")
	if code != 0 || !strings.Contains(out, "lu en local") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errb)
	}
	if strings.Contains(errb, "requires a local provider") || strings.Contains(errb, "blocked") {
		t.Fatalf("refus inattendu : %q", errb)
	}
	r := local.requests()
	if len(r) != 2 || !strings.Contains(r[1], "48213") {
		t.Fatalf("requêtes : %q", r)
	}
	if !strings.Contains(errb, "classification: session is sensitive (path), routed to ollama\n") {
		t.Fatalf("avis absent : %q", errb)
	}
	evs := e.journalEvents()
	st := payloadsOf[event.SessionStartedPayload](t, evs, event.SessionStarted)
	if len(st) != 1 || st[0].Routing != nil {
		t.Fatalf("SessionStarted.routing : %+v", st)
	}
	for _, p := range payloadsOf[event.ProviderSelectedPayload](t, evs, event.ProviderSelected) {
		if p.Provider.ID != "ollama" {
			t.Fatalf("fournisseur %q retenu", p.Provider.ID)
		}
	}
}

func TestRoutingConfigCheck(t *testing.T) {
	local := newFakeLLM(t, sseText("x"))
	e := newEnvExtra(t, nil, "eu", localProvider("loc", local)+`
[[providers]]
id = "secours"
base_url = "https://secours.example/v1"
api_key_env = "TEST_KEY"
model = "m2"
jurisdiction = "eu"

[classification]
paths = ["rh/**", "contrats/**"]
markers = ["C2 - Interne"]

[routing]
local = "loc"
fallback = ["secours"]
`)
	code, out, errb := e.run("--lang", "en", "config", "check")
	if code != 0 || !strings.Contains(out, "\nclassification: 2 paths, 1 markers\n") ||
		!strings.Contains(out, "\nrouting: local loc, fallback secours\n") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errb)
	}
	code, out, _ = e.run("config", "check")
	if code != 0 || !strings.Contains(out, "\nclassification : 2 chemins, 1 marquages\n") ||
		!strings.Contains(out, "\nroutage : local loc, repli secours\n") {
		t.Fatalf("fr : code=%d stdout=%q", code, out)
	}

	e = newEnv(t, nil, "eu")
	code, out, errb = e.run("--lang", "en", "config", "check")
	if code != 0 || !strings.Contains(out, "\nclassification: 0 paths, 0 markers\n") || !strings.Contains(out, "\nrouting: none\n") {
		t.Fatalf("sans routage : code=%d stdout=%q stderr=%q", code, out, errb)
	}
	code, out, _ = e.run("config", "check")
	if code != 0 || !strings.Contains(out, "\nroutage : aucun\n") {
		t.Fatalf("sans routage fr : code=%d stdout=%q", code, out)
	}
}

// Ruling R5 : un fournisseur local sur un hôte non local est signalé au
// démarrage, sans refus.
func TestRoutingNonLoopbackNotice(t *testing.T) {
	remote := newFakeLLM(t, sseText("bonjour"))
	e := newEnvExtra(t, remote, "eu", `
[[providers]]
id = "gpu"
base_url = "http://gpu.invalid:11434/v1"
model = "llama"
context_window = 64000
jurisdiction = "local"

[routing]
local = "gpu"
`)
	code, out, errb := e.run("--lang", "en", "-p", "salut")
	if code != 0 || !strings.Contains(out, "bonjour") ||
		!strings.Contains(errb, `routing: local provider "gpu" uses non-loopback host "gpu.invalid"`) {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errb)
	}
	_, _, errb = e.run("-p", "salut")
	if !strings.Contains(errb, "routage : le fournisseur local « gpu » utilise l'hôte non local « gpu.invalid »") {
		t.Fatalf("fr : %q", errb)
	}
}

func TestRoutingPolicyNotice(t *testing.T) {
	cfg := config.Config{Policy: &orgpolicy.Policy{ID: "acme"}, PolicyRoutingLocal: "ollama"}
	got := strings.Join(policyNotices(i18n.EN, cfg), "\n")
	if !strings.Contains(got, "policy acme: routing.local imposed (ollama)") {
		t.Fatalf("en : %q", got)
	}
	got = strings.Join(policyNotices(i18n.FR, cfg), "\n")
	if !strings.Contains(got, "politique acme : routing.local imposé (ollama)") {
		t.Fatalf("fr : %q", got)
	}
}

func TestBuildRouter(t *testing.T) {
	remote := config.Provider{ID: "mistral", BaseURL: "https://api.mistral.ai/v1", APIKeyEnv: "K", Model: "m", Jurisdiction: "eu"}
	loc := config.Provider{ID: "ollama", BaseURL: "http://localhost:11434/v1", Model: "llama", ContextWindow: 8000, Jurisdiction: "local"}
	nokey := config.Provider{ID: "scaleway", BaseURL: "https://api.scaleway.ai/v1", APIKeyEnv: "SCW", Model: "m", Jurisdiction: "eu"}
	cfg := config.Config{Providers: []config.Provider{remote, loc, nokey}}
	d := routerDeps{lang: i18n.EN, keys: map[string]string{"mistral": "k"}}
	// Sans [routing], courant distant : aucun routeur.
	r, info, n, err := buildRouter(cfg, remote, nil, d)
	if r != nil || info != nil || n != nil || err != nil {
		t.Fatalf("distant : %v %v %v %v", r, info, n, err)
	}
	// Sans [routing], courant local : il est le fournisseur local.
	lp := provider.NewOpenAICompat(providerInfo(loc), "")
	r, info, _, err = buildRouter(cfg, loc, lp, d)
	if err != nil || info != nil || routingLocal(r) != "ollama" || len(r.Candidates(false)) != 1 {
		t.Fatalf("local : %v %v %v", r, info, err)
	}
	if c := r.Candidates(true); len(c) != 1 || c[0].Window != 8000 {
		t.Fatalf("candidat local : %+v", c)
	}
	// Repli sans clé : ignoré avec un avis, sans refus du démarrage.
	cfg.Routing = config.Routing{Local: "ollama", Fallback: []string{"mistral", "scaleway"}}
	mp := provider.NewOpenAICompat(providerInfo(remote), "k")
	r, info, n, err = buildRouter(cfg, remote, mp, d)
	if err != nil || routingLocal(r) != "ollama" || info.Local != "ollama" || len(info.Fallback) != 0 {
		t.Fatalf("routage : %v %+v %v", r, info, err)
	}
	if got := strings.Join(n, "\n"); !strings.Contains(got, `routing: fallback provider "scaleway" skipped (`) {
		t.Fatalf("avis : %q", got)
	}
	if ids := r.Candidates(false); len(ids) != 1 || ids[0].ID != "mistral" {
		t.Fatalf("candidats : %+v", ids)
	}
}

// Lignes « config check » de la classification et du routage sans règle ni
// [routing].
const (
	routingCheckEN = "classification: 0 paths, 0 markers\nrouting: none\n"
	routingCheckFR = "classification : 0 chemins, 0 marquages\nroutage : aucun\n"
)

// proxySpy remplace newProvider et la détection Ollama : il retient, par
// fournisseur, si le fournisseur ignore les proxys d'environnement et si la
// détection a reçu un client sans proxy.
type proxySpy struct {
	mu       sync.Mutex
	noProxy  map[string]bool
	detected map[string]bool
}

func spyProxies(t *testing.T) *proxySpy {
	t.Helper()
	s := &proxySpy{noProxy: map[string]bool{}, detected: map[string]bool{}}
	oldNew, oldDetect := newProvider, detectFuncs["ollama"]
	t.Cleanup(func() { newProvider, detectFuncs["ollama"] = oldNew, oldDetect })
	newProvider = func(info llm.ProviderInfo, key string, noProxy bool) provider.Provider {
		s.mu.Lock()
		s.noProxy[info.ID] = noProxy
		s.mu.Unlock()
		return oldNew(info, key, noProxy)
	}
	detectFuncs["ollama"] = func(_ context.Context, c *http.Client, base, _ string) (provider.Detected, error) {
		tr, ok := (*http.Transport)(nil), false
		if c != nil {
			tr, ok = c.Transport.(*http.Transport)
		}
		s.mu.Lock()
		s.detected[base] = ok && tr.Proxy == nil
		s.mu.Unlock()
		return provider.Detected{Model: "llama", ContextWindow: 64000}, nil
	}
	return s
}

// Ruling R14 : le fournisseur local du routage n'emprunte jamais un proxy
// d'environnement, quel que soit le profil (profil par défaut ici), même
// sur un hôte non local admis par R5.
func TestRoutingLocalNeverProxied(t *testing.T) {
	s := spyProxies(t)
	remote := newFakeLLM(t, sseText("bonjour"))
	e := newEnvExtra(t, remote, "eu", `
[[providers]]
id = "gpu"
base_url = "http://gpu.lan:11434/v1"
detect = "ollama"
jurisdiction = "local"

[routing]
local = "gpu"
`)
	code, _, errb := e.run("--lang", "en", "-p", "salut")
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	if !s.noProxy["gpu"] || !s.detected["http://gpu.lan:11434/v1"] {
		t.Fatalf("local par proxy : fournisseur sans proxy=%v, détection sans proxy=%v", s.noProxy["gpu"], s.detected["http://gpu.lan:11434/v1"])
	}
	// Le fournisseur distant garde le comportement d'avant.
	if s.noProxy["test"] {
		t.Fatal("fournisseur distant sans proxy")
	}
}

// Un fournisseur « local » en repli n'emprunte pas le proxy ; le routeur
// dédié de la compaction le réutilise tel quel et signale son hôte non
// local.
func TestRoutingLocalFallbackNeverProxied(t *testing.T) {
	s := spyProxies(t)
	remote := newFakeLLM(t, sseText("bonjour"))
	e := newEnvExtra(t, remote, "eu", `
[[providers]]
id = "gpu"
base_url = "http://gpu.lan:11434/v1"
detect = "ollama"
jurisdiction = "local"

[routing]
fallback = ["gpu"]
compaction = "gpu"
`)
	code, _, errb := e.run("--lang", "en", "-p", "salut")
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	if !s.noProxy["gpu"] || !s.detected["http://gpu.lan:11434/v1"] {
		t.Fatalf("repli local par proxy : fournisseur sans proxy=%v, détection sans proxy=%v", s.noProxy["gpu"], s.detected["http://gpu.lan:11434/v1"])
	}
	if !strings.Contains(errb, "gpu.lan") {
		t.Fatalf("avis d'hôte non local absent : %q", errb)
	}
}

// Review Focus 3 + R14 : un fournisseur courant local, fournisseur local du
// routeur, est construit et détecté sans proxy.
func TestRoutingCurrentLocalNeverProxied(t *testing.T) {
	s := spyProxies(t)
	local := newFakeLLM(t, sseText("bonjour local"))
	e := newEnvExtra(t, nil, "eu", fmt.Sprintf(`
[[providers]]
id = "ollama"
base_url = %q
jurisdiction = "local"
`, local.srv.URL))
	code, out, errb := e.run("--lang", "en", "--provider", "ollama", "-p", "salut")
	if code != 0 || !strings.Contains(out, "bonjour local") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errb)
	}
	if !s.noProxy["ollama"] || !s.detected[local.srv.URL] {
		t.Fatalf("courant local par proxy : %v %v", s.noProxy, s.detected)
	}
}

// Ruling R15 : SessionStarted.routing.local est le fournisseur local
// effectif, vide quand il est indisponible.
func TestRoutingLocalUnavailable(t *testing.T) {
	remote := newFakeLLM(t, sseCall("read_file", `{"path":"rh/paie.csv"}`), sseText("refus compris"))
	e := newEnvExtra(t, remote, "eu", `
[[providers]]
id = "gpu"
base_url = "http://127.0.0.1:1/v1"
detect = "ollama"
jurisdiction = "local"

[classification]
paths = ["rh/**"]

[routing]
local = "gpu"
`)
	e.writeFile(filepath.Join(e.wd, "rh", "paie.csv"), paie)
	code, _, errb := e.run("--lang", "en", "-p", "lis rh/paie.csv")
	if code != 3 || !strings.Contains(errb, `routing: local provider "gpu" unavailable (`) {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	if strings.Contains(strings.Join(remote.requests(), ""), "48213") {
		t.Fatal("contenu classé envoyé au distant")
	}
	st := payloadsOf[event.SessionStartedPayload](t, e.journalEvents(), event.SessionStarted)
	if len(st) != 1 || st[0].Routing == nil || st[0].Routing.Local != "" {
		t.Fatalf("SessionStarted.routing : %+v", st)
	}
}

// countDetections remplace la détection Ollama par une détection fixe et
// compte ses appels par URL de base.
func countDetections(t *testing.T) func(base string) int {
	t.Helper()
	var mu sync.Mutex
	n := map[string]int{}
	old := detectFuncs["ollama"]
	t.Cleanup(func() { detectFuncs["ollama"] = old })
	detectFuncs["ollama"] = func(_ context.Context, _ *http.Client, base, _ string) (provider.Detected, error) {
		mu.Lock()
		n[base]++
		mu.Unlock()
		return provider.Detected{Model: "llama", ContextWindow: 64000}, nil
	}
	return func(base string) int {
		mu.Lock()
		defer mu.Unlock()
		return n[base]
	}
}

// Un fournisseur dédié « local » joint par un hôte non local est signalé ;
// les avis de sa détection sont affichés.
func TestDedicatedLocalNonLoopbackNotice(t *testing.T) {
	count := countDetections(t)
	remote := newFakeLLM(t, sseText("bonjour"))
	e := newEnvExtra(t, remote, "eu", `
[[providers]]
id = "gpu"
name = "GPU"
base_url = "http://gpu.invalid:11434/v1"
detect = "ollama"
jurisdiction = "local"

[routing]
compaction = "gpu"
`)
	code, _, errb := e.run("--lang", "en", "-p", "salut")
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	for _, want := range []string{
		`routing: local provider "gpu" uses non-loopback host "gpu.invalid"`,
		"GPU: model llama, context window 64000 tokens (read from the server)",
	} {
		if strings.Count(errb, want) != 1 {
			t.Fatalf("avis %q absent ou répété : %q", want, errb)
		}
	}
	if n := count("http://gpu.invalid:11434/v1"); n != 1 {
		t.Fatalf("%d détections", n)
	}
}

// Le fournisseur local du routage, réutilisé pour la compaction et les
// sous-agents, n'est détecté et signalé qu'une fois.
func TestDedicatedReusesLocalCandidate(t *testing.T) {
	count := countDetections(t)
	remote := newFakeLLM(t, sseText("bonjour"))
	e := newEnvExtra(t, remote, "eu", `
[[providers]]
id = "gpu"
base_url = "http://gpu.invalid:11434/v1"
detect = "ollama"
jurisdiction = "local"

[routing]
local = "gpu"
compaction = "gpu"
subagents = "gpu"
`)
	code, _, errb := e.run("--lang", "en", "-p", "salut")
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	if n := strings.Count(errb, `uses non-loopback host "gpu.invalid"`); n != 1 {
		t.Fatalf("avis non local x%d : %q", n, errb)
	}
	if n := count("http://gpu.invalid:11434/v1"); n != 1 {
		t.Fatalf("%d détections", n)
	}
}
