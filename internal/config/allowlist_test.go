package config

import (
	"slices"
	"strings"
	"testing"

	"github.com/tenon-code-eu/tenon/internal/orgpolicy"
)

const provQual = "[[providers]]\nid = \"x\"\nbase_url = \"https://x.eu/v1\"\njurisdiction = \"eu\"\nqualifications = %s\n"

func qualCfg(q string) string { return strings.Replace(provQual, "%s", q, 1) }

func TestQualificationValid(t *testing.T) {
	c, err := Load(write(t, qualCfg(`["secnumcloud"]`)), "", env(nil))
	if err != nil {
		t.Fatal(err)
	}
	p, _ := c.Provider("x")
	if !slices.Equal(p.Qualifications, []string{"secnumcloud"}) {
		t.Fatalf("quals = %v", p.Qualifications)
	}
}

func TestQualificationUnknown(t *testing.T) {
	for _, q := range []string{"SecNumCloud", "hds"} {
		_, err := Load(write(t, qualCfg(`["`+q+`"]`)), "", env(nil))
		want := `config: unknown qualification "` + q + `" (secnumcloud)`
		if err == nil || err.Error() != want {
			t.Errorf("%s: err = %v", q, err)
		}
	}
}

func TestQualificationMCP(t *testing.T) {
	g := "[[mcp.servers]]\nid = \"s\"\ntransport = \"http\"\nurl = \"https://m.eu/x\"\njurisdiction = \"eu\"\nqualifications = [\"secnumcloud\"]\n"
	c, err := Load(write(t, g), "", env(nil))
	if err != nil || !slices.Equal(c.MCP.Servers[0].Qualifications, []string{"secnumcloud"}) {
		t.Fatalf("err = %v", err)
	}
	_, err = Load(write(t, strings.Replace(g, "secnumcloud", "nope", 1)), "", env(nil))
	if err == nil || err.Error() != `config: unknown qualification "nope" (secnumcloud)` {
		t.Fatalf("err = %v", err)
	}
}

func TestAllowlistGlobal(t *testing.T) {
	c, err := Load(write(t, "[provider_allowlist]\nhosts = [\"a.eu\", \"*.b.eu\"]\nlocal = true\n"), "", env(nil))
	if err != nil || c.Allowlist == nil || !c.Allowlist.Local || len(c.Allowlist.Hosts) != 2 || c.ProjectAllowlist != nil {
		t.Fatalf("c = %+v err = %v", c.Allowlist, err)
	}
}

func TestAllowlistProject(t *testing.T) {
	c, err := Load("", write(t, "[provider_allowlist]\nhosts = [\"a.eu\"]\n"), env(nil))
	if err != nil || c.Allowlist != nil || c.ProjectAllowlist == nil || c.ProjectAllowlist.Hosts[0] != "a.eu" {
		t.Fatalf("c = %+v err = %v", c.ProjectAllowlist, err)
	}
}

func TestAllowlistEmpty(t *testing.T) {
	_, err := Load(write(t, "[provider_allowlist]\n"), "", env(nil))
	if err == nil || err.Error() != "config: provider_allowlist is empty" {
		t.Fatalf("global err = %v", err)
	}
	_, err = Load("", write(t, "[provider_allowlist]\nlocal = false\n"), env(nil))
	if err == nil || err.Error() != "project config: provider_allowlist is empty" {
		t.Fatalf("project err = %v", err)
	}
}

func TestAllowlistBadEntry(t *testing.T) {
	_, err := Load(write(t, "[provider_allowlist]\nhosts = [\"https://x.eu\"]\n"), "", env(nil))
	if err == nil || !strings.HasPrefix(err.Error(), `config: provider_allowlist: "https://x.eu": `) {
		t.Fatalf("global err = %v", err)
	}
	_, err = Load("", write(t, "[provider_allowlist]\nhosts = [\"https://x.eu\"]\n"), env(nil))
	if err == nil || !strings.HasPrefix(err.Error(), `project config: provider_allowlist: "https://x.eu": `) {
		t.Fatalf("project err = %v", err)
	}
}

func TestApplyPolicyStripsQualifications(t *testing.T) {
	cfg := baseCfg()
	cfg.Providers = append(cfg.Providers,
		Provider{ID: "zeta", BaseURL: "https://z.eu", Jurisdiction: "eu", Qualifications: []string{"secnumcloud"}},
		Provider{ID: "alpha", BaseURL: "https://a.eu", Jurisdiction: "eu", Qualifications: []string{"secnumcloud"}},
		Provider{ID: "plain", BaseURL: "https://p.eu", Jurisdiction: "eu"})
	cfg.MCP.Servers = []MCPServer{{ID: "m", Transport: "http", URL: "https://m.eu", Jurisdiction: "eu", Qualifications: []string{"secnumcloud"}}}
	pol := &orgpolicy.Policy{ID: "p", Providers: &orgpolicy.Providers{Define: []orgpolicy.ProviderDef{
		{ID: "org", BaseURL: "https://o.eu", Jurisdiction: "eu", Qualifications: []string{"secnumcloud"}}}}}
	got, err := ApplyPolicy(cfg, pol)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range got.Providers {
		if (p.ID == "org") != (len(p.Qualifications) > 0) {
			t.Errorf("provider %s quals = %v", p.ID, p.Qualifications)
		}
	}
	if len(got.MCP.Servers[0].Qualifications) != 0 {
		t.Error("mcp quals kept")
	}
	if !slices.Equal(got.PolicyStrippedQualifications, []string{"alpha", "m", "zeta"}) {
		t.Errorf("stripped = %v", got.PolicyStrippedQualifications)
	}
	// cfg d'origine intacte (pas d'aliasing)
	if len(cfg.MCP.Servers[0].Qualifications) == 0 {
		t.Error("input cfg mutated")
	}
}

// Un id redéfini par la politique (sans qualifications) n'est pas listé.
func TestApplyPolicyStrippedExcludesDefined(t *testing.T) {
	cfg := baseCfg()
	cfg.Providers = append(cfg.Providers,
		Provider{ID: "org", BaseURL: "https://o.eu", Jurisdiction: "eu", Qualifications: []string{"secnumcloud"}})
	pol := &orgpolicy.Policy{ID: "p", Providers: &orgpolicy.Providers{Define: []orgpolicy.ProviderDef{
		{ID: "org", BaseURL: "https://o.eu", Jurisdiction: "eu"}}}}
	got, err := ApplyPolicy(cfg, pol)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range got.Providers {
		if p.ID == "org" && len(p.Qualifications) > 0 {
			t.Errorf("quals kept: %v", p.Qualifications)
		}
	}
	if len(got.PolicyStrippedQualifications) != 0 {
		t.Errorf("stripped = %v", got.PolicyStrippedQualifications)
	}
}

// Un id partagé par un fournisseur et un serveur MCP n'est listé qu'une fois.
func TestApplyPolicyStrippedDedup(t *testing.T) {
	cfg := baseCfg()
	cfg.Providers = append(cfg.Providers,
		Provider{ID: "x", BaseURL: "https://x.eu", Jurisdiction: "eu", Qualifications: []string{"secnumcloud"}})
	cfg.MCP.Servers = []MCPServer{{ID: "x", Transport: "http", URL: "https://m.eu", Jurisdiction: "eu", Qualifications: []string{"secnumcloud"}}}
	got, err := ApplyPolicy(cfg, &orgpolicy.Policy{ID: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.PolicyStrippedQualifications, []string{"x"}) {
		t.Errorf("stripped = %v", got.PolicyStrippedQualifications)
	}
}
