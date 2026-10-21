package compliance

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "réécrit les fichiers golden")

// fixedReport construit à la main un rapport déterministe.
func fixedReport() Report {
	t0 := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	return Report{
		Generator:   "tenon test",
		GeneratedAt: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC),
		Period:      Period{Since: "2026-10-01", Until: "2026-10-20"},
		Scope:       Scope{Kind: "project", Project: "/work/demo"},
		Disclaimer:  Disclaimer,
		Integrity: Integrity{
			Intact: true, TotalLines: 4, FinalHash: "sha256:aa",
			RangeLines: 4, FirstHash: "sha256:bb", LastHash: "sha256:aa", Level: "metadata",
			Timestamps: Timestamps{LinesAfter: 4, Errors: []string{}},
		},
		Summary: Summary{Sessions: 1, Turns: 2, ToolCalls: 3, FilesModified: 1, Jurisdictions: []string{"EU"}},
		Providers: []ProviderUse{{
			ID: "p", Name: "Prov", Model: "m", Country: "FR", Jurisdiction: "EU", Qualifications: []string{},
			Sessions: 1, First: t0, Last: t0.Add(time.Hour),
		}},
		Profiles:    []string{"eu-only"},
		Policies:    []PolicyUse{},
		Permissions: []ToolPermissions{{Tool: "bash", Requested: 2, AllowedOnce: 2}},
		Files: []FileChange{{
			TS: t0, Session: "s1", Path: "a.go", Tool: "edit",
			HashBefore: "sha256:01", HashAfter: "sha256:02", Author: "main",
		}},
		Mapping: []MappingRow{{Element: "Integrity", Frameworks: []string{"X"}}},
		GitHint: GitHint,
	}
}

func TestRenderJSONGolden(t *testing.T) {
	r := fixedReport()
	id, err := ReportID(r)
	if err != nil {
		t.Fatal(err)
	}
	got, err := RenderJSON(r, id, "sha256:cc")
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "report.golden.json")
	if *update {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("sortie différente du golden (relancer avec -update)\n%s", got)
	}
}

func TestReportIDStableAndSensitive(t *testing.T) {
	r := fixedReport()
	a, err := ReportID(r)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := ReportID(r)
	if a != b {
		t.Errorf("id instable: %s != %s", a, b)
	}
	if !strings.HasPrefix(a, "sha256:") || len(a) != len("sha256:")+64 {
		t.Errorf("format id: %q", a)
	}
	r.Summary.Turns++
	c, _ := ReportID(r)
	if c == a {
		t.Error("id inchangé après modification d'un compte")
	}
}

func TestRenderJSONEnvelope(t *testing.T) {
	r := fixedReport()
	id, _ := ReportID(r)
	out, err := RenderJSON(r, id, "sha256:cc")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(out, []byte("}\n")) {
		t.Error("saut de ligne final manquant")
	}
	var env struct {
		SchemaVersion int             `json:"schema_version"`
		Generator     string          `json:"generator"`
		ReportID      string          `json:"report_id"`
		HTMLSHA256    string          `json:"html_sha256"`
		Report        json.RawMessage `json:"report"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatal(err)
	}
	if env.SchemaVersion != 1 || env.Generator != r.Generator || env.ReportID != id || env.HTMLSHA256 != "sha256:cc" {
		t.Errorf("enveloppe: %+v", env)
	}
	var back Report
	if err := json.Unmarshal(env.Report, &back); err != nil {
		t.Fatal(err)
	}
	again, _ := ReportID(back)
	if again != id {
		t.Errorf("rapport relu: id %s, attendu %s", again, id)
	}
	// Ordre des clés de l'enveloppe.
	last := -1
	for _, k := range []string{`"schema_version"`, `"generator"`, `"report_id"`, `"html_sha256"`, `"report"`} {
		i := bytes.Index(out, []byte(k))
		if i <= last {
			t.Errorf("clé %s hors ordre", k)
		}
		last = i
	}
}
