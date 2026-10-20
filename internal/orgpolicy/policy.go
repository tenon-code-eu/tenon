package orgpolicy

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/tenon-code-eu/tenon/internal/profile"
)

// Policy est la politique d'organisation décodée et validée.
type Policy struct {
	ID         string
	Version    int
	Profile    profile.Name // plancher ; "" = aucun
	Providers  *Providers   // nil : pas de liste blanche
	Rules      []Rule       // [[permissions.rules]]
	Redaction  []Redaction  // [[redaction.rules]]
	AuditLevel string       // [audit] level ; "" = non imposé
	MCP        MCP
	KeyID      string // renseigné par Load
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
		Level string `toml:"level"`
	} `toml:"audit"`
	MCP MCP `toml:"mcp"`
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
		if _, err := regexp.Compile(rd.Pattern); err != nil {
			return Policy{}, policyErr("redaction %q: %v", rd.Name, err)
		}
	}
	if r.Providers != nil {
		if err := checkHosts("providers.allow_hosts", r.Providers.AllowHosts); err != nil {
			return Policy{}, err
		}
		for _, d := range r.Providers.Define {
			if d.ID == "" || d.BaseURL == "" {
				return Policy{}, policyErr("providers.define: id and base_url are required")
			}
			switch d.Jurisdiction {
			case "eu", "non-eu", "local":
			default:
				return Policy{}, policyErr("providers.define %q: invalid jurisdiction %q", d.ID, d.Jurisdiction)
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
	return Policy{
		ID: r.ID, Version: r.Version, Profile: prof, Providers: r.Providers,
		Rules: r.Permissions.Rules, Redaction: r.Redaction.Rules,
		AuditLevel: r.Audit.Level, MCP: r.MCP,
	}, nil
}

func checkHosts(field string, hosts []string) error {
	for _, h := range hosts {
		if err := checkHost(h); err != nil {
			return policyErr("%s: %q: %v", field, h, err)
		}
	}
	return nil
}

func checkHost(h string) error {
	if h == "" {
		return errors.New("empty host")
	}
	if strings.ContainsAny(h, "/:") {
		return errors.New("scheme, path or port not allowed")
	}
	rest := h
	if strings.HasPrefix(h, "*.") {
		rest = h[2:]
		if len(strings.Split(rest, ".")) < 2 {
			return errors.New("wildcard needs at least two labels")
		}
	}
	if strings.Contains(rest, "*") {
		return errors.New("wildcard only allowed as leading \"*.\"")
	}
	for _, l := range strings.Split(rest, ".") {
		if l == "" {
			return errors.New("empty label")
		}
	}
	return nil
}

// HostOf renvoie l'hôte de l'URL en minuscules, sans port ; "" en cas d'erreur.
func HostOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// matchHost applique la règle exacte / joker strict.
func matchHost(entries []string, host string) bool {
	host = strings.ToLower(host)
	if host == "" {
		return false
	}
	for _, e := range entries {
		e = strings.ToLower(e)
		if rest, ok := strings.CutPrefix(e, "*."); ok {
			if strings.HasSuffix(host, "."+rest) {
				return true
			}
			continue
		}
		if host == e {
			return true
		}
	}
	return false
}

// AllowsHost indique si un fournisseur sur cet hôte est admis. Un *Policy nil
// n'impose rien.
func (p *Policy) AllowsHost(host string) bool {
	if p == nil || p.Providers == nil {
		return true
	}
	if p.Providers.AllowLocal && profile.IsLocalHost(host) {
		return true
	}
	return matchHost(p.Providers.AllowHosts, host)
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
			return matchHost(p.MCP.AllowHosts, HostOf(rawURL))
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
