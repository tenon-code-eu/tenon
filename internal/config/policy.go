package config

import (
	"fmt"
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

	// Avis des qualifications retirées : hors ids définis par la politique
	// (ils portent leurs propres qualifications), triés et dédoublonnés.
	if pol.Providers != nil {
		stripped = slices.DeleteFunc(stripped, func(id string) bool {
			return slices.ContainsFunc(pol.Providers.Define, func(d orgpolicy.ProviderDef) bool { return d.ID == id })
		})
	}
	slices.Sort(stripped)
	cfg.PolicyStrippedQualifications = slices.Compact(stripped)

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

	if pol.Timestamp != nil {
		if u := cfg.Audit.Timestamp; u == nil || u.URL != pol.Timestamp.URL {
			cfg.PolicyTimestampImposed = true
		}
		ts := &AuditTimestamp{URL: pol.Timestamp.URL, CAPEM: slices.Clone(pol.Timestamp.CA)}
		// Sans ca dans la politique, le ca_file de l'utilisateur reste ; avec
		// un ca, celui-ci l'emporte et ca_file est abandonné.
		if len(ts.CAPEM) == 0 && cfg.Audit.Timestamp != nil {
			ts.CAFile = cfg.Audit.Timestamp.CAFile
		}
		cfg.Audit.Timestamp = ts
	}

	// Classification : les règles de la politique s'ajoutent.
	cfg.Classification.Paths = append(slices.Clone(cfg.Classification.Paths), pol.Classification.Paths...)
	cfg.Classification.Markers = append(slices.Clone(cfg.Classification.Markers), pol.Classification.Markers...)

	// Fournisseur local imposé : il doit exister et être local, sinon la
	// politique est refusée. Contrôlé après les define.
	if l := pol.Routing.Local; l != "" {
		if p, ok := cfg.Provider(l); !ok || p.Jurisdiction != "local" {
			return Config{}, fmt.Errorf("%w: routing.local must name a local provider", orgpolicy.ErrPolicy)
		}
		if cfg.Routing.Local != l {
			cfg.PolicyRoutingLocal = l
		}
		cfg.Routing.Local = l
	}

	// Budget : le plafond de la politique ne peut que baisser celui de la config.
	cfg.Budget.SessionTokens = lowestBudget(cfg.Budget.SessionTokens, pol.Budget.SessionTokens)
	cfg.finishBudget()

	// Mémoire : la politique ne fait que couper ou raccourcir.
	if pol.Memory.Disabled {
		off := false
		cfg.Memory.Enabled = &off
		cfg.MemoryDisabledBy = "policy"
	}
	if pol.Memory.NoProject {
		off := false
		cfg.Memory.Project = &off
	}
	if m := pol.Memory.MaxRetentionDays; m > 0 && m < cfg.Memory.Days() {
		cfg.Memory.RetentionDays = m
	}

	// ACP : la politique ne fait que couper les serveurs MCP de l'éditeur.
	if pol.ACP.NoEditorMCP {
		cfg.ACP.AcceptEditorMCP = false
	}

	// Confinement : la politique ne fait que durcir ; deny_read s'ajoute.
	cfg.Sandbox.Mode = stricterSandbox(cfg.Sandbox.Mode, pol.Sandbox.Mode)
	pd, err := expandSandboxPaths("deny_read", pol.Sandbox.DenyRead, cfg.home)
	if err != nil {
		return Config{}, err
	}
	cfg.Sandbox.DenyRead = append(slices.Clone(cfg.Sandbox.DenyRead), pd...)
	// Plancher du profil, connu seulement ici (la politique peut le relever).
	cfg.Sandbox.Mode = stricterSandbox(cfg.Sandbox.Mode, profile.SandboxMode(cfg.Profile))

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
