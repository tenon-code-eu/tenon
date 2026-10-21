package main

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tenon-code-eu/tenon/internal/config"
	"github.com/tenon-code-eu/tenon/internal/i18n"
	"github.com/tenon-code-eu/tenon/internal/profile"
)

// qualified déclare la qualification secnumcloud sur le fournisseur de test
// (paire de remplacement pour editGlobal).
var qualified = []string{`jurisdiction = "eu"`, "jurisdiction = \"eu\"\nqualifications = [\"secnumcloud\"]"}

// projectConfig écrit la configuration projet de l'environnement.
func (e *env) projectConfig(content string) {
	e.t.Helper()
	e.writeFile(filepath.Join(e.wd, ".tenon", "config.toml"), content)
}

func TestAllowlistGlobalRefusedE2E(t *testing.T) {
	f := newFakeLLM(t, sseText("salut"))
	e := newEnvExtra(t, f, "eu", "\n[provider_allowlist]\nhosts = [\"api.mistral.ai\"]\n")
	code, _, errb := e.run("--lang", "en", "-p", "salut")
	if code != 1 || !strings.Contains(errb, `provider "test" (host "127.0.0.1") not in provider_allowlist (global)`) {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	if n := len(f.requests()); n != 0 {
		t.Fatalf("%d requête(s) envoyée(s)", n)
	}
	code, _, errb = e.run("-p", "salut")
	if code != 1 || !strings.Contains(errb, "fournisseur « test » (hôte « 127.0.0.1 ») absent de provider_allowlist (globale)") {
		t.Fatalf("fr : code=%d stderr=%q", code, errb)
	}
	if ents, _ := e.sessionFiles(); len(ents) != 0 {
		t.Fatalf("session créée : %v", ents)
	}
}

func TestAllowlistLocalE2E(t *testing.T) {
	f := newFakeLLM(t, sseText("bonjour"))
	e := newEnvExtra(t, f, "eu", "\n[provider_allowlist]\nlocal = true\n")
	code, out, errb := e.run("-p", "salut")
	if code != 0 || !strings.Contains(out, "bonjour") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errb)
	}
}

// Liste projet seule, sans liste globale : elle s'applique quand même.
func TestAllowlistProjectOnlyE2E(t *testing.T) {
	f := newFakeLLM(t, sseText("salut"))
	e := newEnv(t, f, "eu")
	e.projectConfig("[provider_allowlist]\nhosts = [\"api.mistral.ai\"]\n")
	code, _, errb := e.run("--lang", "en", "-p", "salut")
	if code != 1 || !strings.Contains(errb, `provider "test" (host "127.0.0.1") not in provider_allowlist (project)`) {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	if n := len(f.requests()); n != 0 {
		t.Fatalf("%d requête(s) envoyée(s)", n)
	}
	_, _, errb = e.run("-p", "salut")
	if !strings.Contains(errb, "absent de provider_allowlist (projet)") {
		t.Fatalf("fr : %q", errb)
	}
}

// La liste projet restreint une liste globale qui accepte l'hôte.
func TestAllowlistProjectNarrowsE2E(t *testing.T) {
	f := newFakeLLM(t, sseText("salut"))
	e := newEnvExtra(t, f, "eu", "\n[provider_allowlist]\nlocal = true\n")
	e.projectConfig("[provider_allowlist]\nhosts = [\"api.mistral.ai\"]\n")
	code, _, errb := e.run("--lang", "en", "-p", "salut")
	if code != 1 || !strings.Contains(errb, `provider "test" (host "127.0.0.1") not in provider_allowlist (project)`) {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	if n := len(f.requests()); n != 0 {
		t.Fatalf("%d requête(s) envoyée(s)", n)
	}
}

// --provider désigne un autre fournisseur : son hôte est contrôlé aussi.
func TestAllowlistProviderFlagE2E(t *testing.T) {
	f := newFakeLLM(t, sseText("bonjour"))
	e := newEnvExtra(t, f, "eu", `
[[providers]]
id = "autre"
base_url = "https://api.example.eu/v1"
api_key_env = "TEST_KEY"
model = "autre-model"
jurisdiction = "eu"

[provider_allowlist]
hosts = ["127.0.0.1"]
`)
	if code, out, errb := e.run("-p", "salut"); code != 0 || !strings.Contains(out, "bonjour") {
		t.Fatalf("défaut : code=%d stdout=%q stderr=%q", code, out, errb)
	}
	code, _, errb := e.run("--lang", "en", "--provider", "autre", "-p", "salut")
	if code != 1 || !strings.Contains(errb, `provider "autre" (host "api.example.eu") not in provider_allowlist (global)`) {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	if n := len(f.requests()); n != 1 {
		t.Fatalf("%d requête(s) envoyée(s)", n)
	}
}

func TestSecNumCloudUnqualifiedE2E(t *testing.T) {
	f := newFakeLLM(t, sseText("salut"))
	e := newEnv(t, f, "eu")
	e.editGlobal("profile = \"secnumcloud\"\n")
	code, _, errb := e.run("--lang", "en", "-p", "salut")
	if code != 1 || !strings.Contains(errb, `profile secnumcloud: provider "test" is not declared secnumcloud-qualified`) {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	if n := len(f.requests()); n != 0 {
		t.Fatalf("%d requête(s) envoyée(s)", n)
	}
	code, _, errb = e.run("-p", "salut")
	if code != 1 || !strings.Contains(errb, "profil secnumcloud : fournisseur « test » non déclaré qualifié secnumcloud") {
		t.Fatalf("fr : code=%d stderr=%q", code, errb)
	}
}

func TestSecNumCloudQualifiedE2E(t *testing.T) {
	f := newFakeLLM(t, sseText("bonjour"))
	e := newEnv(t, f, "eu")
	e.editGlobal("profile = \"secnumcloud\"\n", qualified...)
	code, out, errb := e.run("-p", "salut")
	if code != 0 || !strings.Contains(out, "bonjour") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errb)
	}
	ss := sessionStarted(t, e.sessionContent())
	if len(ss) != 1 || !slices.Equal(ss[0].Provider.Qualifications, []string{"secnumcloud"}) {
		t.Fatalf("SessionStarted : %+v", ss)
	}
}

// Un fournisseur local sur un hôte local est admis sans qualification.
func TestSecNumCloudLocalE2E(t *testing.T) {
	f := newFakeLLM(t, sseText("bonjour"))
	e := newEnv(t, f, "local")
	e.editGlobal("profile = \"secnumcloud\"\n")
	if code, out, errb := e.run("-p", "salut"); code != 0 || !strings.Contains(out, "bonjour") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errb)
	}
}

