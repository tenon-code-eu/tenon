package config

import (
	"os"
	"testing"
)

// TestConfigExampleLoads vérifie que l'exemple de la documentation se charge
// avec la version courante et illustre ce qu'il annonce.
func TestConfigExampleLoads(t *testing.T) {
	const path = "../../docs/config.example.toml"
	// Load ignore un fichier absent : son existence est vérifiée à part.
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path, "", env(nil))
	if err != nil {
		t.Fatalf("%s : %v", path, err)
	}
	if len(cfg.MCP.Servers) == 0 {
		t.Fatal("aucun serveur MCP dans l'exemple")
	}
	transports := map[string]bool{}
	for _, s := range cfg.MCP.Servers {
		transports[s.Transport] = true
	}
	if !transports["stdio"] || !transports["http"] {
		t.Fatalf("l'exemple doit déclarer un serveur stdio et un serveur http : %v", transports)
	}
	deny := false
	for _, r := range cfg.Permissions.Rules {
		deny = deny || r.Decision == "deny"
	}
	if !deny {
		t.Fatal("aucune règle deny dans l'exemple")
	}
	if len(cfg.Redaction.Rules) == 0 {
		t.Fatal("aucune règle de masquage dans l'exemple")
	}
	if len(cfg.Providers) <= len(presets()) {
		t.Fatal("aucun fournisseur personnalisé dans l'exemple")
	}
}
