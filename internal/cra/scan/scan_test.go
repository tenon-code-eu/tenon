package scan

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tenon-code-eu/tenon/internal/cra/dossier"
	"github.com/tenon-code-eu/tenon/internal/cra/osv"
	"github.com/tenon-code-eu/tenon/internal/cra/sbom"
)

func mkDB(t *testing.T, docs map[string][]string) *osv.DB {
	t.Helper()
	dir := t.TempDir()
	index := map[string]any{}
	for eco, ds := range docs {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		for i, d := range ds {
			w, _ := zw.Create(string(rune('a'+i)) + ".json")
			_, _ = w.Write([]byte(d))
		}
		_ = zw.Close()
		_ = os.MkdirAll(filepath.Join(dir, eco), 0o755)
		_ = os.WriteFile(filepath.Join(dir, eco, "all.zip"), buf.Bytes(), 0o644)
		sum := sha256.Sum256(buf.Bytes())
		index[eco] = map[string]any{"date": "2026-10-20T10:00:00Z", "sha256": hex.EncodeToString(sum[:]), "entries": len(ds)}
	}
	b, _ := json.Marshal(index)
	_ = os.WriteFile(filepath.Join(dir, "index.json"), b, 0o644)
	db, err := osv.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

const npmDoc = `{"id":"GHSA-1","aliases":["CVE-2026-1"],"summary":"s","affected":[{"package":{"ecosystem":"npm","name":"left"},"ranges":[{"type":"SEMVER","events":[{"introduced":"0"},{"fixed":"2.0.0"}]}]}]}`
const npmDoc2 = `{"id":"GHSA-2","summary":"s","affected":[{"package":{"ecosystem":"npm","name":"left"},"ranges":[{"type":"SEMVER","events":[{"introduced":"0"},{"fixed":"3.0.0"}]}]}]}`
const mvnDoc = `{"id":"GHSA-3","summary":"s","affected":[{"package":{"ecosystem":"Maven","name":"org.x:y"},"versions":["1.0"]}]}`

func bom(purls ...string) sbom.BOM {
	var cs []sbom.Component
	for _, p := range purls {
		cs = append(cs, sbom.Component{Name: "n", PURL: p, BOMRef: p})
	}
	return sbom.BOM{Components: cs}
}

func statuses(r Result) map[string]string {
	m := map[string]string{}
	for _, f := range r.Findings {
		m[f.ID] = f.Status
	}
	return m
}

func TestScanVEX(t *testing.T) {
	db := mkDB(t, map[string][]string{"npm": {npmDoc, npmDoc2}})
	b := bom("pkg:npm/left@1.5.0")
	d := dossier.Dossier{}
	r, err := Run(d, []sbom.BOM{b}, db)
	if err != nil {
		t.Fatal(err)
	}
	if r.Open != 2 || r.SBOMs != 1 || r.Components != 1 || r.DBDate.IsZero() {
		t.Fatalf("%+v", r)
	}
	d.VEX = []dossier.VEXEntry{
		{ID: "CVE-2026-1", Ref: "pkg:npm/left", State: "not_affected", Justification: "j"}, // alias, sans version
		{ID: "GHSA-2", Ref: "pkg:npm/left@1.5.0?type=x", State: "affected"},                // reste open
	}
	r, _ = Run(d, []sbom.BOM{b}, db)
	if s := statuses(r); s["GHSA-1"] != "not_affected" || s["GHSA-2"] != "open" || r.Open != 1 || r.NotAffected != 1 {
		t.Fatalf("%v %+v", s, r)
	}
	// Ref versionnée sur une autre version : sans effet.
	d.VEX = []dossier.VEXEntry{{ID: "GHSA-1", Ref: "pkg:npm/left@1.2.3", State: "fixed"}}
	r, _ = Run(d, []sbom.BOM{b}, db)
	if r.Open != 2 || r.Fixed != 0 {
		t.Fatalf("%+v", r)
	}
	// bom-ref ; versionné précis l'emporte sur non versionné.
	d.VEX = []dossier.VEXEntry{
		{ID: "GHSA-1", Ref: "pkg:npm/left", State: "affected"},
		{ID: "GHSA-1", Ref: "pkg:npm/left@1.5.0", State: "fixed"},
		{ID: "GHSA-2", Ref: "pkg:npm/left@1.5.0", State: "under_investigation"},
	}
	r, _ = Run(d, []sbom.BOM{b}, db)
	if s := statuses(r); s["GHSA-1"] != "fixed" || s["GHSA-2"] != "open" || r.Fixed != 1 {
		t.Fatal(s)
	}
	d.VEX = []dossier.VEXEntry{{ID: "GHSA-2", Ref: "pkg:npm/left@1.5.0", State: "not_affected", Justification: "j"}}
	b.Components[0].BOMRef = "ref-1"
	d.VEX = []dossier.VEXEntry{{ID: "GHSA-2", Ref: "ref-1", State: "not_affected", Justification: "j"}}
	r, _ = Run(d, []sbom.BOM{b}, db)
	if statuses(r)["GHSA-2"] != "not_affected" {
		t.Fatal(statuses(r))
	}
}

func TestScanToVerify(t *testing.T) {
	db := mkDB(t, map[string][]string{"Maven": {mvnDoc}})
	r, err := Run(dossier.Dossier{}, []sbom.BOM{bom("pkg:maven/org.x/y@1.0")}, db)
	if err != nil {
		t.Fatal(err)
	}
	if r.ToVerify != 0 && r.Open != 1 {
		t.Fatalf("%+v", r)
	}
	// Version absente des listes, écosystème sans comparateur : to_verify.
	doc := `{"id":"GHSA-4","affected":[{"package":{"ecosystem":"Maven","name":"org.x:y"},"ranges":[{"type":"ECOSYSTEM","events":[{"introduced":"0"},{"fixed":"2"}]}]}]}`
	db = mkDB(t, map[string][]string{"Maven": {doc}})
	r, err = Run(dossier.Dossier{}, []sbom.BOM{bom("pkg:maven/org.x/y@1.5")}, db)
	if err != nil || r.ToVerify != 1 || r.Open != 0 {
		t.Fatalf("%+v %v", r, err)
	}
	// Le VEX peut trancher un to_verify.
	d := dossier.Dossier{VEX: []dossier.VEXEntry{{ID: "GHSA-4", Ref: "pkg:maven/org.x/y", State: "fixed"}}}
	r, _ = Run(d, []sbom.BOM{bom("pkg:maven/org.x/y@1.5")}, db)
	if r.ToVerify != 0 || r.Fixed != 1 {
		t.Fatalf("%+v", r)
	}
}

func TestScanNoArchive(t *testing.T) {
	db := mkDB(t, map[string][]string{"npm": {npmDoc}})
	r, err := Run(dossier.Dossier{}, []sbom.BOM{bom("pkg:npm/left@1.0.0", "pkg:pypi/requests@2.0")}, db)
	var nd *NoDBError
	if !errors.As(err, &nd) || nd.Ecosystem != "PyPI" || !errors.Is(err, osv.ErrNoArchive) {
		t.Fatalf("%v", err)
	}
	if err.Error() != "cra: no vulnerability database for PyPI (run tenon cra db update)" {
		t.Fatal(err)
	}
	if r.Open != 0 {
		t.Fatal("no clean result expected")
	}
	if _, err := Run(dossier.Dossier{}, nil, nil); err == nil {
		t.Fatal("nil db must error")
	}
}
