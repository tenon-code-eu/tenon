package orgpolicy

import (
	"crypto/x509"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/tenon-code-eu/tenon/internal/hostmatch"
	"github.com/tenon-code-eu/tenon/internal/profile"
	"github.com/tenon-code-eu/tenon/internal/tsp"
)

// Policy est la politique d'organisation décodée et validée.
type Policy struct {
	ID         string
	Version    int
	Profile    profile.Name    // plancher ; "" = aucun
	Providers  *Providers      // nil : pas de liste blanche
	Rules      []Rule          // [[permissions.rules]]
	Redaction  []Redaction     // [[redaction.rules]]
	AuditLevel string          // [audit] level ; "" = non imposé
	Timestamp  *AuditTimestamp // [audit.timestamp] ; nil = non imposé
	MCP        MCP
	Sandbox    Sandbox // [sandbox] ; Mode "" = non imposé
	KeyID      string  // renseigné par Load
}

// AuditTimestamp : horodatage RFC 3161 imposé. CA : PEM des racines de
// confiance de l'autorité (nil = racines système).
type AuditTimestamp struct {
	URL string
	CA  []byte
}

// Sandbox : confinement imposé. Mode : "auto" ou "required" ("off" refusé).
// Les chemins sont absolus ou commencent par « ~/ ».
type Sandbox struct {
	Mode     string   `toml:"mode"`
	DenyRead []string `toml:"deny_read"`
}

// Providers porte la liste blanche et les fournisseurs imposés.
type Providers struct {
	AllowHosts []string      `toml:"allow_hosts"`
	AllowLocal bool          `toml:"allow_local"`
	Define     []ProviderDef `toml:"define"`
}

// ProviderDef est un fournisseur fourni par l'organisation.
type ProviderDef struct {
	ID            string `toml:"id"`
	Name          string `toml:"name"`
	BaseURL       string `toml:"base_url"`
	APIKeyEnv     string `toml:"api_key_env"`
	Model         string `toml:"model"`
	Country       string `toml:"country"`
	Jurisdiction  string `toml:"jurisdiction"`
	ContextWindow int    `toml:"context_window"`
	// Qualifications : qualifications déclarées par l'organisation.
	Qualifications []string `toml:"qualifications"`
}

// Rule est une règle de permission (deny ou ask uniquement).
type Rule struct {
	Tool     string `toml:"tool"`
	Match    string `toml:"match"`
	Decision string `toml:"decision"`
}

// Redaction est un motif de masquage imposé.
type Redaction struct {
	Name    string `toml:"name"`
	Pattern string `toml:"pattern"`
}

// MCP restreint les serveurs MCP ; Mode "" équivaut à "any".
type MCP struct {
	Mode          string   `toml:"mode"`
	AllowHosts    []string `toml:"allow_hosts"`
	AllowCommands []string `toml:"allow_commands"`
}

// raw reflète la forme TOML (tables imbriquées) avant aplatissement.
type raw struct {
	ID          string     `toml:"id"`
	Version     int        `toml:"version"`
	Profile     string     `toml:"profile"`
	Providers   *Providers `toml:"providers"`
	Permissions struct {
		Rules []Rule `toml:"rules"`
	} `toml:"permissions"`
	Redaction struct {
		Rules []Redaction `toml:"rules"`
	} `toml:"redaction"`
	Audit struct {
		Level     string `toml:"level"`
		Timestamp *struct {
			URL string `toml:"url"`
			CA  string `toml:"ca"`
		} `toml:"timestamp"`
	} `toml:"audit"`
	MCP     MCP     `toml:"mcp"`
	Sandbox Sandbox `toml:"sandbox"`
}

var idRe = regexp.MustCompile(`^[a-z0-9_-]{1,64}$`)

