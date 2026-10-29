package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/exp/teatest"

	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/orgpolicy"
	"github.com/tenon-code-eu/tenon/internal/orgpolicy/orgpolicytest"
)

// withPolicy signe policyTOML dans un répertoire temporaire, le désigne par
// TENON_POLICY_DIR et renvoie le répertoire et l'identifiant de clé.
func (e *env) withPolicy(policyTOML string) (dir, keyID string) {
	e.t.Helper()
	dir = e.t.TempDir()
	keyID = orgpolicytest.Sign(e.t, dir, policyTOML)
	e.vars["TENON_POLICY_DIR"] = dir
	return dir, keyID
}

const policyHead = "id = \"acme-dev\"\nversion = 3\n"

func TestPolicyHostRefusedE2E(t *testing.T) {
	f := newFakeLLM(t, sseText("salut"))
	e := newEnv(t, f, "eu")
	e.withPolicy(policyHead + "[providers]\nallow_hosts = [\"api.mistral.ai\"]\n")
	code, _, errb := e.run("--lang", "en", "-p", "salut")
	if code != 1 || !strings.Contains(errb, `provider "test" (host "127.0.0.1") not allowed by policy acme-dev`) {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	if n := len(f.requests()); n != 0 {
		t.Fatalf("%d requête(s) envoyée(s)", n)
	}
	code, _, errb = e.run("-p", "salut")
	if code != 1 || !strings.Contains(errb, "fournisseur « test » (hôte « 127.0.0.1 ») non autorisé par la politique acme-dev") {
		t.Fatalf("fr : code=%d stderr=%q", code, errb)
	}
	if ents, _ := e.sessionFiles(); len(ents) != 0 {
		t.Fatalf("session créée : %v", ents)
	}
}

func TestPolicyAllowLocalE2E(t *testing.T) {
	f := newFakeLLM(t, sseText("bonjour"))
	e := newEnv(t, f, "eu")
	_, key := e.withPolicy(policyHead + "[providers]\nallow_local = true\n")
	code, out, errb := e.run("-p", "salut")
	if code != 0 || !strings.Contains(out, "bonjour") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errb)
	}
	ss := sessionStarted(t, e.sessionContent())
	if len(ss) != 1 || ss[0].Policy == nil || ss[0].Policy.ID != "acme-dev" || ss[0].Policy.Version != 3 || ss[0].Policy.KeyID != key {
		t.Fatalf("SessionStarted : %+v", ss)
	}
}

func TestPolicyBadSignatureE2E(t *testing.T) {
	f := newFakeLLM(t, sseText("salut"))
	e := newEnv(t, f, "eu")
	dir, _ := e.withPolicy(policyHead)
	// Modifiée après signature.
	e.writeFile(filepath.Join(dir, "policy.toml"), policyHead+"profile = \"\"\n")
	code, _, errb := e.run("--lang", "en", "-p", "salut")
	if code != 1 || !strings.Contains(errb, "tenon: policy: "+filepath.Join(dir, "policy.toml")+": ") {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	if n := len(f.requests()); n != 0 {
		t.Fatalf("%d requête(s) envoyée(s)", n)
	}
	code, _, errb = e.run("-p", "salut")
	if code != 1 || !strings.Contains(errb, "tenon : politique : "+filepath.Join(dir, "policy.toml")) {
		t.Fatalf("fr : code=%d stderr=%q", code, errb)
	}
	// Même refus pour config check.
	code, _, errb = e.runCheck("--lang", "en")
	if code != 1 || !strings.Contains(errb, "tenon: policy: ") {
		t.Fatalf("config check : code=%d stderr=%q", code, errb)
	}
}

func TestPolicyMCPSkippedE2E(t *testing.T) {
	f := newFakeLLM(t, sseText("fini"))
	e := newEnvExtra(t, f, "eu", stdioDecl(t))
	log := stdioMCP(t, e, "")
	e.withPolicy(policyHead + "[mcp]\nmode = \"off\"\n")
	code, out, errb := e.run("--lang", "en", "-p", "salut")
	if code != 0 || !strings.Contains(out, "fini") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errb)
	}
	if !strings.Contains(errb, `policy acme-dev: MCP server "test" skipped (not allowed)`) {
		t.Fatalf("avis absent : %q", errb)
	}
	if _, pids := mcpLog(t, log); len(pids) != 0 {
		t.Fatalf("serveur lancé : pids %v", pids)
	}
	if !strings.Contains(e.sessionContent(), `"server":"test"`) {
		t.Fatal("McpServerDown absent de la session")
	}
	_, _, errb = e.run("-p", "salut")
	if !strings.Contains(errb, "politique acme-dev : serveur MCP « test » ignoré (non autorisé)") {
		t.Fatalf("fr : %q", errb)
	}
}

