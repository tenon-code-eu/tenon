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
	// Les blocs commentés doivent rester valables une fois décommentés.
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		table string
		check func(Config) bool
	}{
		{"cra", func(c Config) bool { return c.CRA.OSVMirror != "" }},
		{"budget", func(c Config) bool { return c.Budget.SessionTokens > 0 && c.Budget.WarnAt == 0.8 }},
		{"usage", func(c Config) bool { return c.Usage.WhPerMTok > 0 && c.Usage.GCO2PerKWh > 0 }},
		{"routing", func(c Config) bool { return c.Routing.Compaction == "ollama" && c.Routing.Subagents == "ollama" }},
	} {
		var out []string
		in := false
		for _, l := range strings.Split(string(b), "\n") {
			switch {
			case l == "# ["+c.table+"]":
				in = true
				out = append(out, "["+c.table+"]")
			case in && strings.HasPrefix(l, "# "):
				out = append(out, strings.TrimPrefix(l, "# "))
			default:
				in = false
			}
		}
		if len(out) < 2 {
			t.Fatalf("bloc [%s] absent de l'exemple", c.table)
		}
		tmp := filepath.Join(t.TempDir(), "config.toml")
		if err := os.WriteFile(tmp, []byte(strings.Join(out, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		c2, err := Load(tmp, "", env(nil))
		if err != nil {
			t.Fatalf("[%s] : %v", c.table, err)
		}
		if !c.check(c2) {
			t.Fatalf("bloc [%s] non lu : %+v", c.table, c2)
		}
	}
}
