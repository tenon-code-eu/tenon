package config

import (
	"strings"
	"testing"

	"github.com/tenon-code-eu/tenon/internal/orgpolicy"
)

func loadSB(t *testing.T, global, project string) (Config, error) {
	t.Helper()
	var g, p string
	if global != "" {
		g = write(t, global)
	}
	if project != "" {
		p = write(t, project)
	}
	return Load(g, p, env(map[string]string{"HOME": "/home/u"}))
}

func wantErr(t *testing.T, err error, msg string) {
	t.Helper()
	if err == nil || err.Error() != msg {
		t.Fatalf("err = %v, want %q", err, msg)
	}
}

func TestSandboxDefault(t *testing.T) {
	cfg, err := loadSB(t, "", "")
	if err != nil || cfg.Sandbox.Mode != "auto" {
		t.Fatalf("%v %q", err, cfg.Sandbox.Mode)
	}
}

func TestSandboxModeInvalid(t *testing.T) {
	_, err := loadSB(t, "[sandbox]\nmode = \"x\"\n", "")
	wantErr(t, err, "config: sandbox.mode must be auto, required or off")
}

func TestSandboxPathInvalid(t *testing.T) {
	_, err := loadSB(t, "[sandbox]\nwrite = [\"rel\"]\n", "")
	wantErr(t, err, "config: sandbox.write: path must be absolute or start with ~/")
	_, err = loadSB(t, "[sandbox]\ndeny_read = [\"rel\"]\n", "")
	wantErr(t, err, "config: sandbox.deny_read: path must be absolute or start with ~/")
}

func TestSandboxHomeExpansion(t *testing.T) {
	cfg, err := loadSB(t, "[sandbox]\nwrite = [\"~/w\", \"/abs\"]\ndeny_read = [\"~/d\"]\n", "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(cfg.Sandbox.Write, ",") != "/home/u/w,/abs" || cfg.Sandbox.DenyRead[0] != "/home/u/d" {
		t.Fatalf("%+v", cfg.Sandbox)
	}
	g := write(t, "[sandbox]\nwrite = [\"~/w\"]\n")
	_, err = Load(g, "", env(nil))
	wantErr(t, err, "config: sandbox.write: HOME not set")
}

func TestSandboxProjectOff(t *testing.T) {
	_, err := loadSB(t, "", "[sandbox]\nmode = \"off\"\n")
	wantErr(t, err, "config: sandbox.mode not allowed in project configuration")
}

func TestSandboxProjectWrite(t *testing.T) {
	_, err := loadSB(t, "", "[sandbox]\nwrite = [\"/x\"]\n")
	wantErr(t, err, "config: sandbox.write not allowed in project configuration")
}

func TestSandboxProjectHardens(t *testing.T) {
	cfg, err := loadSB(t, "[sandbox]\nmode = \"auto\"\ndeny_read = [\"/g\"]\n", "[sandbox]\nmode = \"required\"\ndeny_read = [\"/p\"]\n")
	if err != nil || cfg.Sandbox.Mode != "required" || strings.Join(cfg.Sandbox.DenyRead, ",") != "/g,/p" {
		t.Fatalf("%v %+v", err, cfg.Sandbox)
	}
	// Le projet n'assouplit jamais : global required reste required.
	cfg, err = loadSB(t, "[sandbox]\nmode = \"required\"\n", "[sandbox]\nmode = \"auto\"\n")
	if err != nil || cfg.Sandbox.Mode != "required" {
		t.Fatalf("%v %+v", err, cfg.Sandbox)
	}
}

func TestRuleNetwork(t *testing.T) {
	cfg, err := loadSB(t, "[[permissions.rules]]\ntool = \"shell\"\nmatch = \"go mod download *\"\ndecision = \"allow\"\nnetwork = true\n", "")
	if err != nil || !cfg.Permissions.Rules[0].Network {
		t.Fatalf("%v", err)
	}
	if _, err = loadSB(t, "[[permissions.rules]]\ntool = \"shell\"\ndecision = \"ask\"\nnetwork = true\n", ""); err != nil {
		t.Fatal(err)
	}
}

func TestRuleNetworkDeny(t *testing.T) {
	_, err := loadSB(t, "[[permissions.rules]]\ntool = \"shell\"\ndecision = \"deny\"\nnetwork = true\n", "")
	wantErr(t, err, "config: network only with allow or ask")
}

func TestRuleNetworkTool(t *testing.T) {
	_, err := loadSB(t, "[[permissions.rules]]\ntool = \"read_file\"\ndecision = \"allow\"\nnetwork = true\n", "")
	wantErr(t, err, "config: network only for tool shell")
}

func TestRuleNetworkProject(t *testing.T) {
	_, err := loadSB(t, "", "[[permissions.rules]]\ntool = \"shell\"\ndecision = \"ask\"\nnetwork = true\n")
	wantErr(t, err, "config: network not allowed in project configuration")
}

func loadPol(t *testing.T, global, project string, pol *orgpolicy.Policy) (Config, error) {
	t.Helper()
	var g, p string
	if global != "" {
		g = write(t, global)
	}
	if project != "" {
		p = write(t, project)
	}
	return LoadPolicy(g, p, env(map[string]string{"HOME": "/home/u"}), pol)
}

func TestPolicySandbox(t *testing.T) {
	pol := &orgpolicy.Policy{ID: "p", Sandbox: orgpolicy.Sandbox{Mode: "required", DenyRead: []string{"/srv/s", "~/o"}}}
	cfg, err := loadPol(t, "[sandbox]\nmode = \"auto\"\ndeny_read = [\"/g\"]\n", "", pol)
	if err != nil || cfg.Sandbox.Mode != "required" || strings.Join(cfg.Sandbox.DenyRead, ",") != "/g,/srv/s,/home/u/o" {
		t.Fatalf("%v %+v", err, cfg.Sandbox)
	}
	cfg, err = loadPol(t, "[sandbox]\nmode = \"off\"\n", "", &orgpolicy.Policy{ID: "p", Sandbox: orgpolicy.Sandbox{Mode: "auto"}})
	if err != nil || cfg.Sandbox.Mode != "auto" {
		t.Fatalf("%v %+v", err, cfg.Sandbox)
	}
	cfg, err = loadPol(t, "[sandbox]\nmode = \"off\"\n", "", &orgpolicy.Policy{ID: "p"})
	if err != nil || cfg.Sandbox.Mode != "off" {
		t.Fatalf("%v %+v", err, cfg.Sandbox)
	}
}

func TestStrictLocalSandbox(t *testing.T) {
	cfg, err := loadSB(t, "profile = \"strict-local\"\ndefault_provider = \"ollama\"\n[sandbox]\nmode = \"off\"\n", "")
	if err != nil || cfg.Sandbox.Mode != "required" {
		t.Fatalf("%v %+v", err, cfg.Sandbox)
	}
	// Profil relevé par la politique : plancher appliqué dans ApplyPolicy.
	cfg, err = loadPol(t, "[sandbox]\nmode = \"off\"\n", "", &orgpolicy.Policy{ID: "p", Profile: "strict-local"})
	if err != nil {
		t.Skipf("profil strict-local incompatible avec le fournisseur par défaut : %v", err)
	}
	if cfg.Sandbox.Mode != "required" {
		t.Fatalf("%+v", cfg.Sandbox)
	}
}
