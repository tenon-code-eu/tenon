package config

import (
	"os"
	"path/filepath"
	"strings"
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
	// Le bloc [cra] est commenté : il doit rester valable une fois décommenté.
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	in := false
	for _, l := range strings.Split(string(b), "\n") {
		switch {
		case l == "# [cra]":
			in = true
			out = append(out, "[cra]")
		case in && strings.HasPrefix(l, "# "):
			out = append(out, strings.TrimPrefix(l, "# "))
		default:
			in = false
		}
	}
	if len(out) < 2 {
		t.Fatal("bloc [cra] absent de l'exemple")
	}
	tmp := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(tmp, []byte(strings.Join(out, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c2, err := Load(tmp, "", env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c2.CRA.OSVMirror == "" {
		t.Fatal("osv_mirror non lu")
	}
}
