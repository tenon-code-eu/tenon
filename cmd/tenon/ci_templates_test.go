package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Contrôle textuel des modèles CI : aucun module YAML.
func TestCITemplates(t *testing.T) {
	versionRe := regexp.MustCompile(`TENON_VERSION=v[0-9]+\.[0-9]+\.[0-9]+`)
	files := map[string]string{
		"gitlab-ci.yml": "when: always",
		"forgejo.yml":   "if: always()",
		"github.yml":    "if: always()",
	}
	common := []string{
		"--strict",
		"--config ci/tenon.toml",
		"TENON_REQUIRE_SIGNATURE=1",
		"tenon audit verify",
		"tenon audit export --all-projects --out tenon-audit",
		"XDG_DATA_HOME",
		"tenon-events.jsonl",
		".tenon-data/tenon/audit",
	}
	for name, always := range files {
		t.Run(name, func(t *testing.T) {
			b, err := os.ReadFile(filepath.Join("..", "..", "docs", "ci", name))
			if err != nil {
				t.Fatal(err)
			}
			s := string(b)
			for _, want := range append(common, always) {
				if !strings.Contains(s, want) {
					t.Errorf("%s : %q absent", name, want)
				}
			}
			if !versionRe.MatchString(s) {
				t.Errorf("%s : TENON_VERSION=vX.Y.Z absent", name)
			}
			if strings.Contains(strings.ToLower(s), "latest") {
				t.Errorf("%s : « latest » interdit", name)
			}
		})
	}
}

// Le fichier de configuration d'exemple des modèles passe config check,
// avec la seule clé d'API dans l'environnement.
func TestCIExampleConfig(t *testing.T) {
	e := newEnv(t, nil, "eu")
	e.vars = map[string]string{
		"XDG_CONFIG_HOME": e.vars["XDG_CONFIG_HOME"],
		"XDG_DATA_HOME":   e.vars["XDG_DATA_HOME"],
		"HOME":            e.vars["HOME"],
		"MISTRAL_API_KEY": "test",
	}
	abs, err := filepath.Abs(filepath.Join("..", "..", "docs", "ci", "tenon.toml"))
	if err != nil {
		t.Fatal(err)
	}
	code, out, errb := e.run("--config", abs, "config", "check")
	if code != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errb)
	}
}
