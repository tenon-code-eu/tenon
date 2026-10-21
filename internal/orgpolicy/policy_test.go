package orgpolicy

import (
	"errors"
	"reflect"
	"testing"

	"github.com/tenon-code-eu/tenon/internal/profile"
	"github.com/tenon-code-eu/tenon/internal/tsp/tsptest"
)

const fullTOML = `
id = "acme-dev"
version = 3
profile = "eu-only"

[providers]
allow_hosts = ["api.mistral.ai", "*.scaleway.ai"]
allow_local = true

[[providers.define]]
id = "interne"
name = "vLLM interne"
base_url = "https://llm.acme.eu/v1"
api_key_env = "ACME_LLM_KEY"
model = "qwen3"
country = "FR"
jurisdiction = "eu"
context_window = 128000

[[permissions.rules]]
tool = "shell"
match = "curl *"
decision = "deny"

[[redaction.rules]]
name = "acme-token"
pattern = "ACME-[A-Z0-9]{32}"

[audit]
level = "full"

[mcp]
mode = "allowlist"
allow_hosts = ["mcp.acme.eu"]
allow_commands = ["/usr/local/bin/mcp-git"]
`

func TestParseMinimal(t *testing.T) {
	p, err := Parse([]byte("id = \"acme-dev\"\nversion = 3"))
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "acme-dev" || p.Version != 3 || p.Providers != nil || p.MCP.Mode != "" {
		t.Fatalf("got %+v", p)
	}
}

func TestParseFull(t *testing.T) {
	p, err := Parse([]byte(fullTOML))
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "acme-dev" || p.Version != 3 || p.Profile != profile.EUOnly || p.AuditLevel != "full" {
		t.Fatalf("scalars: %+v", p)
	}
	pr := p.Providers
	if pr == nil || len(pr.AllowHosts) != 2 || !pr.AllowLocal || len(pr.Define) != 1 {
		t.Fatalf("providers: %+v", pr)
	}
	want := ProviderDef{ID: "interne", Name: "vLLM interne", BaseURL: "https://llm.acme.eu/v1",
		APIKeyEnv: "ACME_LLM_KEY", Model: "qwen3", Country: "FR", Jurisdiction: "eu", ContextWindow: 128000}
	if !reflect.DeepEqual(pr.Define[0], want) {
		t.Fatalf("define: %+v", pr.Define[0])
	}
	if len(p.Rules) != 1 || p.Rules[0] != (Rule{"shell", "curl *", "deny"}) {
		t.Fatalf("rules: %+v", p.Rules)
	}
	if len(p.Redaction) != 1 || p.Redaction[0] != (Redaction{"acme-token", "ACME-[A-Z0-9]{32}"}) {
		t.Fatalf("redaction: %+v", p.Redaction)
	}
	if p.MCP.Mode != "allowlist" || len(p.MCP.AllowHosts) != 1 || p.MCP.AllowCommands[0] != "/usr/local/bin/mcp-git" {
		t.Fatalf("mcp: %+v", p.MCP)
	}
}

func TestParseRejects(t *testing.T) {
	const hd = "id = \"a\"\nversion = 1\n"
	cases := map[string]string{
		"id bad":         "id = \"A B\"\nversion = 1",
		"id missing":     "version = 1",
		"version 0":      "id = \"a\"\nversion = 0",
		"version miss":   "id = \"a\"",
		"profile":        hd + "profile = \"nope\"",
		"unknown key":    hd + "[foo]\nx = 1",
		"allow rule":     hd + "[[permissions.rules]]\ntool=\"shell\"\ndecision=\"allow\"",
		"bad decision":   hd + "[[permissions.rules]]\ntool=\"shell\"\ndecision=\"maybe\"",
		"empty tool":     hd + "[[permissions.rules]]\ndecision=\"deny\"",
		"bad regexp":     hd + "[[redaction.rules]]\nname=\"x\"\npattern=\"(\"",
		"host empty":     hd + "[providers]\nallow_hosts=[\"\"]",
		"host slash":     hd + "[providers]\nallow_hosts=[\"a.eu/x\"]",
		"host port":      hd + "[providers]\nallow_hosts=[\"a.eu:443\"]",
		"host scheme":    hd + "[providers]\nallow_hosts=[\"https://a.eu\"]",
		"host star mid":  hd + "[providers]\nallow_hosts=[\"a.*.eu\"]",
		"host star one":  hd + "[providers]\nallow_hosts=[\"*.eu\"]",
		"host star bare": hd + "[providers]\nallow_hosts=[\"*\"]",
		"mcp host":       hd + "[mcp]\nallow_hosts=[\"a.eu:1\"]",
		"cmd relative":   hd + "[mcp]\nallow_commands=[\"mcp-git\"]",
		"mcp mode":       hd + "[mcp]\nmode=\"x\"",
		"audit level":    hd + "[audit]\nlevel=\"x\"",
		"define no id":   hd + "[[providers.define]]\nbase_url=\"https://a.eu\"\njurisdiction=\"eu\"",
		"define no url":  hd + "[[providers.define]]\nid=\"a\"\njurisdiction=\"eu\"",
		"define jur":     hd + "[[providers.define]]\nid=\"a\"\nbase_url=\"https://a.eu\"\njurisdiction=\"x\"",
		"redact no name": hd + "[[redaction.rules]]\npattern=\"x\"",
		"redact blank":   hd + "[[redaction.rules]]\nname=\"  \"\npattern=\"x\"",
		"redact no pat":  hd + "[[redaction.rules]]\nname=\"x\"",
		"define qual":    hd + "[[providers.define]]\nid=\"a\"\nbase_url=\"https://a.eu\"\njurisdiction=\"eu\"\nqualifications=[\"hds\"]",
		"define neg ctx": hd + "[[providers.define]]\nid=\"a\"\nbase_url=\"https://a.eu\"\njurisdiction=\"eu\"\ncontext_window=-1",
		"define dup id":  hd + "[[providers.define]]\nid=\"a\"\nbase_url=\"https://a.eu\"\njurisdiction=\"eu\"\n[[providers.define]]\nid=\"a\"\nbase_url=\"https://b.eu\"\njurisdiction=\"eu\"",
		"define nojur":   hd + "[[providers.define]]\nid=\"a\"\nbase_url=\"https://a.eu\"",
		"ts http remote": hd + "[audit.timestamp]\nurl=\"http://tsa.example.eu\"",
		"ts bad ca":      hd + "[audit.timestamp]\nurl=\"https://tsa.eu\"\nca=\"junk\"",
		"ts no url":      hd + "[audit.timestamp]\nca=\"x\"",
		"ts sl remote":   hd + "profile = \"strict-local\"\n[audit.timestamp]\nurl=\"https://tsa.example.eu\"",
	}
	for name, src := range cases {
		_, err := Parse([]byte(src))
		if err == nil {
			t.Errorf("%s: want error", name)
		} else if !errors.Is(err, ErrPolicy) {
			t.Errorf("%s: not ErrPolicy: %v", name, err)
		}
	}
}

