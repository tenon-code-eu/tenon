package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/tenon-code-eu/tenon/internal/tsp/tsptest"
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
		"--config ci/tenon.toml audit verify",
		"--config ci/tenon.toml audit export --all-projects --out tenon-audit",
		"tenon-events.jsonl",
		"tenon-audit-log",
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
			if name != "gitlab-ci.yml" && !strings.Contains(s, "include-hidden-files: true") {
				t.Errorf("%s : include-hidden-files: true absent", name)
			}
			// Le journal d'audit reste hors de l'espace de travail (le sandbox
			// refuse le dossier de données) : il y est copié avant l'archivage.
			if strings.Contains(s, ".tenon-data") || strings.Contains(s, "XDG_DATA_HOME: ") {
				t.Errorf("%s : le dossier de données ne doit pas être dans l'espace de travail", name)
			}
			if strings.Contains(s, "| TENON_VERSION") {
				t.Errorf("%s : pas de curl | sh", name)
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

// La séquence des modèles CI (exécution, vérification, export) tourne sur un
// espace de travail temporaire, avec des chemins relatifs et une fausse TSA.
func TestCITemplateSequenceE2E(t *testing.T) {
	tsa := tsptest.NewServer(t, tsptest.Options{})
	f := newFakeLLM(t, sseText("ok"))
	ws := t.TempDir()
	// Les modèles utilisent des chemins relatifs au dossier courant.
	t.Chdir(ws)
	home := filepath.Join(ws, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	// Dossier de données hors de l'espace de travail, comme dans les modèles.
	data := t.TempDir()
	e := &env{
		t:  t,
		wd: ws,
		vars: map[string]string{
			"XDG_CONFIG_HOME": filepath.Join(ws, "xdg-config"),
			"XDG_DATA_HOME":   filepath.Join(data, "share"),
			"HOME":            home,
			"TEST_KEY":        "cle-de-test",
		},
		dataDir: filepath.Join(data, "share", "tenon"),
	}
	cfg := fmt.Sprintf(`lang = "en"
default_provider = "test"

[[providers]]
id = "test"
name = "Test SAS"
base_url = %q
api_key_env = "TEST_KEY"
model = "test-model"
country = "FR"
jurisdiction = "eu"
`, f.srv.URL) + timestampTOML(tsa.URL, writeCA(t, tsa.CA.PEM))
	e.writeFile(filepath.Join(ws, "ci", "tenon.toml"), cfg)

	code, out, errb := e.run("--config", "ci/tenon.toml", "-p", "x", "--strict", "--output", "json")
	if code != 0 {
		t.Fatalf("run : code=%d stdout=%q stderr=%q", code, out, errb)
	}
	code, out, errb = e.run("--config", "ci/tenon.toml", "audit", "verify")
	if code != 0 || !strings.Contains(out, "timestamps: 1 valid") {
		t.Fatalf("verify : code=%d stdout=%q stderr=%q", code, out, errb)
	}
	code, out, errb = e.run("--config", "ci/tenon.toml", "audit", "export", "--all-projects", "--out", "tenon-audit")
	if code != 0 {
		t.Fatalf("export : code=%d stdout=%q stderr=%q", code, out, errb)
	}
	for _, ext := range []string{"html", "json"} {
		m, _ := filepath.Glob(filepath.Join(ws, "tenon-audit", "*."+ext))
		if len(m) != 1 {
			t.Errorf("tenon-audit/*.%s : %v", ext, m)
		}
	}
	// Copie du journal dans l'espace de travail avant l'archivage.
	if err := os.CopyFS(filepath.Join(ws, "tenon-audit-log"), os.DirFS(filepath.Join(e.dataDir, "audit"))); err != nil {
		t.Fatal(err)
	}
	if m, _ := filepath.Glob(filepath.Join(ws, "tenon-audit-log", "*.jsonl")); len(m) == 0 {
		t.Error("tenon-audit-log/*.jsonl absent")
	}
}

// --config "" et --config= sont refusés.
func TestConfigEmptyRefused(t *testing.T) {
	e := newEnv(t, nil, "eu")
	for _, args := range [][]string{{"--config", "", "config", "check"}, {"--config=", "config", "check"}} {
		code, _, errb := e.run(args...)
		if code != 1 || !strings.Contains(errb, "config: --config requires a file") {
			t.Errorf("%v : code=%d stderr=%q", args, code, errb)
		}
	}
}

// audit export crée son dossier --out (imbriqué) en 0700.
func TestAuditExportCreatesOutDir(t *testing.T) {
	e := newEnv(t, newFakeLLM(t, sseText("ok")), "eu")
	if code, out, errb := e.run("-p", "x"); code != 0 {
		t.Fatalf("run : code=%d %q %q", code, out, errb)
	}
	code, out, errb := e.run("audit", "export", "--all-projects", "--out", "a/b/rapport")
	if code != 0 {
		t.Fatalf("export : code=%d stdout=%q stderr=%q", code, out, errb)
	}
	dir := filepath.Join(e.wd, "a", "b", "rapport")
	fi, err := os.Stat(dir)
	if err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("dossier : %v %v", fi, err)
	}
	m, _ := filepath.Glob(filepath.Join(dir, "tenon-audit-*"))
	if len(m) != 2 {
		t.Fatalf("fichiers : %v", m)
	}
}
