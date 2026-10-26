package acp

import (
	"net/url"
	"regexp"
	"slices"

	"github.com/tenon-code-eu/tenon/internal/config"
	"github.com/tenon-code-eu/tenon/internal/i18n"
)

// editorIDPattern reprend la forme des identifiants de [[mcp.servers]].
var editorIDPattern = regexp.MustCompile(`^[a-z0-9_-]{1,32}$`)

// EditorMCP convertit les serveurs MCP transmis par l'éditeur en
// config.MCPServer, sans en croire les valeurs : stdio devient local sans
// risque déclaré, http devient non-eu sauf hôte local. Un serveur sse, mal
// formé ou dont l'identifiant existe déjà (la configuration l'emporte) est
// ignoré avec une notice. Les valeurs d'environnement et d'en-têtes de
// l'éditeur ne sont ni conservées ni journalisées : config.MCPServer ne
// connaît que des noms de variables du processus.
func EditorMCP(in []MCPServer, existing []config.MCPServer, lang i18n.Lang) (out []config.MCPServer, notices []string) {
	seen := map[string]bool{}
	for _, s := range existing {
		seen[s.ID] = true
	}
	for _, s := range in {
		if s.Type == "sse" {
			notices = append(notices, i18n.T(lang, "acp.mcp_sse", s.Name))
			continue
		}
		if !editorIDPattern.MatchString(s.Name) {
			notices = append(notices, i18n.T(lang, "acp.mcp_invalid", s.Name))
			continue
		}
		var d config.MCPServer
		switch {
		case s.Type == "http" || s.URL != "":
			u, err := url.Parse(s.URL)
			if s.URL == "" || err != nil || u.Hostname() == "" || (s.Type != "" && s.Type != "http") {
				notices = append(notices, i18n.T(lang, "acp.mcp_invalid", s.Name))
				continue
			}
			d = config.MCPServer{ID: s.Name, Transport: "http", URL: s.URL, Jurisdiction: "non-eu"}
			if slices.Contains([]string{"localhost", "127.0.0.1", "::1"}, u.Hostname()) {
				d.Jurisdiction = "local"
			}
		case s.Type == "" && s.Command != "":
			d = config.MCPServer{
				ID: s.Name, Transport: "stdio", Jurisdiction: "local",
				Command: append([]string{s.Command}, s.Args...),
			}
		default:
			notices = append(notices, i18n.T(lang, "acp.mcp_invalid", s.Name))
			continue
		}
		if seen[s.Name] {
			notices = append(notices, i18n.T(lang, "acp.mcp_clash", s.Name))
			continue
		}
		seen[s.Name] = true
		if len(s.Env) > 0 || len(s.Headers) > 0 {
			notices = append(notices, i18n.T(lang, "acp.mcp_values", s.Name))
		}
		out = append(out, d)
	}
	return out, notices
}