func mustParse(t *testing.T, s string) *Policy {
	t.Helper()
	p, err := Parse([]byte("id = \"a\"\nversion = 1\n" + s))
	if err != nil {
		t.Fatal(err)
	}
	return &p
}

func TestAllowsHost(t *testing.T) {
	p := mustParse(t, "[providers]\nallow_hosts = [\"api.mistral.ai\", \"*.scaleway.ai\"]\nallow_local = false")
	for h, want := range map[string]bool{
		"api.mistral.ai": true, HostOf("https://API.Mistral.AI:443/v1"): true,
		"api.scaleway.ai": true, "a.b.scaleway.ai": true, "scaleway.ai": false,
		"api.scaleway.ai.evil.eu": false, "mistral.ai": false, "localhost": false,
	} {
		if got := p.AllowsHost(h); got != want {
			t.Errorf("%q: got %v", h, got)
		}
	}
	p = mustParse(t, "[providers]\nallow_hosts = [\"api.mistral.ai\"]\nallow_local = true")
	for h, want := range map[string]bool{"localhost": true, "127.0.0.1": true, "gpu.lan": false} {
		if got := p.AllowsHost(h); got != want {
			t.Errorf("local %q: got %v", h, got)
		}
	}
}

func TestAllowsHostNoProviders(t *testing.T) {
	if !mustParse(t, "").AllowsHost("anything.com") {
		t.Fatal("want true")
	}
	var p *Policy
	if !p.AllowsHost("x.com") || !p.AllowsMCP("http", "http://x", "") {
		t.Fatal("nil policy must allow")
	}
}

func TestAllowsMCP(t *testing.T) {
	any := mustParse(t, "")
	off := mustParse(t, "[mcp]\nmode = \"off\"")
	al := mustParse(t, "[mcp]\nmode = \"allowlist\"\nallow_hosts = [\"mcp.acme.eu\"]\nallow_commands = [\"/usr/local/bin/mcp-git\"]")
	if !any.AllowsMCP("http", "http://localhost:9000", "") || off.AllowsMCP("http", "https://mcp.acme.eu/x", "") ||
		off.AllowsMCP("stdio", "", "/usr/local/bin/mcp-git") {
		t.Fatal("any/off")
	}
	cases := []struct {
		tr, u, c string
		want     bool
	}{
		{"http", "https://mcp.acme.eu/x", "", true},
		{"http", "http://localhost:9000", "", false},
		{"stdio", "", "/usr/local/bin/mcp-git", true},
		{"stdio", "", "mcp-git", false},
		{"stdio", "", "/usr/local/bin/mcp-git2", false},
	}
	for _, c := range cases {
		if got := al.AllowsMCP(c.tr, c.u, c.c); got != c.want {
			t.Errorf("%+v: got %v", c, got)
		}
	}
}

func TestParseTimestamp(t *testing.T) {
	ca := tsptest.NewCA(t)
	p := mustParse(t, "[audit]\nlevel=\"full\"\n[audit.timestamp]\nurl=\"https://tsa.eu\"\nca='''"+string(ca.PEM)+"'''\n")
	if p.AuditLevel != "full" || p.Timestamp == nil || p.Timestamp.URL != "https://tsa.eu" || string(p.Timestamp.CA) != string(ca.PEM) {
		t.Fatalf("%+v", p)
	}
	p = mustParse(t, "[audit.timestamp]\nurl=\"https://tsa.eu\"\n")
	if p.Timestamp == nil || p.Timestamp.CA != nil {
		t.Fatalf("%+v", p.Timestamp)
	}
}
