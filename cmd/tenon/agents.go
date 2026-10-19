package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/tenon-code-eu/tenon/internal/agents"
	"github.com/tenon-code-eu/tenon/internal/i18n"
)

// agentsBudget est la taille maximale de la section « agents » du prompt.
const agentsBudget = 4000

// discoveredAgents est le résultat de discoverAgents.
type discoveredAgents struct {
	cat     *agents.Catalog
	section string
	notices []string
}

// discoverAgents découvre les agents de l'utilisateur et du projet wd.
// notices (déjà traduits et nettoyés) : avis de découverte, bannière (s'il y
// a des fichiers d'agents), agents omis du prompt.
func discoverAgents(lang i18n.Lang, getenv func(string) string, wd string) discoveredAgents {
	cat, disc := agents.Discover(lang, agents.Dirs(getenv, wd))
	var notices []string
	for _, n := range disc {
		notices = append(notices, clean(string(n)))
	}
	if total, project := cat.FileCount(); total > 0 {
		notices = append(notices, i18n.T(lang, "agents.banner", total, project))
	}
	section, dropped := cat.PromptSection(lang, agentsBudget)
	if dropped > 0 {
		notices = append(notices, i18n.T(lang, "agents.dropped", dropped))
	}
	return discoveredAgents{cat: cat, section: section, notices: notices}
}

// listAgents affiche « tenon agents » : un agent par ligne, puis les avis.
func listAgents(lang i18n.Lang, stdout io.Writer, getenv func(string) string, wd string) int {
	cat, disc := agents.Discover(lang, agents.Dirs(getenv, wd))
	for _, a := range cat.List() {
		desc := strings.Join(strings.Fields(clean(a.Description)), " ")
		line := clean(a.Name) + "  " + string(a.Source)
		if a.Source != agents.Builtin {
			line += "  " + clean(a.Path)
			if a.Truncated {
				line += " (truncated)"
			}
		}
		line += "  " + desc
		switch {
		case a.Tools == nil:
			line += "  tools: all"
		case len(a.Tools) == 0:
			line += "  tools: none"
		default:
			line += "  tools: " + clean(strings.Join(a.Tools, ", "))
		}
		if a.Model != "" {
			line += "  model: " + clean(a.Model)
		}
		fmt.Fprintln(stdout, line)
	}
	for _, n := range disc {
		fmt.Fprintln(stdout, clean(string(n)))
	}
	return 0
}
