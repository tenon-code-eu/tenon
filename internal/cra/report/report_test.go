package report

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tenon-code-eu/tenon/internal/cra/dossier"
	"github.com/tenon-code-eu/tenon/internal/cra/osv"
	"github.com/tenon-code-eu/tenon/internal/cra/sbom"
	"github.com/tenon-code-eu/tenon/internal/cra/scan"
	"github.com/tenon-code-eu/tenon/internal/i18n"
)

var update = flag.Bool("update", false, "réécrit les fichiers golden")

var fixedNow = time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)

func tp(t time.Time) *time.Time { return &t }

// fixedDossier est un dossier complet aux valeurs fixes.
func fixedDossier() Dossier {
	aware := time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)
	return Dossier{
		Product: dossier.Product{
			Name: "Widget <1>", Version: "1.2.0", Manufacturer: "Acme SAS", SecurityContact: "security@acme.eu",
			Category: "important-1", SupportEnd: time.Date(2031, 12, 31, 0, 0, 0, 0, time.UTC),
			SBOMs: []string{"cra/sbom*.cdx.json"},
		},
		BOMs: []BOMSummary{{File: "cra/sbom.cdx.json", SpecVersion: "1.5", Generator: "syft 1.0", Timestamp: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC), Components: 3}},
		Scan: &scan.Result{
			SBOMs: 1, Components: 3, Open: 1, ToVerify: 1, NotAffected: 0, Fixed: 1, Conflicts: 1, Unchecked: 1,
			DBDate: time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC),
			Findings: []scan.Finding{
				{Finding: osv.Finding{ID: "GHSA-1", Summary: "prototype <pollution>", Component: sbom.Component{Name: "left", Version: "1.0.0", PURL: "pkg:npm/left@1.0.0"}}, Status: scan.StatusOpen},
				{Finding: osv.Finding{ID: "GHSA-2", Summary: "old bug", Component: sbom.Component{Name: "right", Version: "1.0.0", PURL: "pkg:npm/right@1.0.0"}}, Status: scan.StatusFixed, Conflict: true},
			},
			UncheckedComponents: []sbom.Component{{Name: "nopurl", Version: "1"}},
		},
		Reports: []ReportView{{
			ID: "2026-10-20-1", Kind: "vulnerability", Summary: "exploited flaw", AwareAt: tp(aware),
			EarlyWarningAt: tp(aware.Add(2 * time.Hour)),
			Deadlines: []DeadlineView{
				{Step: "early-warning", Due: tp(aware.Add(24 * time.Hour)), Done: tp(aware.Add(2 * time.Hour)), Status: "done"},
				{Step: "notification", Due: tp(aware.Add(72 * time.Hour)), Status: "overdue"},
				{Step: "final", Status: "unknown"},
			},
		}},
		Docs:        []Doc{{Name: "architecture.md", Content: "# Design\n<script>alert(1)</script>\n"}},
		GeneratedAt: fixedNow,
	}
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s : sortie différente du golden (relancer avec -update)\n%s", name, got)
	}
}

func TestWriteGolden(t *testing.T) {
	for _, lang := range []i18n.Lang{i18n.EN, i18n.FR} {
		dir := filepath.Join(t.TempDir(), "out")
		if err := Write(dir, fixedDossier(), lang); err != nil {
			t.Fatal(err)
		}
		j, err := os.ReadFile(filepath.Join(dir, "cra-dossier.json"))
		if err != nil {
			t.Fatal(err)
		}
		h, err := os.ReadFile(filepath.Join(dir, "cra-dossier.html"))
		if err != nil {
			t.Fatal(err)
		}
		golden(t, "golden.json", j)
		golden(t, "golden."+string(lang)+".html", h)
		for _, f := range []string{"cra-dossier.json", "cra-dossier.html"} {
			st, err := os.Stat(filepath.Join(dir, f))
			if err != nil || st.Mode().Perm() != 0o600 {
				t.Fatalf("%s : %v %v", f, st, err)
			}
		}
		if st, _ := os.Stat(dir); st.Mode().Perm() != 0o700 {
			t.Fatalf("dossier de sortie en %v", st.Mode().Perm())
		}
	}
}

func TestWriteScanNull(t *testing.T) {
	d := fixedDossier()
	d.Scan = nil
	dir := t.TempDir()
	if err := Write(dir, d, i18n.EN); err != nil {
		t.Fatal(err)
	}
	j, _ := os.ReadFile(filepath.Join(dir, "cra-dossier.json"))
	if !strings.Contains(string(j), `"scan": null`) {
		t.Fatalf("scan non nul : %s", j)
	}
	h, _ := os.ReadFile(filepath.Join(dir, "cra-dossier.html"))
	if !strings.Contains(string(h), "no vulnerability scan") {
		t.Fatalf("avis d'absence de scan manquant")
	}
}

func TestWriteEscapesDocs(t *testing.T) {
	dir := t.TempDir()
	if err := Write(dir, fixedDossier(), i18n.EN); err != nil {
		t.Fatal(err)
	}
	h, _ := os.ReadFile(filepath.Join(dir, "cra-dossier.html"))
	s := string(h)
	if strings.Contains(s, "<script>alert(1)</script>") || !strings.Contains(s, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Fatalf("doc non échappé")
	}
	if strings.Contains(s, "<1>") || strings.Contains(s, "<pollution>") {
		t.Fatalf("champ non échappé")
	}
}

const buildProduct = `name = "P"
version = "1.0"
manufacturer = "M"
security_contact = "s@x.eu"
support_end = 2031-12-31
category = "default"
`

func TestBuild(t *testing.T) {
	root := t.TempDir()
	w := func(rel, data string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	w("cra/product.toml", buildProduct)
	w("cra/sbom.cdx.json", `{"bomFormat":"CycloneDX","specVersion":"1.5","metadata":{"timestamp":"2026-10-01T09:00:00Z"},"components":[{"name":"a","version":"1","purl":"pkg:npm/a@1"},{"name":"b","version":"1"}]}`)
	w("cra/docs/b.md", "second")
	w("cra/docs/a.md", "first")
	w("cra/docs/notes.txt", "ignored")
	w("cra/reports/2026-10-20-1.toml", "kind = \"vulnerability\"\nsummary = \"s\"\naware_at = 2026-10-20T08:00:00Z\n")
	d, err := dossier.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Build(d, nil, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.BOMs) != 1 || got.BOMs[0].File != "cra/sbom.cdx.json" || got.BOMs[0].Components != 2 || got.BOMs[0].SpecVersion != "1.5" {
		t.Fatalf("boms %+v", got.BOMs)
	}
	if len(got.Docs) != 2 || got.Docs[0].Name != "a.md" || got.Docs[0].Content != "first" || got.Docs[1].Name != "b.md" {
		t.Fatalf("docs %+v", got.Docs)
	}
	if len(got.Reports) != 1 || got.Reports[0].ID != "2026-10-20-1" || len(got.Reports[0].Deadlines) != 3 || got.Reports[0].Deadlines[1].Status != "overdue" {
		t.Fatalf("reports %+v", got.Reports)
	}
	if got.Scan != nil || !got.GeneratedAt.Equal(fixedNow) || got.Product.Name != "P" {
		t.Fatalf("%+v", got)
	}
}
