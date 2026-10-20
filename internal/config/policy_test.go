package config

import (
	"reflect"
	"testing"

	"github.com/tenon-code-eu/tenon/internal/orgpolicy"
	"github.com/tenon-code-eu/tenon/internal/profile"
)

func baseCfg() Config { return Defaults(func(string) string { return "" }) }

func TestApplyPolicyNil(t *testing.T) {
	cfg := baseCfg()
	got, err := ApplyPolicy(cfg, nil)
	if err != nil || !reflect.DeepEqual(got, cfg) {
		t.Fatalf("want unchanged, got err=%v", err)
	}
}

func TestApplyPolicyProfileFloor(t *testing.T) {
	cases := []struct{ cfg, pol, want, raised profile.Name }{
		{profile.None, profile.EUOnly, profile.EUOnly, profile.None},
		{profile.EUOnly, profile.StrictLocal, profile.StrictLocal, profile.EUOnly},
		{profile.StrictLocal, profile.EUOnly, profile.StrictLocal, profile.None},
	}
	for _, c := range cases {
		cfg := baseCfg()
		cfg.Profile = c.cfg
		got, err := ApplyPolicy(cfg, &orgpolicy.Policy{ID: "p", Profile: c.pol})
		if err != nil {
			t.Fatal(err)
		}
		if got.Profile != c.want || got.PolicyRaisedProfile != c.raised {
			t.Errorf("%q/%q: profile=%q raised=%q", c.cfg, c.pol, got.Profile, got.PolicyRaisedProfile)
		}
	}
}

func TestApplyPolicyDefineWins(t *testing.T) {
	cfg := baseCfg()
	cfg.Providers = append(cfg.Providers, Provider{ID: "interne", BaseURL: "https://evil.example/v1", Jurisdiction: "non-eu"})
	pol := &orgpolicy.Policy{ID: "p", Providers: &orgpolicy.Providers{Define: []orgpolicy.ProviderDef{
		{ID: "interne", Name: "Interne", BaseURL: "https://llm.corp.eu/v1", Jurisdiction: "eu", Model: "m"},
		{ID: "nouveau", BaseURL: "https://n.corp.eu/v1", Jurisdiction: "eu"},
	}}}
	got, err := ApplyPolicy(cfg, pol)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := got.Provider("interne")
	if p.BaseURL != "https://llm.corp.eu/v1" || p.Model != "m" {
		t.Errorf("provider = %+v", p)
	}
	if !reflect.DeepEqual(got.PolicyOverriddenProviders, []string{"interne"}) {
		t.Errorf("overridden = %v", got.PolicyOverriddenProviders)
	}
	if _, ok := got.Provider("nouveau"); !ok {
		t.Error("nouveau missing")
	}
}

func TestApplyPolicyDefineNoDetectInherit(t *testing.T) {
	cfg := baseCfg()
	var id string
	for _, p := range cfg.Providers {
		if p.Detect != "" {
			id = p.ID
		}
	}
	if id == "" {
		t.Skip("no detect preset")
	}
	pol := &orgpolicy.Policy{ID: "p", Providers: &orgpolicy.Providers{Define: []orgpolicy.ProviderDef{
		{ID: id, BaseURL: "https://x.corp.eu/v1", Jurisdiction: "eu"}}}}
	got, err := ApplyPolicy(cfg, pol)
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := got.Provider(id); p.Detect != "" {
		t.Errorf("detect inherited: %q", p.Detect)
	}
}

func TestApplyPolicyRules(t *testing.T) {
	cfg := baseCfg()
	cfg.Permissions.Rules = []Rule{{Tool: "bash", Decision: "allow"}}
	pol := &orgpolicy.Policy{ID: "p", Rules: []orgpolicy.Rule{{Tool: "bash", Match: "rm *", Decision: "deny"}}}
	got, err := ApplyPolicy(cfg, pol)
	if err != nil {
		t.Fatal(err)
	}
	r := got.Permissions.Rules
	if len(r) != 2 || !r[0].Policy || r[0].Decision != "deny" || r[1].Policy || r[1].Decision != "allow" {
		t.Errorf("rules = %+v", r)
	}
}

func TestApplyPolicyAudit(t *testing.T) {
	cfg := baseCfg()
	cfg.Audit.Level = "metadata"
	got, err := ApplyPolicy(cfg, &orgpolicy.Policy{ID: "p", AuditLevel: "full"})
	if err != nil || got.Audit.Level != "full" || got.PolicyAuditFrom != "metadata" {
		t.Errorf("level=%q from=%q err=%v", got.Audit.Level, got.PolicyAuditFrom, err)
	}
	cfg.Audit.Level = "full"
	got, _ = ApplyPolicy(cfg, &orgpolicy.Policy{ID: "p", AuditLevel: "full"})
	if got.PolicyAuditFrom != "" {
		t.Errorf("from = %q", got.PolicyAuditFrom)
	}
}

func TestApplyPolicyRedaction(t *testing.T) {
	cfg := baseCfg()
	cfg.Redaction.Rules = []RedactionRule{{Name: "u", Pattern: "a"}}
	got, err := ApplyPolicy(cfg, &orgpolicy.Policy{ID: "p", Redaction: []orgpolicy.Redaction{{Name: "o", Pattern: "b"}}})
	if err != nil {
		t.Fatal(err)
	}
	r := got.Redaction.Rules
	if len(r) != 2 || r[0].Name != "u" || r[1].Name != "o" {
		t.Errorf("redaction = %+v", r)
	}
}
