package main

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/sandbox"
)

// withProbe remplace la sonde d'isolation par st et active les avis du bac à
// sable, le temps du test.
func withProbe(t *testing.T, st sandbox.Status) {
	t.Helper()
	oldProbe, oldNotices := probeSandbox, sandboxNotices
	probeSandbox = func() sandbox.Status { return st }
	sandboxNotices = true
	t.Cleanup(func() { probeSandbox, sandboxNotices = oldProbe, oldNotices })
}

// sandboxTOML ajoute [sandbox] mode = mode en tête de la configuration globale.
func sandboxTOML(mode string) string {
	return "\n[sandbox]\nmode = \"" + mode + "\"\n"
}

// shellResults renvoie les ToolResult de l'outil shell d'une sortie JSON.
func shellResults(t *testing.T, out string) []event.ToolResultPayload {
	t.Helper()
	var res []event.ToolResultPayload
	for _, ev := range decodeEvents(t, out) {
		if ev.Type != event.ToolResult {
			continue
		}
		var p event.ToolResultPayload
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			t.Fatal(err)
		}
		if p.Tool == "shell" {
			res = append(res, p)
		}
	}
	return res
}

func TestSandboxRequiredUnavailable(t *testing.T) {
	withProbe(t, sandbox.Status{Available: false, Reason: "x"})
	f := newFakeLLM(t, sseText("salut"))
	e := newEnvExtra(t, f, "eu", sandboxTOML("required"))
	code, _, errb := e.run("--lang", "en", "-p", "salut")
	if code != 1 || !strings.Contains(errb, "sandbox: required but unavailable (x)") {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	if n := len(f.requests()); n != 0 {
		t.Fatalf("%d requête(s) au fournisseur", n)
	}
}

func TestSandboxRequiredNetworkNone(t *testing.T) {
	withProbe(t, sandbox.Status{Available: true, Mechanism: "landlock", Network: "none", Reason: "y"})
	f := newFakeLLM(t, sseText("salut"))
	e := newEnvExtra(t, f, "eu", sandboxTOML("required"))
	code, _, errb := e.run("--lang", "en", "-p", "salut")
	if code != 1 || !strings.Contains(errb, "sandbox: required but network not isolated (y)") {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	if n := len(f.requests()); n != 0 {
		t.Fatalf("%d requête(s) au fournisseur", n)
	}
}

// Les refus de « required » ne dépendent pas des avis (sandboxNotices).
func TestSandboxRequiredRefusalNotSuppressed(t *testing.T) {
	withProbe(t, sandbox.Status{Available: false, Reason: "x"})
	sandboxNotices = false
	e := newEnvExtra(t, newFakeLLM(t, sseText("salut")), "eu", sandboxTOML("required"))
	code, _, errb := e.run("--lang", "en", "-p", "salut")
	if code != 1 || !strings.Contains(errb, "sandbox: required but unavailable (x)") {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
}

func TestSandboxAutoUnavailable(t *testing.T) {
	withProbe(t, sandbox.Status{Available: false, Mechanism: "none", Network: "none", Reason: "x"})
	f := newFakeLLM(t, sseCall("shell", `{"command":"echo bonjour-shell"}`), sseText("fini"))
	e := newEnvExtra(t, f, "eu", allowRule("shell"))
	code, out, errb := e.run("--lang", "en", "-p", "lance", "--output", "json")
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	if !strings.Contains(errb, "sandbox: unavailable (x), commands run unconfined") {
		t.Fatalf("avis absent : %q", errb)
	}
	res := shellResults(t, out)
	if len(res) != 1 || !strings.Contains(res[0].Content, "bonjour-shell") || res[0].Sandbox == nil || res[0].Sandbox.Confined || !res[0].Sandbox.Network {
		t.Fatalf("résultats shell : %+v", res)
	}
}

func TestSandboxAutoNetworkNotIsolatedNotice(t *testing.T) {
	withProbe(t, sandbox.Status{Available: true, Mechanism: "landlock", Network: "none", Reason: "y"})
	e := newEnv(t, newFakeLLM(t, sseText("salut")), "eu")
	code, _, errb := e.run("--lang", "en", "-p", "salut")
	if code != 0 || !strings.Contains(errb, "sandbox: network not isolated (y)") {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
}

func TestSandboxTCPNotice(t *testing.T) {
	withProbe(t, sandbox.Status{Available: true, Mechanism: "landlock", Network: "tcp", Reason: "user namespaces unavailable: z", UnixSockets: true})
	e := newEnv(t, newFakeLLM(t, sseText("salut")), "eu")
	code, _, errb := e.run("--lang", "en", "-p", "salut")
	if code != 0 || !strings.Contains(errb, "sandbox: network isolation TCP only (user namespaces unavailable)") {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
}

// Les avis sont en français avec --lang fr.
func TestSandboxNoticeFrench(t *testing.T) {
	withProbe(t, sandbox.Status{Available: false, Mechanism: "none", Network: "none", Reason: "x"})
	e := newEnv(t, newFakeLLM(t, sseText("salut")), "eu")
	code, _, errb := e.run("--lang", "fr", "-p", "salut")
	if code != 0 || !strings.Contains(errb, "sandbox : indisponible (x), les commandes s'exécutent sans isolation") {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
}

// R11 : sans confinement des sockets Unix (Landlock ABI < 9), chaque socket
// d'exécution connu et présent est signalé.
func TestSandboxUnixSocketNotice(t *testing.T) {
	withProbe(t, sandbox.Status{Available: true, Mechanism: "landlock", Network: "full", UnixSockets: false})
	// Chemin court : la limite d'un chemin de socket est d'environ 104 octets.
	rt, err := os.MkdirTemp("/tmp", "rt")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(rt) })
	sock := filepath.Join(rt, "docker.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	e := newEnv(t, newFakeLLM(t, sseText("salut")), "eu")
	e.vars["XDG_RUNTIME_DIR"] = rt
	code, _, errb := e.run("--lang", "en", "-p", "salut")
	want := "sandbox: Unix sockets not confined (Landlock ABI < 9): " + sock + " reachable"
	if code != 0 || !strings.Contains(errb, want) {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	if strings.Contains(errb, filepath.Join(rt, "podman", "podman.sock")) {
		t.Fatalf("socket absent signalé : %q", errb)
	}
	// Sockets confinés : aucun avis.
	withProbe(t, sandbox.Status{Available: true, Mechanism: "landlock", Network: "full", UnixSockets: true})
	if _, _, errb := e.run("--lang", "en", "-p", "salut"); strings.Contains(errb, "Unix sockets") {
		t.Fatalf("avis inattendu : %q", errb)
	}
}

func TestSandboxSessionStarted(t *testing.T) {
	withProbe(t, sandbox.Status{Available: true, Mechanism: "sandbox-exec", Network: "full"})
	e := newEnv(t, newFakeLLM(t, sseText("salut")), "eu")
	if code, _, errb := e.run("-p", "salut"); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	ss := sessionStarted(t, e.sessionContent())
	want := event.SandboxInfo{Mode: "auto", Mechanism: "sandbox-exec", Network: "full"}
	if len(ss) != 1 || ss[0].Sandbox == nil || *ss[0].Sandbox != want {
		t.Fatalf("SessionStarted : %+v", ss)
	}
	b, err := json.Marshal(want)
	if err != nil || string(b) != `{"mode":"auto","mechanism":"sandbox-exec","network":"full"}` {
		t.Fatalf("JSON : %s %v", b, err)
	}
}

// Mode off : aucune sonde, isolation absente déclarée.
func TestSandboxSessionStartedOff(t *testing.T) {
	probed := false
	withProbe(t, sandbox.Status{})
	probeSandbox = func() sandbox.Status { probed = true; return sandbox.Status{} }
	e := newEnvExtra(t, newFakeLLM(t, sseText("salut")), "eu", sandboxTOML("off"))
	if code, _, errb := e.run("-p", "salut"); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	ss := sessionStarted(t, e.sessionContent())
	want := event.SandboxInfo{Mode: "off", Mechanism: "none", Network: "none"}
	if probed || len(ss) != 1 || ss[0].Sandbox == nil || *ss[0].Sandbox != want {
		t.Fatalf("sonde=%v SessionStarted : %+v", probed, ss)
	}
}

// Un serveur MCP stdio non confiné (isolation indisponible) est déclaré tel
// quel dans SessionStarted.
func TestSandboxMCPSessionStarted(t *testing.T) {
	withProbe(t, sandbox.Status{Available: false, Mechanism: "none", Network: "none", Reason: "x"})
	e := newEnvExtra(t, newFakeLLM(t, sseText("salut")), "eu", stdioDecl(t))
	stdioMCP(t, e, "")
	if code, _, errb := e.run("-p", "salut"); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	ss := sessionStarted(t, e.sessionContent())
	if len(ss) != 1 || len(ss[0].MCP) != 1 || ss[0].MCP[0].Sandbox == nil || *ss[0].MCP[0].Sandbox != (event.SandboxUse{Confined: false, Network: true}) {
		t.Fatalf("SessionStarted : %+v", ss)
	}
}

func TestSandboxSecretEnv(t *testing.T) {
	for _, mode := range []string{"auto", "off"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("K1", "valeur-secrete-k1")
			t.Setenv("K2", "valeur-secrete-k2")
			t.Setenv("K3", "valeur-secrete-k3")
			t.Setenv("K_VISIBLE", "visible-ok")
			f := newFakeLLM(t, sseCall("shell", `{"command":"env"}`), sseText("fini"))
			e := newEnvExtra(t, f, "eu", `
[[providers]]
id = "autre"
name = "Autre"
base_url = "http://127.0.0.1:1"
api_key_env = "K1"
model = "m"
country = "FR"
jurisdiction = "eu"

[[mcp.servers]]
id = "distant"
transport = "http"
url = "http://127.0.0.1:1/mcp"
bearer_env = "K2"
jurisdiction = "eu"
disabled = true

[[mcp.servers]]
id = "local"
transport = "stdio"
command = ["/nonexistent"]
env_from = ["K3"]
jurisdiction = "eu"
disabled = true
`+allowRule("shell")+sandboxTOML(mode))
			e.vars["K1"], e.vars["K2"], e.vars["K3"] = "valeur-secrete-k1", "valeur-secrete-k2", "valeur-secrete-k3"
			code, out, errb := e.run("-p", "env", "--output", "json")
			if code != 0 {
				t.Fatalf("code=%d stderr=%q", code, errb)
			}
			res := shellResults(t, out)
			if len(res) != 1 || !strings.Contains(res[0].Content, "K_VISIBLE=") {
				t.Fatalf("résultats shell : %+v", res)
			}
			for _, k := range []string{"K1=", "K2=", "K3=", "TEST_KEY="} {
				if strings.Contains(res[0].Content, "\n"+k) || strings.HasPrefix(res[0].Content, k) {
					t.Fatalf("%s présent : %q", k, res[0].Content)
				}
			}
		})
	}
}

func TestSandboxConfigCheck(t *testing.T) {
	withProbe(t, sandbox.Status{Available: true, Mechanism: "sandbox-exec", Network: "full"})
	e := newEnv(t, nil, "eu")
	code, out, errb := e.runCheck("--lang", "en")
	if code != 0 || !strings.Contains(out, "sandbox: auto (sandbox-exec, network full)\n") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errb)
	}

	withProbe(t, sandbox.Status{Available: false, Mechanism: "none", Network: "none", Reason: "x"})
	e = newEnvExtra(t, nil, "eu", sandboxTOML("required"))
	code, out, errb = e.runCheck("--lang", "en")
	if !strings.Contains(out, "sandbox: required (unavailable: x)\n") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errb)
	}
	// Le démarrage serait refusé : config check échoue de même.
	if code != 1 || !strings.Contains(errb, "sandbox: required but unavailable (x)") {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
}

// Isolation réelle (sonde du système) : un secret de ~/.ssh n'est pas lisible,
// même sous une règle allow.
func TestSandboxRealDenyRead(t *testing.T) {
	if st := sandbox.Probe(); !st.Available {
		t.Skipf("isolation indisponible : %s", st.Reason)
	}
	withProbe(t, sandbox.Probe())
	sandboxNotices = false
	f := newFakeLLM(t, sseCall("shell", `{"command":"cat ~/.ssh/id"}`), sseText("fini"))
	e := newEnvExtra(t, f, "eu", allowRule("shell"))
	home := e.vars["HOME"]
	e.writeFile(filepath.Join(home, ".ssh", "id"), "CLE-PRIVEE-SECRETE\n")
	t.Setenv("HOME", home)
	code, out, errb := e.run("-p", "lis", "--output", "json")
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	res := shellResults(t, out)
	if len(res) != 1 || !res[0].IsError || strings.Contains(res[0].Content, "CLE-PRIVEE-SECRETE") || res[0].Sandbox == nil || !res[0].Sandbox.Confined {
		t.Fatalf("résultats shell : %+v", res)
	}
}

// Ligne « config check » de l'isolation sous la sonde par défaut des tests.
const (
	sandboxCheckEN = "sandbox: auto (unavailable: test)\n"
	sandboxCheckFR = "sandbox : auto (indisponible : test)\n"
)

// sandboxReady déclare l'isolation disponible (réseau complet, sockets Unix
// confinés), sans avis : pour les tests sous strict-local (mode required)
// qui ne lancent ni commande shell ni serveur stdio.
func sandboxReady(t *testing.T) {
	t.Helper()
	withProbe(t, sandbox.Status{Available: true, Mechanism: "landlock", Network: "full", UnixSockets: true})
	sandboxNotices = false
}

// strict-local impose le mode required : sans isolation, le démarrage est
// refusé avant tout contact du fournisseur.
func TestSandboxStrictLocalRequired(t *testing.T) {
	withProbe(t, sandbox.Status{Available: false, Reason: "x"})
	f := newFakeLLM(t, sseText("bonjour local"))
	e := newEnv(t, f, "local")
	e.editGlobal("profile = \"strict-local\"\n")
	code, _, errb := e.run("--lang", "en", "-p", "salut")
	if code != 1 || !strings.Contains(errb, "sandbox: required but unavailable (x)") {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	if n := len(f.requests()); n != 0 {
		t.Fatalf("%d requête(s)", n)
	}
}

// R14 : un --config explicite hors du projet est interdit (le fichier seul) ;
// dans le projet, ou au chemin par défaut, il ne l'est pas.
func TestSandboxConfigDeny(t *testing.T) {
	wd := t.TempDir()
	getenv := func(k string) string { return map[string]string{"XDG_CONFIG_HOME": "/x"}[k] }
	out := filepath.Join(t.TempDir(), "ci.toml")
	if got := configDeny(out, wd, getenv); len(got) != 1 || got[0] != out {
		t.Fatalf("hors projet : %v", got)
	}
	for _, p := range []string{filepath.Join(wd, "ci", "tenon.toml"), "/x/tenon/config.toml", ""} {
		if got := configDeny(p, wd, getenv); got != nil {
			t.Fatalf("%q : %v", p, got)
		}
	}
	// Chemin du projet par un lien symbolique : comparaison canonique.
	link := filepath.Join(t.TempDir(), "lien")
	if err := os.Symlink(wd, link); err != nil {
		t.Fatal(err)
	}
	if got := configDeny(filepath.Join(link, "ci.toml"), wd, getenv); got != nil {
		t.Fatalf("lien : %v", got)
	}
}

// Isolation réelle : le fichier --config hors du projet n'est pas lisible.
func TestSandboxRealConfigDeny(t *testing.T) {
	if st := sandbox.Probe(); !st.Available {
		t.Skipf("isolation indisponible : %s", st.Reason)
	}
	withProbe(t, sandbox.Probe())
	sandboxNotices = false
	e := newEnv(t, nil, "eu")
	f := newFakeLLM(t, nil, sseText("fini"))
	p := altConfig(e, f, allowRule("shell"))
	f.script[0] = sseCall("shell", `{"command":"cat `+p+`"}`)
	code, out, errb := e.run("--config", p, "-p", "lis", "--output", "json")
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	res := shellResults(t, out)
	if len(res) != 1 || !res[0].IsError || strings.Contains(res[0].Content, "default_provider") || res[0].Sandbox == nil || !res[0].Sandbox.Confined {
		t.Fatalf("résultats shell : %+v", res)
	}
}