// Un fournisseur local sur un hôte distant est refusé (message d'hôte).
func TestSecNumCloudLocalRemoteHostE2E(t *testing.T) {
	e := newEnv(t, nil, "local")
	e.editGlobal("profile = \"secnumcloud\"\n", "http://127.0.0.1:1", "https://llm.example.eu/v1")
	code, _, errb := e.run("--lang", "en", "-p", "salut")
	if code != 1 || !strings.Contains(errb, `profile secnumcloud: provider "test" url host "llm.example.eu" is not a local address`) {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	_, _, errb = e.run("-p", "salut")
	if !strings.Contains(errb, "profil secnumcloud : l'hôte « llm.example.eu » de l'URL du fournisseur « test » n'est pas une adresse locale") {
		t.Fatalf("fr : %q", errb)
	}
}

// Sous politique, une qualification déclarée par l'utilisateur est ignorée.
func TestSecNumCloudPolicySelfQualifiedE2E(t *testing.T) {
	f := newFakeLLM(t, sseText("salut"))
	e := newEnv(t, f, "eu")
	e.editGlobal("", qualified...)
	e.withPolicy(policyHead + "profile = \"secnumcloud\"\n[providers]\nallow_local = true\n")
	code, _, errb := e.run("--lang", "en", "-p", "salut")
	if code != 1 || !strings.Contains(errb, `profile secnumcloud: provider "test" is not declared secnumcloud-qualified`) {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	if !strings.Contains(errb, "policy acme-dev: qualifications declared in config ignored (test)") {
		t.Fatalf("avis absent : %q", errb)
	}
	if n := len(f.requests()); n != 0 {
		t.Fatalf("%d requête(s) envoyée(s)", n)
	}
	_, _, errb = e.run("-p", "salut")
	if !strings.Contains(errb, "politique acme-dev : qualifications déclarées dans la configuration ignorées (test)") {
		t.Fatalf("fr : %q", errb)
	}
}

// Un fournisseur défini et qualifié par la politique est admis.
func TestSecNumCloudPolicyDefinedE2E(t *testing.T) {
	f := newFakeLLM(t, sseText("souverain"))
	e := newEnv(t, f, "eu")
	e.editGlobal("", `default_provider = "test"`, `default_provider = "souverain"`)
	e.withPolicy(policyHead + `profile = "secnumcloud"
[providers]
allow_local = true

[[providers.define]]
id = "souverain"
name = "Souverain"
base_url = "` + f.srv.URL + `"
api_key_env = "TEST_KEY"
model = "souverain-model"
country = "FR"
jurisdiction = "eu"
qualifications = ["secnumcloud"]
`)
	code, out, errb := e.run("-p", "salut")
	if code != 0 || !strings.Contains(out, "souverain") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errb)
	}
	// La langue de la configuration (fr) reste appliquée.
	if !strings.Contains(errb, "juridiction") {
		t.Fatalf("langue : %q", errb)
	}
	ss := sessionStarted(t, e.sessionContent())
	if len(ss) != 1 || ss[0].Provider.ID != "souverain" || !slices.Equal(ss[0].Provider.Qualifications, []string{"secnumcloud"}) {
		t.Fatalf("SessionStarted : %+v", ss)
	}
}