func TestPolicyProfileRaisedE2E(t *testing.T) {
	sandboxReady(t)
	f := newFakeLLM(t, sseText("bonjour local"))
	e := newEnv(t, f, "local")
	e.editGlobal("profile = \"eu-only\"\n")
	e.withPolicy(policyHead + "profile = \"strict-local\"\n")
	code, _, errb := e.run("--lang", "en", "-p", "salut")
	if code != 0 || !strings.Contains(errb, `profile "eu-only" from config is weaker than policy profile "strict-local" (policy acme-dev): raised`) {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	if ss := sessionStarted(t, e.sessionContent()); len(ss) != 1 || ss[0].Profile != "strict-local" {
		t.Fatalf("SessionStarted : %+v", ss)
	}
	_, _, errb = e.run("-p", "salut")
	if !strings.Contains(errb, "profil « eu-only » de la configuration plus souple que le profil « strict-local » de la politique acme-dev : relevé") {
		t.Fatalf("fr : %q", errb)
	}

	// Fournisseur UE : refusé par strict-local, imposé par la politique.
	f2 := newFakeLLM(t, sseText("salut"))
	e2 := newEnv(t, f2, "eu")
	e2.withPolicy(policyHead + "profile = \"strict-local\"\n")
	code, _, errb = e2.run("--lang", "en", "-p", "salut")
	if code != 1 || !strings.Contains(errb, `profile strict-local: provider "test" (eu) not allowed`) {
		t.Fatalf("eu : code=%d stderr=%q", code, errb)
	}
	if n := len(f2.requests()); n != 0 {
		t.Fatalf("%d requête(s) envoyée(s)", n)
	}
}

// Une session ouverte sans politique n'échappe pas au refus d'hôte à la reprise.
func TestPolicyResumeE2E(t *testing.T) {
	f := newFakeLLM(t, sseText("enchanté"), sseText("re"))
	e := newEnv(t, f, "eu")
	if code, out, errb := e.run("-p", "je m'appelle Ada"); code != 0 {
		t.Fatalf("run 1 : code=%d stdout=%q stderr=%q", code, out, errb)
	}
	ents, err := e.sessionFiles()
	if err != nil || len(ents) != 1 {
		t.Fatalf("sessions : %v %v", ents, err)
	}
	id := strings.TrimSuffix(ents[0].Name(), ".jsonl")
	e.withPolicy(policyHead + "[providers]\nallow_hosts = [\"api.mistral.ai\"]\n")
	code, _, errb := e.run("--lang", "en", "--resume", id, "-p", "suite")
	if code != 1 || !strings.Contains(errb, `not allowed by policy acme-dev`) || len(f.requests()) != 1 {
		t.Fatalf("run 2 : code=%d requêtes=%d stderr=%q", code, len(f.requests()), errb)
	}
}

// Un fournisseur défini par la politique l'emporte sur sa redéfinition dans la
// configuration : la requête atteint l'URL de la politique.
func TestPolicyRedefinedProviderE2E(t *testing.T) {
	f := newFakeLLM(t, sseText("depuis interne"))
	e := newEnvExtra(t, f, "eu", `
[[providers]]
id = "interne"
base_url = "https://evil.example/v1"
api_key_env = "TEST_KEY"
model = "evil-model"
jurisdiction = "eu"
`)
	e.withPolicy(policyHead + `[providers]
allow_local = true

[[providers.define]]
id = "interne"
name = "Interne"
base_url = "` + f.srv.URL + `"
api_key_env = "TEST_KEY"
model = "interne-model"
country = "FR"
jurisdiction = "eu"
`)
	code, out, errb := e.run("--lang", "en", "--provider", "interne", "-p", "salut")
	if code != 0 || !strings.Contains(out, "depuis interne") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errb)
	}
	if !strings.Contains(errb, `policy acme-dev: provider "interne" from config ignored (defined by policy)`) {
		t.Fatalf("avis absent : %q", errb)
	}
	if reqs := f.requests(); len(reqs) != 1 || !strings.Contains(reqs[0], "interne-model") {
		t.Fatalf("requêtes : %q", reqs)
	}
}

