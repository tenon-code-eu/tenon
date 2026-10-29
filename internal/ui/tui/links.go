package tui

import (
	"strconv"
	"strings"
)

// Liens de l'accueil (constantes : jamais une URL venue d'ailleurs).
const (
	linkDocs   = "https://tenon-code-eu.github.io/tenon/"
	linkGitHub = "https://github.com/tenon-code-eu/tenon"
	linkBug    = "https://github.com/tenon-code-eu/tenon/issues/new"
)

// vteHyperlinks est la première version de VTE qui gère OSC 8.
const vteHyperlinks = 5000

// hyperlink rend text en hyperlien OSC 8 vers url si enabled, en texte brut
// sinon.
func hyperlink(url, text string, enabled bool) string {
	if !enabled {
		return text
	}
	return "\x1b]8;;" + url + "\x1b\\" + text + "\x1b]8;;\x1b\\"
}

// supportsHyperlinks indique si le terminal décrit par l'environnement gère
// les hyperliens OSC 8 : iTerm2, WezTerm, VS Code, Ghostty, kitty, VTE ≥ 0.50 ;
// jamais avec TERM=dumb.
func supportsHyperlinks(getenv func(string) string) bool {
	if getenv("TERM") == "dumb" {
		return false
	}
	switch getenv("TERM_PROGRAM") {
	case "iTerm.app", "WezTerm", "vscode", "ghostty":
		return true
	}
	if getenv("KITTY_WINDOW_ID") != "" {
		return true
	}
	if v, err := strconv.Atoi(strings.TrimSpace(getenv("VTE_VERSION"))); err == nil && v >= vteHyperlinks {
		return true
	}
	return false
}