// Parse décode et valide strictement une politique.
func Parse(b []byte) (Policy, error) {
	var r raw
	md, err := toml.Decode(string(b), &r)
	if err != nil {
		return Policy{}, policyErr("toml: %v", err)
	}
	if un := md.Undecoded(); len(un) > 0 {
		return Policy{}, policyErr("unknown key %q", un[0].String())
	}
	if !idRe.MatchString(r.ID) {
		return Policy{}, policyErr("invalid id %q", r.ID)
	}
	if r.Version < 1 {
		return Policy{}, policyErr("version must be >= 1")
	}
	prof, err := profile.Parse(r.Profile)
	if err != nil {
		return Policy{}, fmt.Errorf("%w: %w", ErrPolicy, err)
	}
	for _, rl := range r.Permissions.Rules {
		if rl.Tool == "" {
			return Policy{}, policyErr("permissions rule: empty tool")
		}
		switch rl.Decision {
		case "ask", "deny":
		case "allow":
			return Policy{}, policyErr(`"allow" rules are not allowed`)
		default:
			return Policy{}, policyErr("permissions rule: invalid decision %q", rl.Decision)
		}
	}
	for _, rd := range r.Redaction.Rules {
		if strings.TrimSpace(rd.Name) == "" || rd.Pattern == "" {
			return Policy{}, policyErr("redaction rule: name and pattern are required")
		}
		if _, err := regexp.Compile(rd.Pattern); err != nil {
			return Policy{}, policyErr("redaction %q: %v", rd.Name, err)
		}
	}
	if r.Providers != nil {
		if err := checkHosts("providers.allow_hosts", r.Providers.AllowHosts); err != nil {
			return Policy{}, err
		}
		seen := map[string]bool{}
		for _, d := range r.Providers.Define {
			if d.ID == "" || d.BaseURL == "" {
				return Policy{}, policyErr("providers.define: id and base_url are required")
			}
			if seen[d.ID] {
				return Policy{}, policyErr("providers.define: duplicate id %q", d.ID)
			}
			seen[d.ID] = true
			if d.ContextWindow < 0 {
				return Policy{}, policyErr("providers.define %q: negative context_window", d.ID)
			}
			switch d.Jurisdiction {
			case "eu", "non-eu", "local":
			default:
				return Policy{}, policyErr("providers.define %q: invalid jurisdiction %q", d.ID, d.Jurisdiction)
			}
			for _, q := range d.Qualifications {
				if q != profile.QualSecNumCloud {
					return Policy{}, policyErr("providers.define %q: unknown qualification %q (%s)", d.ID, q, profile.QualSecNumCloud)
				}
			}
		}
	}
	if err := checkHosts("mcp.allow_hosts", r.MCP.AllowHosts); err != nil {
		return Policy{}, err
	}
	for _, c := range r.MCP.AllowCommands {
		if !filepath.IsAbs(c) {
			return Policy{}, policyErr("mcp.allow_commands: %q is not absolute", c)
		}
	}
	switch r.MCP.Mode {
	case "", "any", "allowlist", "off":
	default:
		return Policy{}, policyErr("unknown mcp.mode %q", r.MCP.Mode)
	}
	switch r.Audit.Level {
	case "", "full", "metadata":
	default:
		return Policy{}, policyErr("unknown audit.level %q", r.Audit.Level)
	}
	switch r.Sandbox.Mode {
	case "", "auto", "required":
	default:
		return Policy{}, policyErr("sandbox.mode must be auto or required")
	}
	for _, d := range r.Sandbox.DenyRead {
		if !filepath.IsAbs(d) && !strings.HasPrefix(d, "~/") {
			return Policy{}, policyErr("sandbox.deny_read: path must be absolute or start with ~/")
		}
	}
	var ts *AuditTimestamp
	if t := r.Audit.Timestamp; t != nil {
		if t.URL == "" {
			return Policy{}, policyErr("audit.timestamp.url missing")
		}
		if err := tsp.CheckURL(t.URL); err != nil {
			return Policy{}, policyErr("%v", err)
		}
		if prof == profile.StrictLocal {
			if h := tsp.Host(t.URL); !profile.IsLocalHost(h) {
				return Policy{}, policyErr("audit.timestamp.url host %q is not local (profile %s)", h, prof)
			}
		}
		ts = &AuditTimestamp{URL: t.URL}
		if t.CA != "" {
			if !x509.NewCertPool().AppendCertsFromPEM([]byte(t.CA)) {
				return Policy{}, policyErr("audit.timestamp.ca: no certificate")
			}
			ts.CA = []byte(t.CA)
		}
	}
	return Policy{
		Timestamp: ts,
		ID:        r.ID, Version: r.Version, Profile: prof, Providers: r.Providers,
		Rules: r.Permissions.Rules, Redaction: r.Redaction.Rules,
		AuditLevel: r.Audit.Level, MCP: r.MCP, Sandbox: r.Sandbox,
	}, nil
}

func checkHosts(field string, hosts []string) error {
	for _, h := range hosts {
		if err := hostmatch.CheckEntry(h); err != nil {
			return policyErr("%s: %q: %v", field, h, err)
		}
	}
	return nil
}

// HostOf renvoie l'hôte de l'URL en minuscules, sans port ; "" en cas d'erreur.
func HostOf(rawURL string) string { return hostmatch.HostOf(rawURL) }

// AllowsHost indique si un fournisseur sur cet hôte est admis. Un *Policy nil
// n'impose rien.
func (p *Policy) AllowsHost(host string) bool {
	if p == nil || p.Providers == nil {
		return true
	}
	if p.Providers.AllowLocal && profile.IsLocalHost(host) {
		return true
	}
	return hostmatch.Match(p.Providers.AllowHosts, host)
}

// AllowsMCP indique si un serveur MCP est admis (transport "http" ou "stdio").
func (p *Policy) AllowsMCP(transport, rawURL, command0 string) bool {
	if p == nil {
		return true
	}
	switch p.MCP.Mode {
	case "", "any":
		return true
	case "allowlist":
		switch transport {
		case "http":
			return hostmatch.Match(p.MCP.AllowHosts, HostOf(rawURL))
		case "stdio":
			for _, c := range p.MCP.AllowCommands {
				if c == command0 {
					return true
				}
			}
		}
	}
	return false
}
