package config

import (
	"slices"

	"github.com/tenon-code-eu/tenon/internal/orgpolicy"
	"github.com/tenon-code-eu/tenon/internal/profile"
)

// ApplyPolicy applique la politique d'organisation comme plancher : elle ne
// fait que durcir la configuration déjà fusionnée. pol == nil renvoie cfg
// inchangée. Le filtrage MCP et la liste blanche d'hôtes ne sont pas
// appliqués ici (démarrage et moteur).
func ApplyPolicy(cfg Config, pol *orgpolicy.Policy) (Config, error) {
	if pol == nil {
		return cfg, nil
	}
	cfg.Policy = pol

	// Profil : le plus strict l'emporte ; on note le relèvement d'un profil
	// configuré explicitement.
	if cfg.Profile != profile.None && profile.Stricter(cfg.Profile, pol.Profile) != cfg.Profile {
		cfg.PolicyRaisedProfile = cfg.Profile
	}
	cfg.Profile = profile.Stricter(cfg.Profile, pol.Profile)

	// Qualifications : déclaratives, donc ignorées sous politique (seuls les
	// define en portent). On copie les serveurs MCP pour ne pas aliaser cfg.
	cfg.MCP.Servers = slices.Clone(cfg.MCP.Servers)
	var stripped []string
	cfg.Providers = slices.Clone(cfg.Providers)
	for i := range cfg.Providers {
		if len(cfg.Providers[i].Qualifications) > 0 {
			stripped = append(stripped, cfg.Providers[i].ID)
			cfg.Providers[i].Qualifications = nil
		}
	}
	for i := range cfg.MCP.Servers {
		if len(cfg.MCP.Servers[i].Qualifications) > 0 {
			stripped = append(stripped, cfg.MCP.Servers[i].ID)
			cfg.MCP.Servers[i].Qualifications = nil
		}
	}
	slices.Sort(stripped)
	cfg.PolicyStrippedQualifications = stripped

	// Fournisseurs imposés : ils remplacent ceux de la config, sans hériter
	// de la détection d'un préréglage. On copie pour ne pas aliaser cfg.
	if pol.Providers != nil {
		for _, d := range pol.Providers.Define {
			p := Provider{
				ID: d.ID, Name: d.Name, BaseURL: d.BaseURL, APIKeyEnv: d.APIKeyEnv,
				Model: d.Model, Country: d.Country, Jurisdiction: d.Jurisdiction,
				ContextWindow: d.ContextWindow, Qualifications: slices.Clone(d.Qualifications),
			}
			if i := slices.IndexFunc(cfg.Providers, func(q Provider) bool { return q.ID == p.ID }); i >= 0 {
				cfg.Providers[i] = p
				cfg.PolicyOverriddenProviders = append(cfg.PolicyOverriddenProviders, p.ID)
			} else {
				cfg.Providers = append(cfg.Providers, p)
			}
		}
	}

	// Règles de la politique en tête, celles de la config ensuite.
	rules := make([]Rule, 0, len(pol.Rules)+len(cfg.Permissions.Rules))
	for _, r := range pol.Rules {
		rules = append(rules, Rule{Tool: r.Tool, Match: r.Match, Decision: r.Decision, Policy: true})
	}
	cfg.Permissions.Rules = append(rules, cfg.Permissions.Rules...)

	red := slices.Clone(cfg.Redaction.Rules)
	for _, r := range pol.Redaction {
		red = append(red, RedactionRule{Name: r.Name, Pattern: r.Pattern})
	}
	cfg.Redaction.Rules = red

	if pol.AuditLevel != "" {
		if cfg.Audit.Level != pol.AuditLevel {
			cfg.PolicyAuditFrom = cfg.Audit.Level
		}
		cfg.Audit.Level = pol.AuditLevel
	}

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