// MCP sous secnumcloud : un serveur http eu non qualifié est ignoré.
func TestSecNumCloudMCPUnqualifiedE2E(t *testing.T) {
	f := newFakeLLM(t, sseText("bonjour"))
	e := newEnvExtra(t, f, "local", `
[[mcp.servers]]
id = "distant"
transport = "http"
url = "https://mcp.example.eu/mcp"
jurisdiction = "eu"
`)
	e.editGlobal("profile = \"secnumcloud\"\n")
	code, _, errb := e.run("--lang", "en", "-p", "salut")
	if code != 0 || !strings.Contains(errb, `profile secnumcloud: MCP server "distant" (not declared secnumcloud-qualified) skipped`) {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	_, _, errb = e.run("-p", "salut")
	if !strings.Contains(errb, "profil secnumcloud : serveur MCP « distant » (non déclaré qualifié secnumcloud) ignoré") {
		t.Fatalf("fr : %q", errb)
	}
}

func TestConfigCheckAllowlistE2E(t *testing.T) {
	e := newEnvExtra(t, nil, "eu", "\n[provider_allowlist]\nhosts = [\"127.0.0.1\"]\nlocal = true\n")
	code, out, errb := e.run("--lang", "en", "config", "check")
	if code != 0 || out != "provider_allowlist: global 1 hosts + local, project - hosts\nOK\n" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errb)
	}
	if _, out, _ = e.run("config", "check"); out != "provider_allowlist : globale 1 hôtes + local, projet - hôtes\nOK\n" {
		t.Fatalf("fr : %q", out)
	}
	// Liste projet qui refuse l'hôte du fournisseur sélectionné.
	e.projectConfig("[provider_allowlist]\nhosts = [\"api.mistral.ai\"]\n")
	code, out, errb = e.run("--lang", "en", "config", "check")
	if code != 1 || out != "provider_allowlist: global 1 hosts + local, project 1 hosts\n" ||
		!strings.Contains(errb, `provider "test" (host "127.0.0.1") not in provider_allowlist (project)`) {
		t.Fatalf("projet : code=%d stdout=%q stderr=%q", code, out, errb)
	}
}

// Contournement des proxys d'environnement : strict-local, ou secnumcloud
// avec un fournisseur local ; le gestionnaire MCP, global, l'active pour
// les deux profils.
func TestProviderNoProxy(t *testing.T) {
	local := config.Provider{Jurisdiction: "local"}
	eu := config.Provider{Jurisdiction: "eu"}
	cases := []struct {
		n         profile.Name
		pc        config.Provider
		prov, mcp bool
	}{
		{profile.None, local, false, false},
		{profile.EUOnly, local, false, false},
		{profile.SecNumCloud, local, true, true},
		{profile.SecNumCloud, eu, false, true},
		{profile.StrictLocal, local, true, true},
	}
	for _, c := range cases {
		if got := providerNoProxy(c.n, c.pc); got != c.prov {
			t.Errorf("providerNoProxy(%q, %q) = %v", c.n, c.pc.Jurisdiction, got)
		}
		if got := mcpNoProxy(c.n); got != c.mcp {
			t.Errorf("mcpNoProxy(%q) = %v", c.n, got)
		}
	}
}

// config check affiche, comme le démarrage, les avis qui expliquent le refus.
func TestConfigCheckStrippedNoticeE2E(t *testing.T) {
	e := newEnv(t, nil, "eu")
	e.editGlobal("", qualified...)
	e.withPolicy(policyHead + "profile = \"secnumcloud\"\n[providers]\nallow_local = true\n")
	code, _, errb := e.run("--lang", "en", "config", "check")
	if code != 1 || !strings.Contains(errb, "policy acme-dev: qualifications declared in config ignored (test)\n") ||
		!strings.Contains(errb, `profile secnumcloud: provider "test" is not declared secnumcloud-qualified`) {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
}

// Sous secnumcloud, un serveur stdio non local est écarté pour sa juridiction,
// pas pour une qualification manquante.
func TestProfileMCPSkipStdioSecNumCloud(t *testing.T) {
	s := config.MCPServer{ID: "a", Transport: "stdio", Jurisdiction: "eu", Qualifications: []string{"secnumcloud"}}
	want := `profile secnumcloud: MCP server "a" (eu) skipped`
	if got := profileMCPSkip(i18n.EN, profile.SecNumCloud, s); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