func TestPolicyAuditImposedE2E(t *testing.T) {
	f := newFakeLLM(t, sseText("salut"))
	e := newEnvExtra(t, f, "eu", "\n[audit]\nlevel = \"metadata\"\n")
	e.withPolicy(policyHead + "[audit]\nlevel = \"full\"\n")
	code, _, errb := e.run("--lang", "en", "-p", "salut")
	if code != 0 || !strings.Contains(errb, `policy acme-dev: audit level "full" imposed (config: "metadata")`) {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
}

func TestConfigCheckPolicyE2E(t *testing.T) {
	e := newEnv(t, nil, "eu")
	_, key := e.withPolicy(policyHead)
	code, out, errb := e.runCheck("--lang", "en")
	if code != 0 || out != "policy: acme-dev v3 (key "+key+")\n"+routingCheckEN+sandboxCheckEN+craCheck+"OK\n" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errb)
	}
	if _, out, _ = e.runCheck(); out != "politique : acme-dev v3 (clé "+key+")\n"+routingCheckFR+sandboxCheckFR+craCheck+"OK\n" {
		t.Fatalf("fr : %q", out)
	}
	// Ligne de politique avant celle du profil, puis contrôle d'hôte.
	_, key = e.withPolicy(policyHead + "profile = \"eu-only\"\n[providers]\nallow_hosts = [\"api.mistral.ai\"]\n")
	code, out, errb = e.runCheck("--lang", "en")
	if code != 1 || out != "policy: acme-dev v3 (key "+key+")\nprofile: eu-only\n" ||
		!strings.Contains(errb, `provider "test" (host "127.0.0.1") not allowed by policy acme-dev`) {
		t.Fatalf("hôte : code=%d stdout=%q stderr=%q", code, out, errb)
	}
}

func TestNoPolicyUnchangedE2E(t *testing.T) {
	f := newFakeLLM(t, sseText("salut"))
	e := newEnv(t, f, "eu")
	if _, err := os.Stat(filepath.Join(orgpolicy.SystemDir(), "policy.toml")); err == nil {
		t.Skip("politique système présente sur cette machine")
	}
	if code, out, errb := e.run("-p", "salut"); code != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errb)
	}
	for _, ev := range decodeEvents(t, e.sessionContent()) {
		if ev.Type == event.SessionStarted && strings.Contains(string(ev.Payload), `"policy"`) {
			t.Fatalf("champ policy présent sans politique : %s", ev.Payload)
		}
	}
}

// TUI : la ligne de la politique est transmise à la TUI (panneau latéral).
func TestPolicyTUIOptionE2E(t *testing.T) {
	f := newFakeLLM(t, sseText("salut"))
	e := newEnv(t, f, "eu")
	_, key := e.withPolicy(policyHead)
	r := e.runTUI(func(tm *teatest.TestModel) {
		waitText(t, tm, "agent de code souverain")
		quitTUI(t, tm)
	})
	if r.code != 0 || r.opts.Policy != "politique : acme-dev v3 (clé "+key+")" {
		t.Fatalf("code=%d Options.Policy=%q stderr=%q", r.code, r.opts.Policy, r.stderr)
	}
}

func TestPolicyCheckValidE2E(t *testing.T) {
	e := newEnv(t, nil, "eu")
	dir := t.TempDir()
	key := orgpolicytest.Sign(t, dir, policyHead+"profile = \"eu-only\"\n[providers]\nallow_hosts = [\"api.mistral.ai\", \"*.x.eu\"]\n[audit]\nlevel = \"full\"\n[mcp]\nmode = \"allowlist\"\n")
	code, out, errb := e.run("--lang", "en", "policy", "check", dir)
	want := "policy: acme-dev v3 (key " + key + ")\nprofile: eu-only\nproviders: 2 allowed hosts, local no, 0 defined\nrules: 0\nredaction: 0\naudit: full\nmcp: allowlist\n"
	if code != 0 || out != want || errb != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errb)
	}
	// Politique minimale, en français.
	dir = t.TempDir()
	key = orgpolicytest.Sign(t, dir, policyHead)
	code, out, _ = e.run("policy", "check", dir)
	want = "politique : acme-dev v3 (clé " + key + ")\nprofil : aucun\nfournisseurs : pas de liste blanche\nrègles : 0\nmasquage : 0\naudit : non imposé\nmcp : any\n"
	if code != 0 || out != want {
		t.Fatalf("fr : code=%d stdout=%q", code, out)
	}
	code, out, _ = e.run("--lang", "en", "policy", "check", dir)
	if code != 0 || !strings.Contains(out, "profile: none\nproviders: no allowlist\n") || !strings.Contains(out, "audit: not imposed\nmcp: any\n") {
		t.Fatalf("en minimal : %q", out)
	}
}

