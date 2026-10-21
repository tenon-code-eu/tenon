package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// altConfig écrit un fichier de configuration pointant vers f.
func altConfig(e *env, f *fakeLLM, extra string) string {
	p := filepath.Join(e.t.TempDir(), "ci.toml")
	e.writeFile(p, fmt.Sprintf(`default_provider = "ci"

[[providers]]
id = "ci"
name = "CI SAS"
base_url = %q
api_key_env = "TEST_KEY"
model = "m"
country = "FR"
jurisdiction = "eu"
`, f.srv.URL)+extra)
	return p
}

func TestConfigFlagMissing(t *testing.T) {
	f := newFakeLLM(t, sseText("ok"))
	e := newEnv(t, f, "eu")
	code, _, errb := e.run("--lang", "fr", "--config", "/nope.toml", "-p", "x")
	if code != 1 || !strings.Contains(errb, "config: /nope.toml: no such file or directory") {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	if n := len(f.requests()); n != 0 {
		t.Fatalf("%d requête(s)", n)
	}
}

func TestConfigFlagDirectory(t *testing.T) {
	e := newEnv(t, nil, "eu")
	d := t.TempDir()
	code, _, errb := e.run("--config", d, "-p", "x")
	if code != 1 || !strings.Contains(errb, "config: "+d+": is a directory") {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
}

func TestConfigFlagUsed(t *testing.T) {
	f := newFakeLLM(t, sseText("ok"))
	// La configuration par défaut vise un fournisseur injoignable.
	e := newEnv(t, nil, "eu")
	p := altConfig(e, f, "")
	code, _, errb := e.run("--config", p, "-p", "x")
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	if n := len(f.requests()); n != 1 {
		t.Fatalf("%d requête(s)", n)
	}
}

func TestConfigFlagProjectAndPolicy(t *testing.T) {
	f := newFakeLLM(t, sseText("ok"))
	e := newEnv(t, nil, "eu")
	p := altConfig(e, f, "")
	e.writeFile(filepath.Join(e.wd, ".tenon", "config.toml"), "[[permissions.rules]]\ntool = \"shell\"\nmatch = \"git push*\"\ndecision = \"deny\"\n")
	e.withPolicy(policyHead + "profile = \"strict-local\"\n")
	code, _, errb := e.run("--lang", "en", "--config", p, "-p", "x")
	// Le profil de la politique s'applique : le fournisseur (UE, non local) est refusé.
	if code != 1 || !strings.Contains(errb, "strict-local") {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	if n := len(f.requests()); n != 0 {
		t.Fatalf("%d requête(s)", n)
	}
	// La règle projet s'applique aussi : config check passe sans politique et
	// la configuration effective contient la règle.
	delete(e.vars, "TENON_POLICY_DIR")
	setup, err := loadSetup("en", func(k string) string { return e.vars[k] }, e.wd, p)
	if err != nil || len(setup.rules) == 0 {
		t.Fatalf("règle projet absente : %v", err)
	}
}

func TestConfigFlagSubcommands(t *testing.T) {
	f := newFakeLLM(t, sseText("ok"))
	e := newEnv(t, f, "eu")
	p := altConfig(e, f, "")
	code, out, errb := e.run("--lang", "en", "--config", p, "config", "check")
	if code != 0 || !strings.HasPrefix(out, "config: "+p+"\n") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errb)
	}
	// audit timestamp lit [audit.timestamp] du fichier désigné.
	q := altConfig(e, f, "\n[audit.timestamp]\nurl = \"http://127.0.0.1:1/tsa\"\n")
	_, _, errb = e.run("--lang", "en", "--config", p, "audit", "timestamp")
	_, _, errb2 := e.run("--lang", "en", "--config", q, "audit", "timestamp")
	if errb == errb2 {
		t.Fatalf("même sortie avec et sans [audit.timestamp] : %q", errb)
	}
}

func TestConfigCheckDefaultAbsent(t *testing.T) {
	e := newEnv(t, nil, "eu")
	def := filepath.Join(e.vars["XDG_CONFIG_HOME"], "tenon", "config.toml")
	if err := os.Remove(def); err != nil {
		t.Fatal(err)
	}
	code, out, errb := e.run("--lang", "en", "config", "check")
	if !strings.HasPrefix(out, "config: "+def+" (absent)\n") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errb)
	}
}

// runCheck exécute « config check » (options racine en tête) et retire la
// première ligne « config : <chemin> », dont le chemin varie.
func (e *env) runCheck(opts ...string) (int, string, string) {
	e.t.Helper()
	code, out, errb := e.run(append(opts, "config", "check")...)
	if l, rest, ok := strings.Cut(out, "\n"); ok && (strings.HasPrefix(l, "config: ") || strings.HasPrefix(l, "config : ")) {
		out = rest
	}
	return code, out, errb
}
