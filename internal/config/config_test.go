package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func write(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDefaults(t *testing.T) {
	// Anglais par défaut, quelle que soit $LANG.
	c := Defaults(env(map[string]string{"LANG": "fr_FR.UTF-8"}))
	if c.Lang != "en" || c.MaxIterations != 50 || c.ShellTimeout.Duration != 2*time.Minute ||
		c.OutputLimit != 30000 || c.Audit.Level != "full" || c.DefaultProvider != "mistral" {
		t.Fatalf("défauts inattendus : %+v", c)
	}
	for _, id := range []string{"mistral", "scaleway", "ovhcloud"} {
		p, ok := c.Provider(id)
		if !ok || p.Jurisdiction != "eu" || p.Model == "" {
			t.Fatalf("préréglage %s : %+v ok=%v", id, p, ok)
		}
	}
	for _, id := range []string{"ollama", "vllm"} {
		p, ok := c.Provider(id)
		// Modèle et fenêtre lus sur le serveur au démarrage.
		if !ok || p.Jurisdiction != "local" || p.Model != "" || p.ContextWindow != 0 || p.Detect != id {
			t.Fatalf("préréglage %s : %+v ok=%v", id, p, ok)
		}
	}
}

func TestLangEnglish(t *testing.T) {
	if c := Defaults(env(map[string]string{"LANG": "en_US.UTF-8"})); c.Lang != "en" {
		t.Fatalf("lang = %q", c.Lang)
	}
}

func TestCustomProviderWithoutJurisdictionIsNonEU(t *testing.T) {
	g := write(t, `
[[providers]]
id = "acme"
name = "Acme"
base_url = "https://api.acme.example/v1"
model = "m"
`)
	c, err := Load(g, "", env(nil))
	if err != nil {
		t.Fatal(err)
	}
	p, ok := c.Provider("acme")
	if !ok || p.Jurisdiction != "non-eu" {
		t.Fatalf("%+v ok=%v", p, ok)
	}
	if _, ok := c.Provider("mistral"); !ok {
		t.Fatal("préréglage mistral perdu")
	}
}

func TestGlobalProviderReplacesPreset(t *testing.T) {
	g := write(t, `
[[providers]]
id = "mistral"
name = "Mistral custom"
base_url = "https://proxy.example/v1"
jurisdiction = "eu"
model = "x"
`)
	c, err := Load(g, "", env(nil))
	if err != nil {
		t.Fatal(err)
	}
	p, _ := c.Provider("mistral")
	if p.BaseURL != "https://proxy.example/v1" {
		t.Fatalf("%+v", p)
	}
}

func TestProjectCannotAllow(t *testing.T) {
	p := write(t, `
[[permissions.rules]]
tool = "shell"
match = "ls*"
decision = "allow"
`)
	_, err := Load("", p, env(nil))
	if err == nil || !strings.Contains(err.Error(), `"allow"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestProjectCannotDefineProviders(t *testing.T) {
	p := write(t, `
[[providers]]
id = "evil"
base_url = "https://evil.example/v1"
`)
	_, err := Load("", p, env(nil))
	if err == nil || !strings.Contains(err.Error(), `"providers"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestProjectCannotSetOtherKeys(t *testing.T) {
	for _, body := range []string{"max_iterations = 1\n", "[audit]\nlevel = \"metadata\"\n", "shell_timeout = \"1h\"\n"} {
		if _, err := Load("", write(t, body), env(nil)); err == nil || !strings.Contains(err.Error(), "key not allowed") {
			t.Fatalf("%q : err = %v", body, err)
		}
	}
}

func TestProjectCanAddDeny(t *testing.T) {
	g := write(t, `
[[permissions.rules]]
tool = "shell"
match = "ls*"
decision = "allow"
`)
	p := write(t, `
lang = "en"
[[permissions.rules]]
tool = "shell"
match = "rm*"
decision = "deny"
`)
	c, err := Load(g, p, env(nil))
	if err != nil {
		t.Fatal(err)
	}
	r := c.Permissions.Rules
	if len(r) != 2 || r[1].Decision != "deny" || r[0].Decision != "allow" || c.Lang != "en" {
		t.Fatalf("%+v lang=%s", r, c.Lang)
	}
}

func TestInvalidRegexRejected(t *testing.T) {
	g := write(t, `
[[redaction.rules]]
name = "bad"
pattern = "("
`)
	if _, err := Load(g, "", env(nil)); err == nil {
		t.Fatal("regex invalide acceptée")
	}
}

func TestInvalidDecisionRejected(t *testing.T) {
	g := write(t, `
[[permissions.rules]]
tool = "shell"
match = "*"
decision = "maybe"
`)
	if _, err := Load(g, "", env(nil)); err == nil {
		t.Fatal("decision invalide acceptée")
	}
}

func TestInvalidEnumsRejected(t *testing.T) {
	for _, body := range []string{
		`lang = "de"`,
		"[audit]\nlevel = \"none\"",
		"[[providers]]\nid = \"a\"\njurisdiction = \"mars\"",
	} {
		if _, err := Load(write(t, body), "", env(nil)); err == nil {
			t.Fatalf("%q accepté", body)
		}
	}
}

func TestUnknownDefaultProvider(t *testing.T) {
	g := write(t, `default_provider = "nope"`)
	if _, err := Load(g, "", env(nil)); err == nil {
		t.Fatal("default_provider inconnu accepté")
	}
}

func TestShellTimeoutParsed(t *testing.T) {
	c, err := Load(write(t, `shell_timeout = "30s"`), "", env(nil))
	if err != nil || c.ShellTimeout.Duration != 30*time.Second {
		t.Fatalf("%v %v", c.ShellTimeout, err)
	}
}

func TestMissingFilesUseDefaults(t *testing.T) {
	d := t.TempDir()
	c, err := Load(filepath.Join(d, "a.toml"), filepath.Join(d, "b.toml"), env(map[string]string{"LANG": "fr_FR"}))
	if err != nil || c.MaxIterations != 50 || c.Lang != "en" {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestPaths(t *testing.T) {
	if GlobalPath(env(map[string]string{"XDG_CONFIG_HOME": "/x"})) != "/x/tenon/config.toml" {
		t.Fatal("XDG config")
	}
	if GlobalPath(env(map[string]string{"HOME": "/h"})) != "/h/.config/tenon/config.toml" {
		t.Fatal("HOME config")
	}
	if DataDir(env(map[string]string{"XDG_DATA_HOME": "/x"})) != "/x/tenon" {
		t.Fatal("XDG data")
	}
	if DataDir(env(map[string]string{"HOME": "/h"})) != "/h/.local/share/tenon" {
		t.Fatal("HOME data")
	}
}

func TestProjectCannotSetDefaultProvider(t *testing.T) {
	_, err := Load("", write(t, `default_provider = "ollama"`+"\n"), env(nil))
	if err == nil || err.Error() != `project config: key not allowed "default_provider"` {
		t.Fatalf("err = %v", err)
	}
}

func TestGlobalRejectsUnknownKeys(t *testing.T) {
	for body, key := range map[string]string{
		"max_iteration = 3\n": "max_iteration",
		"[[permission.rules]]\ntool = \"shell\"\nmatch = \"rm*\"\ndecision = \"deny\"\n":             "permission.rules",
		"[[providers]]\nid = \"x\"\nbase_url = \"http://x\"\njurisdiction = \"eu\"\nmodle = \"m\"\n": "providers.modle",
	} {
		_, err := Load(write(t, body), "", env(nil))
		want := `global config: key not allowed "` + key + `"`
		if err == nil || !strings.HasPrefix(err.Error(), want) {
			t.Fatalf("%q : err = %v, want %s", body, err, want)
		}
	}
}

func TestProjectErrorUsesFullKeyPath(t *testing.T) {
	_, err := Load("", write(t, "[permissions]\nfoo = 1\n"), env(nil))
	if err == nil || err.Error() != `project config: key not allowed "permissions.foo"` {
		t.Fatalf("err = %v", err)
	}
}

func TestDefaultsCompaction(t *testing.T) {
	c := Defaults(env(nil))
	if c.CompactionThreshold != 0.8 {
		t.Fatalf("seuil = %v", c.CompactionThreshold)
	}
	p, _ := c.Provider("mistral")
	if p.Window() != 256000 {
		t.Fatalf("fenêtre mistral = %d", p.Window())
	}
}

// Valeurs vérifiées dans la documentation officielle de chaque fournisseur
// (sources et dates : RELEASE.md, section « Sources vérifiées »).
func TestPresetValues(t *testing.T) {
	want := []struct {
		id, baseURL, model string
		window             int
	}{
		{"mistral", "https://api.mistral.ai/v1", "mistral-medium-3-5", 256000},
		{"scaleway", "https://api.scaleway.ai/v1", "mistral-medium-3.5-128b", 180000},
		{"ovhcloud", "https://oai.endpoints.kepler.ai.cloud.ovh.net/v1", "Qwen3.8-27B", 262000},
		{"ollama", "http://localhost:11434/v1", "", 32000}, // détectés au démarrage
		{"vllm", "http://localhost:8000/v1", "", 32000},
	}
	c := Defaults(env(nil))
	for _, w := range want {
		p, ok := c.Provider(w.id)
		if !ok || p.BaseURL != w.baseURL || p.Model != w.model || p.Window() != w.window {
			t.Errorf("préréglage %s : %q %q %d, attendu %q %q %d",
				w.id, p.BaseURL, p.Model, p.Window(), w.baseURL, w.model, w.window)
		}
	}
}

func TestWindowFallback(t *testing.T) {
	if w := (Provider{}).Window(); w != 32000 {
		t.Fatalf("fenêtre = %d", w)
	}
}

func TestCompactionThresholdRange(t *testing.T) {
	for _, v := range []string{"0", "1", "1.5", "-0.1"} {
		if _, err := Load(write(t, "compaction_threshold = "+v+"\n"), "", env(nil)); err == nil {
			t.Errorf("seuil %s accepté", v)
		}
	}
	c, err := Load(write(t, "compaction_threshold = 0.5\n"), "", env(nil))
	if err != nil || c.CompactionThreshold != 0.5 {
		t.Fatalf("0.5 : %v %v", c.CompactionThreshold, err)
	}
}

func TestNegativeContextWindow(t *testing.T) {
	g := write(t, `
[[providers]]
id = "acme"
name = "Acme"
base_url = "https://api.acme.example/v1"
model = "m"
context_window = -1
`)
	if _, err := Load(g, "", env(nil)); err == nil {
		t.Fatal("context_window négatif accepté")
	}
}

func TestProjectCannotSetCompaction(t *testing.T) {
	p := write(t, "compaction_threshold = 0.5\n")
	_, err := Load("", p, env(nil))
	if err == nil || !strings.Contains(err.Error(), "key not allowed") {
		t.Fatalf("err = %v", err)
	}
}

func mcpErr(t *testing.T, toml, want string) {
	t.Helper()
	_, err := Load(write(t, toml), "", env(nil))
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("erreur attendue contenant %q, obtenu %v", want, err)
	}
}

func TestMCPStdioParsed(t *testing.T) {
	g := write(t, `
[[mcp.servers]]
id = "fs"
transport = "stdio"
command = ["mcp-fs", "--root", "."]
env_from = ["HOME"]
tools = ["read"]
`)
	c, err := Load(g, "", env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.MCP.Servers) != 1 {
		t.Fatalf("%+v", c.MCP)
	}
	s := c.MCP.Servers[0]
	if s.Jurisdiction != "local" || s.Risk != "" || len(s.Command) != 3 || s.EnvFrom[0] != "HOME" || s.Tools[0] != "read" {
		t.Fatalf("%+v", s)
	}
}

func TestMCPHTTPRequiresJurisdiction(t *testing.T) {
	mcpErr(t, `
[[mcp.servers]]
id = "web"
transport = "http"
url = "https://mcp.exemple.fr"
`, "jurisdiction")
	mcpErr(t, `
[[mcp.servers]]
id = "web"
transport = "http"
url = "https://mcp.exemple.fr"
jurisdiction = "mars"
`, "jurisdiction")
}

func TestMCPHTTPRequiresHTTPS(t *testing.T) {
	base := "[[mcp.servers]]\nid = \"web\"\ntransport = \"http\"\njurisdiction = \"eu\"\nurl = "
	for _, u := range []string{"http://exemple.fr", "", "ftp://x.fr", "https://user:pw@exemple.fr", "https://",
		"https://exemple.fr/mcp?token=x", "https://exemple.fr/mcp?", "https://exemple.fr/mcp#x", "http://127.0.0.1:8080/?k=v"} {
		mcpErr(t, base+`"`+u+`"`, "url")
	}
	for _, u := range []string{"http://127.0.0.1:8080", "http://localhost:9000/mcp", "http://[::1]:8080", "https://exemple.fr/mcp"} {
		c, err := Load(write(t, base+`"`+u+`"`), "", env(nil))
		if err != nil {
			t.Fatalf("%s : %v", u, err)
		}
		if c.MCP.Servers[0].URL != u {
			t.Fatalf("%+v", c.MCP.Servers[0])
		}
	}
}

func TestMCPInvalidID(t *testing.T) {
	srv := func(id string) string {
		return "[[mcp.servers]]\nid = \"" + id + "\"\ntransport = \"stdio\"\ncommand = [\"x\"]\n"
	}
	mcpErr(t, srv("GitHub!"), "server id")
	mcpErr(t, srv(""), "server id")
	mcpErr(t, srv(strings.Repeat("a", 33)), "server id")
	mcpErr(t, srv("a")+srv("a"), "duplicate")
}

func TestMCPUnknownTransport(t *testing.T) {
	mcpErr(t, "[[mcp.servers]]\nid = \"a\"\ntransport = \"ws\"\n", "transport")
}

func TestMCPStdioRequiresCommand(t *testing.T) {
	mcpErr(t, "[[mcp.servers]]\nid = \"a\"\ntransport = \"stdio\"\n", "command")
}

func TestMCPInvalidRisk(t *testing.T) {
	mcpErr(t, "[[mcp.servers]]\nid = \"a\"\ntransport = \"stdio\"\ncommand = [\"x\"]\nrisk = \"huge\"\n", "risk")
	for _, r := range []string{"read", "write", "exec", "network"} {
		_, err := Load(write(t, "[[mcp.servers]]\nid = \"a\"\ntransport = \"stdio\"\ncommand = [\"x\"]\nrisk = \""+r+"\"\n"), "", env(nil))
		if err != nil {
			t.Fatalf("%s : %v", r, err)
		}
	}
}

func TestProjectCannotDeclareMCP(t *testing.T) {
	p := write(t, "[[mcp.servers]]\nid = \"a\"\ntransport = \"stdio\"\ncommand = [\"x\"]\n")
	_, err := Load("", p, env(nil))
	if err == nil || !strings.Contains(err.Error(), "key not allowed") {
		t.Fatalf("err = %v", err)
	}
}

// Command[0] relatif avec séparateur (./srv, bin/srv) : résolu depuis le
// dépôt, refusé ; chemin absolu ou nom nu (cherché dans PATH) acceptés.
func TestMCPStdioRelativeCommand(t *testing.T) {
	srv := func(cmd string) string {
		return "[[mcp.servers]]\nid = \"a\"\ntransport = \"stdio\"\ncommand = [\"" + cmd + "\"]\n"
	}
	for _, c := range []string{"./srv", "bin/srv", "../srv"} {
		mcpErr(t, srv(c), "absolute path")
	}
	for _, c := range []string{"/usr/local/bin/srv", "srv"} {
		if _, err := Load(write(t, srv(c)), "", env(nil)); err != nil {
			t.Fatalf("%s : %v", c, err)
		}
	}
}

// Requête ou fragment dans l'URL : refusés, le message renvoie à bearer_env.
func TestMCPURLQueryRefused(t *testing.T) {
	mcpErr(t, "[[mcp.servers]]\nid = \"web\"\ntransport = \"http\"\njurisdiction = \"eu\"\nurl = \"https://exemple.fr/mcp?token=x\"\n", "bearer_env")
}

// Les règles de la config projet sont marquées comme telles (Project) pour que
// leurs ask priment les allow globales.
func TestProjectRulesMarked(t *testing.T) {
	g := write(t, `
[[permissions.rules]]
tool = "shell"
match = "*"
decision = "allow"
`)
	p := write(t, `
[[permissions.rules]]
tool = "shell"
match = "git push*"
decision = "ask"
`)
	c, err := Load(g, p, env(nil))
	if err != nil {
		t.Fatal(err)
	}
	r := c.Permissions.Rules
	if len(r) != 2 || r[0].Project || !r[1].Project {
		t.Fatalf("%+v", r)
	}
}

func TestDetectInheritedAndValidated(t *testing.T) {
	dir := t.TempDir()
	g := filepath.Join(dir, "config.toml")
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(g, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("[[providers]]\nid = \"ollama\"\nbase_url = \"http://gpu.lan:11434/v1\"\nmodel = \"qwen3:8b\"\njurisdiction = \"local\"\n")
	c, err := Load(g, "", env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := c.Provider("ollama"); p.Detect != "ollama" || p.Model != "qwen3:8b" || p.BaseURL != "http://gpu.lan:11434/v1" {
		t.Fatalf("détection non héritée : %+v", p)
	}
	write("[[providers]]\nid = \"ollama\"\ndetect = \"off\"\nmodel = \"m\"\njurisdiction = \"local\"\n")
	if c, err = Load(g, "", env(nil)); err != nil {
		t.Fatal(err)
	}
	if p, _ := c.Provider("ollama"); p.Detect != "off" {
		t.Fatalf("detect = off ignoré : %+v", p)
	}
	write("[[providers]]\nid = \"x\"\ndetect = \"lmstudio\"\n")
	if _, err := Load(g, "", env(nil)); err == nil || !strings.Contains(err.Error(), "invalid detect") {
		t.Fatalf("detect invalide accepté : %v", err)
	}
}

func TestHooksGlobalKey(t *testing.T) {
	c, err := Load(write(t, "[hooks]\nclaude_settings = true\ndisabled = true\n"), "", env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Hooks.ClaudeSettings || !c.Hooks.Disabled {
		t.Fatalf("hooks = %+v", c.Hooks)
	}
}

func TestHooksProjectKeyRefused(t *testing.T) {
	_, err := Load("", write(t, "[hooks]\ndisabled = true\n"), env(nil))
	if err == nil || !strings.Contains(err.Error(), "key not allowed") {
		t.Fatalf("err = %v", err)
	}
}