func TestPolicyCheckInvalidE2E(t *testing.T) {
	e := newEnv(t, nil, "eu")
	dir := t.TempDir()
	orgpolicytest.Sign(t, dir, policyHead)
	e.writeFile(filepath.Join(dir, "policy.toml"), policyHead+"profile = \"\"\n")
	code, out, errb := e.run("--lang", "en", "policy", "check", dir)
	if code != 1 || out != "" || !strings.Contains(errb, "tenon: policy: "+filepath.Join(dir, "policy.toml")+": ") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errb)
	}
}

func TestPolicyCheckAbsentE2E(t *testing.T) {
	e := newEnv(t, nil, "eu")
	dir := t.TempDir()
	code, _, errb := e.run("--lang", "en", "policy", "check", dir)
	if code != 1 || !strings.Contains(errb, "no policy in "+dir) {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	code, _, errb = e.run("policy", "check", dir)
	if code != 1 || !strings.Contains(errb, "aucune politique dans "+dir) {
		t.Fatalf("fr : code=%d stderr=%q", code, errb)
	}
}

func TestPolicyCheckArgsE2E(t *testing.T) {
	e := newEnv(t, nil, "eu")
	code, out, errb := e.run("--lang", "en", "policy", "check", "a", "b")
	if code != 1 || out != "" || !strings.Contains(errb, "usage: tenon") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errb)
	}
}

// La politique charge avant la configuration : avec une configuration globale
// invalide et une politique falsifiée, l'erreur rapportée est celle de la
// politique.
func TestPolicyLoadedBeforeConfigE2E(t *testing.T) {
	f := newFakeLLM(t, sseText("salut"))
	e := newEnv(t, f, "eu")
	dir, _ := e.withPolicy(policyHead)
	if err := os.WriteFile(filepath.Join(dir, "policy.toml"), []byte(policyHead+"# falsifié\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.writeFile(e.globalConfig(), "ceci n'est pas = du toml [[[")
	code, _, errb := e.run("--lang", "en", "-p", "salut")
	if code != 1 || !strings.Contains(errb, "signature: invalid") || strings.Contains(errb, "config.toml") {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	if n := len(f.requests()); n != 0 {
		t.Fatalf("%d requête(s) envoyée(s)", n)
	}
}

// Une politique qui admet l'hôte n'empêche pas la reprise, et la session
// reprise porte la référence de la politique.
func TestPolicyResumeAllowedE2E(t *testing.T) {
	f := newFakeLLM(t, sseText("enchanté"), sseText("re"))
	e := newEnv(t, f, "eu")
	if code, out, errb := e.run("-p", "je m'appelle Ada"); code != 0 {
		t.Fatalf("run 1 : code=%d stdout=%q stderr=%q", code, out, errb)
	}
	ents, err := e.sessionFiles()
	if err != nil || len(ents) != 1 {
		t.Fatalf("sessions : %v %v", ents, err)
	}
	id := strings.TrimSuffix(ents[0].Name(), ".jsonl")
	e.withPolicy(policyHead + "[providers]\nallow_local = true\n")
	code, out, errb := e.run("--lang", "en", "--resume", id, "-p", "suite")
	if code != 0 {
		t.Fatalf("run 2 : code=%d stdout=%q stderr=%q", code, out, errb)
	}
	ss := sessionStarted(t, e.sessionContent())
	last := ss[len(ss)-1]
	if !last.Resumed || last.Policy == nil || last.Policy.ID != "acme-dev" {
		t.Fatalf("SessionStarted : %+v", ss)
	}
}
