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
	c := Defaults(env(map[string]string{"LANG": "fr_FR.UTF-8"}))
	if c.Lang != "fr" || c.MaxIterations != 50 || c.ShellTimeout.Duration != 2*time.Minute ||
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
		if !ok || p.Jurisdiction != "local" || p.Model == "" {
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
		if _, err := Load("", write(t, body), env(nil)); err == nil || !strings.Contains(err.Error(), "clé non autorisée") {
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
	if err != nil || c.MaxIterations != 50 || c.Lang != "fr" {
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
	if err == nil || err.Error() != `config projet : clé non autorisée "default_provider"` {
		t.Fatalf("err = %v", err)
	}
}

func TestGlobalRejectsUnknownKeys(t *testing.T) {
	for body, key := range map[string]string{
		"max_iteration = 3\n": "max_iteration",
		"[[permission.rules]]\ntool = \"shell\"\nmatch = \"rm*\"\ndecision = \"deny\"\n": "permission.rules",
		"[[providers]]\nid = \"x\"\nbase_url = \"http://x\"\njurisdiction = \"eu\"\nmodle = \"m\"\n": "providers.modle",
	} {
		_, err := Load(write(t, body), "", env(nil))
		want := `config globale : clé non autorisée "` + key + `"`
		if err == nil || !strings.HasPrefix(err.Error(), want) {
			t.Fatalf("%q : err = %v, want %s", body, err, want)
		}
	}
}

func TestProjectErrorUsesFullKeyPath(t *testing.T) {
	_, err := Load("", write(t, "[permissions]\nfoo = 1\n"), env(nil))
	if err == nil || err.Error() != `config projet : clé non autorisée "permissions.foo"` {
		t.Fatalf("err = %v", err)
	}
}
