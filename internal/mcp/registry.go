// Ce fichier, sans contrainte de compilation, porte le registre en lecture
// seule des outils exposés : l'interface le consulte sur toute plateforme.

package mcp

import (
	"slices"
	"sync"

	"github.com/tenon-code-eu/tenon/internal/config"
	"github.com/tenon-code-eu/tenon/internal/llm"
)

// ServerInfo décrit le serveur d'un outil MCP exposé (valeurs de la configuration).
type ServerInfo struct {
	ID           string
	Jurisdiction llm.Jurisdiction
	Country      string
}

// Lookup renvoie le serveur d'un outil MCP exposé.
func Lookup(toolName string) (ServerInfo, bool) {
	regMu.RLock()
	defer regMu.RUnlock()
	e, ok := registry[toolName]
	return ServerInfo{ID: e.server, Jurisdiction: e.jurisdiction, Country: e.country}, ok
}

// Registre global (lecture seule hors Start) : nom d'outil → serveur, juridiction.
type regEntry struct {
	server       string
	jurisdiction llm.Jurisdiction
	country      string
}

var (
	regMu    sync.RWMutex
	registry = map[string]regEntry{}
)

// registerTools remplace les entrées des serveurs déclarés par entries.
func registerTools(servers []config.MCPServer, entries map[string]regEntry) {
	regMu.Lock()
	defer regMu.Unlock()
	for name, e := range registry {
		if slices.ContainsFunc(servers, func(s config.MCPServer) bool { return s.ID == e.server }) {
			delete(registry, name)
		}
	}
	for name, e := range entries {
		registry[name] = e
	}
}

// Describe renvoie le serveur et la juridiction d'un outil MCP exposé.
func Describe(toolName string) (server string, jurisdiction llm.Jurisdiction, ok bool) {
	regMu.RLock()
	defer regMu.RUnlock()
	e, ok := registry[toolName]
	return e.server, e.jurisdiction, ok
}
