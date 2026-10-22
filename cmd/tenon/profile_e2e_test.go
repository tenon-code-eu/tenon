package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/exp/teatest"

	"github.com/tenon-code-eu/tenon/internal/event"
)

// globalConfig est le chemin de la configuration globale de l'environnement.
func (e *env) globalConfig() string {
	return filepath.Join(e.vars["XDG_CONFIG_HOME"], "tenon", "config.toml")
}

// editGlobal réécrit la configuration globale : top (clés de premier niveau)
// est placé en tête, puis chaque paire old → new de repl est remplacée.
func (e *env) editGlobal(top string, repl ...string) {
	e.t.Helper()
	b, err := os.ReadFile(e.globalConfig())
	if err != nil {
		e.t.Fatal(err)
	}
	s := strings.NewReplacer(repl...).Replace(string(b))
	e.writeFile(e.globalConfig(), top+s)
}

// sessionStarted renvoie les charges SessionStarted d'un fichier JSONL.
func sessionStarted(t *testing.T, content string) []event.SessionStartedPayload {
	t.Helper()
	var out []event.SessionStartedPayload
	for _, ev := range decodeEvents(t, content) {
		if ev.Type != event.SessionStarted {
			continue
		}
		var p event.SessionStartedPayload
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

// sessionContent renvoie le contenu de l'unique fichier de session.
func (e *env) sessionContent() string {
	e.t.Helper()
	ents, err := e.sessionFiles()
	if err != nil || len(ents) != 1 {
		e.t.Fatalf("sessions : %v %v", ents, err)
	}
	b, err := os.ReadFile(filepath.Join(e.sessionsDir(), ents[0].Name()))
	if err != nil {
		e.t.Fatal(err)
	}
	return string(b)
}

func TestProfileEUOnlyRefusesNonEUE2E(t *testing.T) {
	f := newFakeLLM(t, sseText("salut"))
	e := newEnv(t, f, "non-eu")
	e.editGlobal("profile = \"eu-only\"\n")
	code, _, errb := e.run("--lang", "en", "-p", "salut")
	if code != 1 || !strings.Contains(errb, `profile eu-only: provider "test" (non-eu) not allowed`) {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	if n := len(f.requests()); n != 0 {
		t.Fatalf("%d requête(s) envoyée(s)", n)
	}
	code, _, errb = e.run("-p", "salut")
	if code != 1 || !strings.Contains(errb, "profil eu-only : fournisseur « test » (non-eu) non autorisé") {
		t.Fatalf("fr : code=%d stderr=%q", code, errb)
	}
	if ents, _ := e.sessionFiles(); len(ents) != 0 {
		t.Fatalf("session créée : %v", ents)
	}
}

func TestProfileUnknownJurisdictionE2E(t *testing.T) {
	f := newFakeLLM(t, sseText("salut"))
	e := newEnv(t, f, "")
	e.editGlobal("profile = \"eu-only\"\n", `jurisdiction = ""`, "")
	// Juridiction absente : non-eu par défaut à la lecture de la configuration.
	code, _, errb := e.run("--lang", "en", "-p", "salut")
	if code != 1 || !strings.Contains(errb, `profile eu-only: provider "test" (non-eu) not allowed`) {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
}

func TestProfileAllowNonEUE2E(t *testing.T) {
	f := newFakeLLM(t, sseText("salut"))
	e := newEnv(t, f, "eu")
	e.editGlobal("profile = \"eu-only\"\n")
	code, _, errb := e.run("--lang", "en", "--allow-non-eu", "-p", "salut")
	if code != 1 || !strings.Contains(errb, "profile eu-only: --allow-non-eu not allowed") {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	code, _, errb = e.run("--allow-non-eu", "-p", "salut")
	if code != 1 || !strings.Contains(errb, "profil eu-only : --allow-non-eu non autorisé") {
		t.Fatalf("fr : code=%d stderr=%q", code, errb)
	}
	// Interface interactive : refus avant l'ouverture de la TUI.
	r := e.runTUI(func(*teatest.TestModel) {}, "--lang", "en", "--allow-non-eu")
	if r.code != 1 || r.started || !strings.Contains(r.stderr, "profile eu-only: --allow-non-eu not allowed") {
		t.Fatalf("tui : code=%d started=%v stderr=%q", r.code, r.started, r.stderr)
	}
	if n := len(f.requests()); n != 0 {
		t.Fatalf("%d requête(s) envoyée(s)", n)
	}
}

func TestProfileStrictLocalDefaultE2E(t *testing.T) {
	f := newFakeLLM(t, sseText("salut"))
	e := newEnv(t, f, "local")
	e.editGlobal("profile = \"strict-local\"\n", `default_provider = "test"`, `default_provider = "mistral"`)
	code, _, errb := e.run("--lang", "en", "-p", "salut")
	want := `profile strict-local: provider "mistral" (eu) not allowed (use --provider ollama or set default_provider)`
	if code != 1 || !strings.Contains(errb, want) {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	code, _, errb = e.run("-p", "salut")
	if code != 1 || !strings.Contains(errb, "(utilisez --provider ollama ou default_provider)") {
		t.Fatalf("fr : code=%d stderr=%q", code, errb)
	}
}

// Le contrôle du profil précède la détection du modèle local : l'hôte
// distant n'est jamais contacté (aucune erreur de détection).
func TestProfileStrictLocalNoDetectE2E(t *testing.T) {
	e := newEnvExtra(t, nil, "local", `
[[providers]]
id = "ollama"
base_url = "http://gpu.invalid:11434/v1"
jurisdiction = "local"
`)
	e.editGlobal("profile = \"strict-local\"\n")
	code, _, errb := e.run("--lang", "en", "--provider", "ollama", "-p", "salut")
	if code != 1 || !strings.Contains(errb, `profile strict-local: provider "ollama" url host "gpu.invalid" is not a local address (set a local base_url (localhost or a private IP address))`) {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	if strings.Contains(errb, "unreachable") || strings.Contains(errb, "serves no model") {
		t.Fatalf("détection lancée : %q", errb)
	}
	code, _, errb = e.run("--provider", "ollama", "-p", "salut")
	if code != 1 || !strings.Contains(errb, "profil strict-local : l'hôte « gpu.invalid » de l'URL du fournisseur « ollama » n'est pas une adresse locale (fixez une base_url locale (localhost ou une adresse IP privée))") {
		t.Fatalf("fr : code=%d stderr=%q", code, errb)
	}
}

func TestProfileStrictLocalOKE2E(t *testing.T) {
	sandboxReady(t)
	f := newFakeLLM(t, sseText("bonjour local"))
	e := newEnv(t, f, "local")
	e.editGlobal("profile = \"strict-local\"\n")
	code, out, errb := e.run("-p", "salut")
	if code != 0 || !strings.Contains(out, "bonjour local") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errb)
	}
	if ss := sessionStarted(t, e.sessionContent()); len(ss) != 1 || ss[0].Profile != "strict-local" {
		t.Fatalf("session : %+v", ss)
	}
	if !strings.Contains(e.auditContents(), `"profile":"strict-local"`) {
		t.Fatal("profil absent de l'audit")
	}
}

// --resume d'une session ouverte sans profil : refusée comme une nouvelle
// session avec un fournisseur interdit ; reprise avec un fournisseur UE, le
// nouveau SessionStarted porte le profil.
func TestProfileResumeE2E(t *testing.T) {
	f := newFakeLLM(t, sseText("enchanté"), sseText("re"))
	e := newEnv(t, f, "eu")
	if code, out, errb := e.run("-p", "je m'appelle Ada"); code != 0 {
		t.Fatalf("run 1 : code=%d stdout=%q stderr=%q", code, out, errb)
	}
	e.editGlobal("profile = \"eu-only\"\n", `jurisdiction = "eu"`, `jurisdiction = "non-eu"`)
	code, _, errb := e.run("--lang", "en", "--resume", "last", "-p", "suite")
	if code != 1 || !strings.Contains(errb, `profile eu-only: provider "test" (non-eu) not allowed`) || len(f.requests()) != 1 {
		t.Fatalf("run 2 : code=%d requêtes=%d stderr=%q", code, len(f.requests()), errb)
	}
	e.editGlobal("", `jurisdiction = "non-eu"`, `jurisdiction = "eu"`)
	if code, out, errb := e.run("--resume", "last", "-p", "suite"); code != 0 {
		t.Fatalf("run 3 : code=%d stdout=%q stderr=%q", code, out, errb)
	}
	ss := sessionStarted(t, e.sessionContent())
	if len(ss) != 2 || ss[0].Profile != "" || ss[1].Profile != "eu-only" {
		t.Fatalf("SessionStarted : %+v", ss)
	}
}

func TestProfileMCPSkippedE2E(t *testing.T) {
	ts, _, all := httpMCPAll(t)
	f := newFakeLLM(t, sseCall("mcp__web__echo", `{"text":"x"}`), sseText("fini"))
	e := newEnvExtra(t, f, "eu", httpDecl(ts.URL, "non-eu", "")+allowRule("mcp__web__echo"))
	e.editGlobal("profile = \"eu-only\"\n")
	code, out, errb := e.run("--lang", "en", "-p", "appelle echo")
	if code != 0 || !strings.Contains(out, "fini") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errb)
	}
	if !strings.Contains(errb, `profile eu-only: MCP server "web" (non-eu) skipped`) {
		t.Fatalf("avis absent : %q", errb)
	}
	if all.Load() != 0 {
		t.Fatalf("%d requête(s) reçue(s) par le serveur ignoré", all.Load())
	}
	var down []event.McpServerDownPayload
	for _, ev := range decodeEvents(t, e.sessionContent()) {
		if ev.Type == event.McpServerDown {
			var p event.McpServerDownPayload
			if err := json.Unmarshal(ev.Payload, &p); err != nil {
				t.Fatal(err)
			}
			down = append(down, p)
		}
	}
	if len(down) != 1 || down[0].Server != "web" || down[0].Error != `profile eu-only: MCP server "web" (non-eu) skipped` {
		t.Fatalf("McpServerDown : %+v", down)
	}
	if reqs := f.requests(); len(reqs) == 0 || strings.Contains(reqs[0], "mcp__web__echo\"") {
		t.Fatalf("outil du serveur ignoré proposé au modèle : %q", reqs)
	}
	_, _, errb = e.run("-p", "appelle echo")
	if !strings.Contains(errb, "profil eu-only : serveur MCP « web » (non-eu) ignoré") {
		t.Fatalf("fr : %q", errb)
	}
}

// strict-local : un serveur http local de nom d'hôte distant est ignoré pour
// son hôte.
func TestProfileMCPHostSkippedE2E(t *testing.T) {
	sandboxReady(t)
	f := newFakeLLM(t, sseText("fini"))
	e := newEnvExtra(t, f, "local", httpDecl("https://mcp.example", "local", ""))
	e.editGlobal("profile = \"strict-local\"\n")
	code, _, errb := e.run("--lang", "en", "-p", "salut")
	if code != 0 || !strings.Contains(errb, `profile strict-local: MCP server "web" (url host "mcp.example" is not a local address) skipped`) {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	_, _, errb = e.run("-p", "salut")
	if !strings.Contains(errb, "profil strict-local : serveur MCP « web » (l'hôte « mcp.example » de l'URL n'est pas une adresse locale) ignoré") {
		t.Fatalf("fr : %q", errb)
	}
}

// TUI : le serveur ignoré est signalé dès l'ouverture, n'est jamais contacté
// et aucune demande de consentement n'apparaît pour lui.
func TestProfileTUIMCPSkippedE2E(t *testing.T) {
	ts, _, all := httpMCPAll(t)
	f := newFakeLLM(t, sseCall("mcp__web__echo", `{"text":"x"}`), sseText("terminé"))
	e := newEnvExtra(t, f, "eu", httpDecl(ts.URL, "non-eu", ""))
	e.editGlobal("profile = \"eu-only\"\n")
	r := e.runTUI(func(tm *teatest.TestModel) {
		waitText(t, tm, "ignoré")
		typeText(tm, "appelle echo")
		pressEnter(tm)
		waitText(t, tm, "terminé")
		quitTUI(t, tm)
	})
	if r.code != 0 || all.Load() != 0 {
		t.Fatalf("code=%d requêtes=%d stderr=%q", r.code, all.Load(), r.stderr)
	}
	if strings.Contains(r.view, "serveur MCP web ·") || strings.Contains(r.view, "envoyés hors de l'UE") {
		t.Fatalf("demande de consentement affichée :\n%s", r.view)
	}
	if r.opts.Profile != "eu-only" {
		t.Fatalf("Options.Profile = %q", r.opts.Profile)
	}
	found := false
	for _, n := range r.opts.Notices {
		found = found || n == "profil eu-only : serveur MCP « web » (non-eu) ignoré"
	}
	if !found {
		t.Fatalf("avis absent : %q", r.opts.Notices)
	}
}

func TestProfileWeakerProjectE2E(t *testing.T) {
	sandboxReady(t)
	f := newFakeLLM(t, sseText("salut"))
	e := newEnv(t, f, "local")
	e.editGlobal("profile = \"strict-local\"\n")
	e.writeFile(filepath.Join(e.wd, ".tenon", "config.toml"), "profile = \"eu-only\"\n")
	code, _, errb := e.run("--lang", "en", "-p", "salut")
	if code != 0 || !strings.Contains(errb, `project profile "eu-only" is weaker than global profile "strict-local": ignored`) {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	_, _, errb = e.run("-p", "salut")
	if !strings.Contains(errb, "profil de projet « eu-only » plus souple que le profil global « strict-local » : ignoré") {
		t.Fatalf("fr : %q", errb)
	}
}

func TestConfigCheckProfileE2E(t *testing.T) {
	e := newEnv(t, nil, "eu")
	e.editGlobal("profile = \"eu-only\"\n")
	code, out, errb := e.runCheck("--lang", "en")
	if code != 0 || out != "profile: eu-only\n"+sandboxCheckEN+"OK\n" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errb)
	}
	if _, out, _ = e.runCheck(); out != "profil : eu-only\n"+sandboxCheckFR+"OK\n" {
		t.Fatalf("fr : %q", out)
	}
	e.editGlobal("", `jurisdiction = "eu"`, `jurisdiction = "non-eu"`)
	code, out, errb = e.runCheck("--lang", "en")
	if code != 1 || strings.Contains(out, "OK") || !strings.Contains(errb, `profile eu-only: provider "test" (non-eu) not allowed`) {
		t.Fatalf("violation : code=%d stdout=%q stderr=%q", code, out, errb)
	}
}
