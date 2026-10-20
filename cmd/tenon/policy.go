package main

import (
	"errors"
	"fmt"
	"io"

	"github.com/tenon-code-eu/tenon/internal/i18n"
	"github.com/tenon-code-eu/tenon/internal/orgpolicy"
)

// policyCheck implémente « tenon policy check [dir] » : vérifie la signature
// et le contenu d'une politique, sans lire la configuration utilisateur ni
// projet. args vaut [] (répertoire système) ou [dir].
func policyCheck(lang i18n.Lang, args []string, stdout, stderr io.Writer) int {
	if len(args) > 1 {
		fmt.Fprintln(stderr, i18n.T(lang, "usage"))
		return 1
	}
	dir := orgpolicy.SystemDir()
	if len(args) == 1 {
		dir = args[0]
	}
	// Pas de contrôle de propriétaire : l'outil sert aussi à valider une
	// politique avant déploiement.
	pol, err := orgpolicy.Load(dir, false)
	if err != nil {
		fmt.Fprintln(stderr, i18n.T(lang, "err.prefix", errors.New(i18n.T(lang, "err.policy_load", err))))
		return 1
	}
	if pol == nil {
		fmt.Fprintln(stderr, i18n.T(lang, "err.prefix", i18n.T(lang, "policy.check_absent", dir)))
		return 1
	}
	fmt.Fprintln(stdout, i18n.T(lang, "config.policy", pol.ID, pol.Version, pol.KeyID))
	prof := i18n.T(lang, "policy.check_none")
	if pol.Profile != "" {
		prof = string(pol.Profile)
	}
	fmt.Fprintln(stdout, i18n.T(lang, "policy.check_profile", prof))
	if pr := pol.Providers; pr != nil {
		local := i18n.T(lang, "policy.check_no")
		if pr.AllowLocal {
			local = i18n.T(lang, "policy.check_yes")
		}
		fmt.Fprintln(stdout, i18n.T(lang, "policy.check_providers", len(pr.AllowHosts), local, len(pr.Define)))
	} else {
		fmt.Fprintln(stdout, i18n.T(lang, "policy.check_providers_none"))
	}
	fmt.Fprintln(stdout, i18n.T(lang, "policy.check_rules", len(pol.Rules)))
	fmt.Fprintln(stdout, i18n.T(lang, "policy.check_redaction", len(pol.Redaction)))
	audit := pol.AuditLevel
	if audit == "" {
		audit = i18n.T(lang, "policy.check_audit_none")
	}
	fmt.Fprintln(stdout, i18n.T(lang, "policy.check_audit", audit))
	mode := pol.MCP.Mode
	if mode == "" {
		mode = "any"
	}
	fmt.Fprintln(stdout, i18n.T(lang, "policy.check_mcp", mode))
	return 0
}
