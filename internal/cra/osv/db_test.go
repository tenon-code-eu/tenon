package osv

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tenon-code-eu/tenon/internal/cra/sbom"
)

// fixture construit un répertoire de base OSV : <dir>/<ECO>/all.zip et
// <dir>/index.json. entries : écosystème → documents JSON OSV.
func fixture(t *testing.T, entries map[string][]string) string {
	t.Helper()
	dir := t.TempDir()
	index := map[string]any{}
	for eco, docs := range entries {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		for i, d := range docs {
			w, err := zw.Create("entry-" + string(rune('a'+i)) + ".json")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.Write([]byte(d)); err != nil {
				t.Fatal(err)
			}
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		writeArchive(t, dir, eco, buf.Bytes(), index)
	}
	writeIndex(t, dir, index)
	return dir
}

func writeArchive(t *testing.T, dir, eco string, data []byte, index map[string]any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, eco), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, eco, "all.zip"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	index[eco] = map[string]any{"date": "2026-10-20T10:00:00Z", "sha256": hex.EncodeToString(sum[:]), "entries": 1}
}

func writeIndex(t *testing.T, dir string, index map[string]any) {
	t.Helper()
	b, err := json.Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func open(t *testing.T, entries map[string][]string) *DB {
	t.Helper()
	db, err := Open(fixture(t, entries))
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func comp(purl string) sbom.Component {
	return sbom.Component{PURL: purl, BOMRef: purl}
}

// states renvoie ID → état des constats.
func states(t *testing.T, db *DB, c sbom.Component) map[string]string {
	t.Helper()
	fs, err := db.Match(c)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]string{}
	for _, f := range fs {
		if _, dup := m[f.ID]; dup {
			t.Fatalf("constat en double %s pour %s", f.ID, c.PURL)
		}
		m[f.ID] = f.State
	}
	return m
}

const semverRange = `{"id":"GO-2026-0001","aliases":["CVE-2026-1"],"summary":"bug","affected":[
 {"package":{"ecosystem":"Go","name":"github.com/foo/bar"},
  "ranges":[{"type":"SEMVER","events":[{"introduced":"0"},{"fixed":"1.2.0"}]}]}]}`

func TestMatchSemverRange(t *testing.T) {
	db := open(t, map[string][]string{"Go": {semverRange}})
	fs, err := db.Match(comp("pkg:golang/github.com/foo/bar@v1.1.9"))
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 1 || fs[0].ID != "GO-2026-0001" || fs[0].State != StateAffected ||
		fs[0].Summary != "bug" || len(fs[0].Aliases) != 1 || fs[0].Aliases[0] != "CVE-2026-1" ||
		fs[0].Component.PURL != "pkg:golang/github.com/foo/bar@v1.1.9" {
		t.Fatalf("1.1.9: %+v", fs)
	}
	for _, v := range []string{"v1.2.0", "v1.3.0"} {
		if m := states(t, db, comp("pkg:golang/github.com/foo/bar@"+v)); len(m) != 0 {
			t.Fatalf("%s : %v", v, m)
		}
	}
	// Autre paquet : aucun constat.
	if m := states(t, db, comp("pkg:golang/github.com/foo/baz@v1.0.0")); len(m) != 0 {
		t.Fatalf("autre paquet : %v", m)
	}
	// Pré-release et pseudo-version avant le correctif : affectées.
	for _, v := range []string{"v1.2.0-rc.1", "v1.1.10-0.20260101000000-abcdefabcdef"} {
		if m := states(t, db, comp("pkg:golang/github.com/foo/bar@"+v)); m["GO-2026-0001"] != StateAffected {
			t.Fatalf("%s : %v", v, m)
		}
	}
}

func TestMatchMultiplePairs(t *testing.T) {
	doc := `{"id":"X-1","affected":[{"package":{"ecosystem":"npm","name":"@scope/pkg"},
	 "ranges":[{"type":"ECOSYSTEM","events":[{"introduced":"0"},{"fixed":"1.0.0"},{"introduced":"2.0.0"},{"fixed":"2.5.0"},{"limit":"9.0.0"}]}]}]}`
	db := open(t, map[string][]string{"npm": {doc}})
	cases := map[string]bool{"0.9.0": true, "1.0.0": false, "1.5.0": false, "2.0.0": true, "2.4.9": true, "2.5.0": false, "3.0.0": false}
	for v, want := range cases {
		m := states(t, db, comp("pkg:npm/%40scope/pkg@"+v))
		if got := m["X-1"] == StateAffected; got != want {
			t.Fatalf("%s : %v, attendu affecté=%v", v, m, want)
		}
	}
}

func TestMatchLastAffected(t *testing.T) {
	doc := `{"id":"RUSTSEC-1","affected":[{"package":{"ecosystem":"crates.io","name":"foo"},
	 "ranges":[{"type":"SEMVER","events":[{"introduced":"0.5.0"},{"last_affected":"0.8.2"}]}]}]}`
	db := open(t, map[string][]string{"crates.io": {doc}})
	cases := map[string]bool{"0.4.9": false, "0.5.0": true, "0.8.2": true, "0.8.3": false}
	for v, want := range cases {
		m := states(t, db, comp("pkg:cargo/foo@"+v))
		if got := m["RUSTSEC-1"] == StateAffected; got != want {
			t.Fatalf("%s : %v, attendu affecté=%v", v, m, want)
		}
	}
}

func TestMatchVersionsList(t *testing.T) {
	doc := `{"id":"GHSA-maven","affected":[{"package":{"ecosystem":"Maven","name":"org.example:lib"},
	 "versions":["1.0.0","1.0.1"]}]}`
	db := open(t, map[string][]string{"Maven": {doc}})
	if m := states(t, db, comp("pkg:maven/org.example/lib@1.0.1?type=jar")); m["GHSA-maven"] != StateAffected {
		t.Fatalf("listée : %v", m)
	}
	// Liste explicite + plage : la liste suffit (Go, comparaison sans « v »).
	gdoc := `{"id":"GO-L","affected":[{"package":{"ecosystem":"Go","name":"example.com/m"},"versions":["0.3.0"],
	 "ranges":[{"type":"SEMVER","events":[{"introduced":"1.0.0"}]}]}]}`
	db = open(t, map[string][]string{"Go": {gdoc}})
	if m := states(t, db, comp("pkg:golang/example.com/m@v0.3.0")); m["GO-L"] != StateAffected {
		t.Fatalf("liste Go : %v", m)
	}
	if m := states(t, db, comp("pkg:golang/example.com/m@v0.4.0")); len(m) != 0 {
		t.Fatalf("hors liste et plage : %v", m)
	}
}

func TestMatchMultipleAffected(t *testing.T) {
	// Deux affected pour le même paquet (plages différentes) et un affected
	// pour un autre paquet ; une entrée de l'autre écosystème homonyme.
	doc := `{"id":"GO-2026-0002","affected":[
	 {"package":{"ecosystem":"Go","name":"stdlib"},"ranges":[{"type":"SEMVER","events":[{"introduced":"0"},{"fixed":"1.21.5"}]}]},
	 {"package":{"ecosystem":"Go","name":"stdlib"},"ranges":[{"type":"SEMVER","events":[{"introduced":"1.22.0"},{"fixed":"1.22.1"}]}]},
	 {"package":{"ecosystem":"Go","name":"golang.org/x/net"},"ranges":[{"type":"SEMVER","events":[{"introduced":"0"},{"fixed":"0.20.0"}]}]},
	 {"package":{"ecosystem":"npm","name":"golang.org/x/net"},"ranges":[{"type":"SEMVER","events":[{"introduced":"0"}]}]}]}`
	db := open(t, map[string][]string{"Go": {doc}})
	cases := []struct {
		purl string
		want bool
	}{
		{"pkg:golang/stdlib@v1.21.4", true},
		{"pkg:golang/stdlib@v1.21.5", false},
		{"pkg:golang/stdlib@v1.22.0", true},
		{"pkg:golang/stdlib@v1.22.1", false},
		{"pkg:golang/golang.org/x/net@v0.19.0", true},
		{"pkg:golang/golang.org/x/net@v0.20.0", false},
		// Plages du second paquet sans effet sur le premier.
		{"pkg:golang/stdlib@v0.19.0", true},
		{"pkg:golang/golang.org/x/net@v1.22.0", false},
	}
	for _, c := range cases {
		m := states(t, db, comp(c.purl))
		if got := m["GO-2026-0002"] == StateAffected; got != c.want || len(m) > 1 {
			t.Fatalf("%s : %v, attendu affecté=%v", c.purl, m, c.want)
		}
	}

	// Doublons dans le SBOM : un seul constat par (vulnérabilité, composant).
	cs := []sbom.Component{
		{Name: "stdlib", PURL: "pkg:golang/stdlib@v1.21.4", BOMRef: "a"},
		{Name: "stdlib", PURL: "pkg:golang/stdlib@v1.21.4?goos=linux", BOMRef: "b"},
		{Name: "net", PURL: "pkg:golang/golang.org/x/net@v0.19.0", BOMRef: "c"},
		{Name: "stdlib", PURL: "pkg:golang/stdlib@v1.22.0", BOMRef: "d"},
	}
	fs, err := db.MatchAll(cs)
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 3 {
		t.Fatalf("MatchAll : %+v", fs)
	}
	refs := map[string]bool{}
	for _, f := range fs {
		refs[f.Component.BOMRef] = true
	}
	if !refs["a"] || !refs["c"] || !refs["d"] || refs["b"] {
		t.Fatalf("MatchAll composants : %+v", fs)
	}
}

func TestMatchWithdrawn(t *testing.T) {
	doc := `{"id":"GO-W","withdrawn":"2026-01-01T00:00:00Z","affected":[{"package":{"ecosystem":"Go","name":"github.com/foo/bar"},
	 "ranges":[{"type":"SEMVER","events":[{"introduced":"0"}]}]}]}`
	db := open(t, map[string][]string{"Go": {doc, semverRange}})
	m := states(t, db, comp("pkg:golang/github.com/foo/bar@v1.0.0"))
	if _, ok := m["GO-W"]; ok || m["GO-2026-0001"] != StateAffected {
		t.Fatalf("retirée : %v", m)
	}
}

func TestMatchToVerify(t *testing.T) {
	doc := `{"id":"GHSA-mvn","affected":[{"package":{"ecosystem":"Maven","name":"org.example:lib"},
	 "versions":["1.0.0"],"ranges":[{"type":"ECOSYSTEM","events":[{"introduced":"0"},{"fixed":"2.0.0"}]}]}]}`
	db := open(t, map[string][]string{"Maven": {doc}})
	if m := states(t, db, comp("pkg:maven/org.example/lib@1.5.0")); m["GHSA-mvn"] != StateToVerify {
		t.Fatalf("Maven non listée : %v", m)
	}
	if m := states(t, db, comp("pkg:maven/org.example/lib@1.0.0")); m["GHSA-mvn"] != StateAffected {
		t.Fatalf("Maven listée : %v", m)
	}
}

func TestMatchToVerifyComparatorError(t *testing.T) {
	db := open(t, map[string][]string{"Go": {semverRange}})
	// Version invalide pour semver : à vérifier, jamais ignorée.
	if m := states(t, db, comp("pkg:golang/github.com/foo/bar@master")); m["GO-2026-0001"] != StateToVerify {
		t.Fatalf("version invalide : %v", m)
	}
	// Version absente : à vérifier.
	if m := states(t, db, comp("pkg:golang/github.com/foo/bar")); m["GO-2026-0001"] != StateToVerify {
		t.Fatalf("sans version : %v", m)
	}
	// Événement invalide dans l'entrée OSV.
	bad := `{"id":"GO-BAD","affected":[{"package":{"ecosystem":"Go","name":"github.com/foo/bar"},
	 "ranges":[{"type":"SEMVER","events":[{"introduced":"0"},{"fixed":"not-a-version"}]}]}]}`
	db = open(t, map[string][]string{"Go": {bad}})
	if m := states(t, db, comp("pkg:golang/github.com/foo/bar@v1.0.0")); m["GO-BAD"] != StateToVerify {
		t.Fatalf("événement invalide : %v", m)
	}
}

func TestMatchAffectedUnknown(t *testing.T) {
	// Seulement une plage GIT : impossible d'évaluer, à vérifier.
	doc := `{"id":"GO-GIT","affected":[{"package":{"ecosystem":"Go","name":"github.com/foo/bar"},
	 "ranges":[{"type":"GIT","repo":"https://x","events":[{"introduced":"0"},{"fixed":"abc"}]}]}]}`
	// Aucune plage ni version.
	doc2 := `{"id":"GO-NONE","affected":[{"package":{"ecosystem":"Go","name":"github.com/foo/bar"}}]}`
	// GIT ignoré quand une plage SEMVER existe.
	doc3 := `{"id":"GO-MIX","affected":[{"package":{"ecosystem":"Go","name":"github.com/foo/bar"},
	 "ranges":[{"type":"GIT","repo":"https://x","events":[{"introduced":"0"}]},{"type":"SEMVER","events":[{"introduced":"0"},{"fixed":"0.5.0"}]}]}]}`
	db := open(t, map[string][]string{"Go": {doc, doc2, doc3}})
	m := states(t, db, comp("pkg:golang/github.com/foo/bar@v1.0.0"))
	if m["GO-GIT"] != StateToVerify || m["GO-NONE"] != StateToVerify {
		t.Fatalf("inconnu : %v", m)
	}
	if _, ok := m["GO-MIX"]; ok {
		t.Fatalf("GIT non ignoré : %v", m)
	}
}

func TestMatchPyPINormalised(t *testing.T) {
	doc := `{"id":"PYSEC-1","affected":[{"package":{"ecosystem":"PyPI","name":"Foo_Bar.baz"},
	 "ranges":[{"type":"ECOSYSTEM","events":[{"introduced":"0"},{"fixed":"2.0"}]}]}]}`
	db := open(t, map[string][]string{"PyPI": {doc}})
	if m := states(t, db, comp("pkg:pypi/foo-bar-baz@1.9.post1")); m["PYSEC-1"] != StateAffected {
		t.Fatalf("normalisé : %v", m)
	}
	if m := states(t, db, comp("pkg:pypi/FOO.BAR_baz@1.0+local")); m["PYSEC-1"] != StateAffected {
		t.Fatalf("normalisé, label local : %v", m)
	}
	if m := states(t, db, comp("pkg:pypi/foo-bar-baz@2.0.0")); len(m) != 0 {
		t.Fatalf("corrigée : %v", m)
	}
	// Liste versions : égalité PEP 440 (1.0 == 1.0.0).
	ldoc := `{"id":"PYSEC-2","affected":[{"package":{"ecosystem":"PyPI","name":"qux"},"versions":["1.0"]}]}`
	db = open(t, map[string][]string{"PyPI": {ldoc}})
	if m := states(t, db, comp("pkg:pypi/qux@1.0.0")); m["PYSEC-2"] != StateAffected {
		t.Fatalf("égalité PEP 440 : %v", m)
	}
}

func TestMatchNoAttempt(t *testing.T) {
	db := open(t, map[string][]string{"Go": {semverRange}})
	for _, c := range []sbom.Component{
		{Name: "blob"},
		{Name: "x", PURL: "not a purl"},
		{Name: "y", PURL: "pkg:deb/debian/openssl@3.0.0"},
	} {
		fs, err := db.Match(c)
		if err != nil || fs != nil {
			t.Fatalf("%+v : %v, %v", c, fs, err)
		}
	}
}

func TestMatchMissingArchive(t *testing.T) {
	db := open(t, map[string][]string{"Go": {semverRange}})
	_, err := db.Match(comp("pkg:npm/left-pad@1.0.0"))
	if !errors.Is(err, ErrNoArchive) || !strings.Contains(err.Error(), "npm") {
		t.Fatalf("archive absente : %v", err)
	}
}

func TestOpenCorrupt(t *testing.T) {
	dir := t.TempDir()
	index := map[string]any{}
	writeArchive(t, dir, "PyPI", []byte("this is not a zip archive"), index)
	writeIndex(t, dir, index)
	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		_, err = db.Match(comp("pkg:pypi/foo@1.0"))
		if err == nil || !strings.Contains(err.Error(), "PyPI") {
			t.Fatalf("archive corrompue : %v", err)
		}
	}

	// Archive valide mais empreinte différente de l'index (troncature).
	dir = fixture(t, map[string][]string{"Go": {semverRange}})
	f, err := os.OpenFile(filepath.Join(dir, "Go", "all.zip"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write([]byte("x"))
	_ = f.Close()
	db, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Match(comp("pkg:golang/github.com/foo/bar@v1.0.0")); err == nil || !strings.Contains(err.Error(), "Go") {
		t.Fatalf("empreinte : %v", err)
	}

	// Entrée JSON invalide dans une archive saine.
	db = open(t, map[string][]string{"npm": {`{"id":`}})
	if _, err := db.Match(comp("pkg:npm/a@1.0.0")); err == nil || !strings.Contains(err.Error(), "npm") {
		t.Fatalf("JSON invalide : %v", err)
	}
}

func TestOpenIndex(t *testing.T) {
	if _, err := Open(t.TempDir()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("index absent : %v", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.json"), []byte("{"), 0o644); err == nil {
		if _, err := Open(dir); err == nil {
			t.Fatal("index invalide accepté")
		}
	}
	dir = t.TempDir()
	writeIndex(t, dir, map[string]any{
		"Go":   map[string]any{"date": "2026-10-20T10:00:00Z", "sha256": "", "entries": 3},
		"PyPI": map[string]any{"date": "2026-10-18T12:00:00+02:00", "sha256": "", "entries": 3},
	})
	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if d, ok := db.Date("Go"); !ok || !d.Equal(time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("Date Go : %v %v", d, ok)
	}
	if _, ok := db.Date("npm"); ok {
		t.Fatal("Date npm présente")
	}
	if d, ok := db.OldestDate(); !ok || !d.Equal(time.Date(2026, 10, 18, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("OldestDate : %v %v", d, ok)
	}
	dir = t.TempDir()
	writeIndex(t, dir, map[string]any{})
	db, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := db.OldestDate(); ok {
		t.Fatal("OldestDate sur index vide")
	}
	dir = t.TempDir()
	writeIndex(t, dir, map[string]any{"Go": map[string]any{"date": "yesterday"}})
	if _, err := Open(dir); err == nil || !strings.Contains(err.Error(), "Go") {
		t.Fatalf("date invalide : %v", err)
	}
}

func TestMatchUnsortedEvents(t *testing.T) {
	doc := `{"id":"X-U","affected":[{"package":{"ecosystem":"npm","name":"u"},
	 "ranges":[{"type":"SEMVER","events":[{"introduced":"2.0.0"},{"fixed":"2.5.0"},{"introduced":"0"},{"fixed":"1.0.0"}]}]}]}`
	doc2 := `{"id":"X-R","affected":[{"package":{"ecosystem":"npm","name":"r"},
	 "ranges":[{"type":"SEMVER","events":[{"fixed":"1.2.0"},{"introduced":"0"}]}]}]}`
	db := open(t, map[string][]string{"npm": {doc, doc2}})
	cases := map[string]bool{"0.5.0": true, "1.5.0": false, "2.2.0": true, "2.5.0": false}
	for v, want := range cases {
		if got := states(t, db, comp("pkg:npm/u@"+v))["X-U"] == StateAffected; got != want {
			t.Fatalf("u %s : affecté=%v, attendu %v", v, got, want)
		}
	}
	if m := states(t, db, comp("pkg:npm/r@1.5.0")); len(m) != 0 {
		t.Fatalf("r 1.5.0 : %v", m)
	}
	if m := states(t, db, comp("pkg:npm/r@1.1.0")); m["X-R"] != StateAffected {
		t.Fatalf("r 1.1.0 : %v", m)
	}
	// Événement invalide rencontré au tri : à vérifier.
	bad := `{"id":"X-B","affected":[{"package":{"ecosystem":"npm","name":"b"},
	 "ranges":[{"type":"SEMVER","events":[{"introduced":"bogus"},{"introduced":"0"},{"fixed":"1.0.0"}]}]}]}`
	db = open(t, map[string][]string{"npm": {bad}})
	if m := states(t, db, comp("pkg:npm/b@2.0.0")); m["X-B"] != StateToVerify {
		t.Fatalf("tri en erreur : %v", m)
	}
}

func TestMatchNuGetCaseInsensitive(t *testing.T) {
	doc := `{"id":"GHSA-nuget","affected":[{"package":{"ecosystem":"NuGet","name":"Newtonsoft.Json"},"versions":["12.0.1"]}]}`
	db := open(t, map[string][]string{"NuGet": {doc}})
	if m := states(t, db, comp("pkg:nuget/newtonsoft.json@12.0.1")); m["GHSA-nuget"] != StateAffected {
		t.Fatalf("NuGet casse : %v", m)
	}
}

func TestMatchVersionsOnlyNoComparator(t *testing.T) {
	// OSV : affecté = union de `versions` et des plages ; une liste seule est
	// complète, une version non listée n'est pas affectée.
	only := `{"id":"GHSA-only","affected":[{"package":{"ecosystem":"Maven","name":"org.example:lib"},"versions":["1.0.0"]}]}`
	withRange := `{"id":"GHSA-range","affected":[{"package":{"ecosystem":"Maven","name":"org.example:lib"},
	 "ranges":[{"type":"ECOSYSTEM","events":[{"introduced":"0"},{"fixed":"2.0.0"}]}]}]}`
	db := open(t, map[string][]string{"Maven": {only, withRange}})
	m := states(t, db, comp("pkg:maven/org.example/lib@1.5.0"))
	if _, ok := m["GHSA-only"]; ok || m["GHSA-range"] != StateToVerify || len(m) != 1 {
		t.Fatalf("Maven : %v", m)
	}
}

func TestMatchMalformedRange(t *testing.T) {
	noEvents := `{"id":"GO-E","affected":[{"package":{"ecosystem":"Go","name":"example.com/m"},
	 "ranges":[{"type":"SEMVER","events":[]}]}]}`
	noIntro := `{"id":"GO-I","affected":[{"package":{"ecosystem":"Go","name":"example.com/m"},
	 "ranges":[{"type":"SEMVER","events":[{"fixed":"2.0.0"}]}]}]}`
	db := open(t, map[string][]string{"Go": {noEvents, noIntro}})
	m := states(t, db, comp("pkg:golang/example.com/m@v1.0.0"))
	if m["GO-E"] != StateToVerify || m["GO-I"] != StateToVerify {
		t.Fatalf("plages mal formées : %v", m)
	}
}

func TestMatchGoStdlibPrefix(t *testing.T) {
	doc := `{"id":"GO-STD","affected":[
	 {"package":{"ecosystem":"Go","name":"stdlib"},"ranges":[{"type":"SEMVER","events":[{"introduced":"0"},{"fixed":"1.22.2"}]}]},
	 {"package":{"ecosystem":"Go","name":"toolchain"},"ranges":[{"type":"SEMVER","events":[{"introduced":"0"},{"fixed":"1.22.2"}]}]}]}`
	db := open(t, map[string][]string{"Go": {doc}})
	for _, p := range []string{"pkg:golang/stdlib@go1.22.1", "pkg:golang/toolchain@go1.22.1"} {
		if m := states(t, db, comp(p)); m["GO-STD"] != StateAffected {
			t.Fatalf("%s : %v", p, m)
		}
	}
	if m := states(t, db, comp("pkg:golang/stdlib@go1.22.2")); len(m) != 0 {
		t.Fatalf("stdlib corrigée : %v", m)
	}
}

// TestMatchGoStdFromCycloneDXGomod : cyclonedx-gomod -std nomme la
// bibliothèque standard « std » ; OSV l'indexe sous « stdlib ».
func TestMatchGoStdFromCycloneDXGomod(t *testing.T) {
	doc := `{"id":"GO-STD","affected":[
	 {"package":{"ecosystem":"Go","name":"stdlib"},"ranges":[{"type":"SEMVER","events":[{"introduced":"0"},{"fixed":"1.22.2"}]}]}]}`
	db := open(t, map[string][]string{"Go": {doc}})
	if m := states(t, db, comp("pkg:golang/std@go1.22.1?type=module")); m["GO-STD"] != StateAffected {
		t.Fatalf("std : %v", m)
	}
	if m := states(t, db, comp("pkg:golang/std@go1.22.2?type=module")); len(m) != 0 {
		t.Fatalf("std corrigée : %v", m)
	}
}

// TestLoadKeepsOnlySBOMNames : seules les entrées des paquets du SBOM sont
// gardées en mémoire ; l'appariement est inchangé et une entrée illisible,
// même d'un autre paquet, rend l'écosystème illisible.
func TestLoadKeepsOnlySBOMNames(t *testing.T) {
	doc := func(id, name string) string {
		return `{"id":"` + id + `","affected":[{"package":{"ecosystem":"npm","name":"` + name + `"},"ranges":[{"type":"SEMVER","events":[{"introduced":"0"},{"fixed":"2.0.0"}]}]}]}`
	}
	db := open(t, map[string][]string{"npm": {doc("A-1", "left"), doc("B-1", "right"), doc("C-1", "other"), doc("C-2", "other")}})
	fs, err := db.MatchAll([]sbom.Component{comp("pkg:npm/left@1.0.0"), comp("pkg:npm/left@1.5.0")})
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 2 || fs[0].ID != "A-1" || fs[1].ID != "A-1" {
		t.Fatalf("constats %+v", fs)
	}
	ix := db.ecos["npm"]
	if ix == nil || ix.kept != 1 || len(ix.byName) != 1 || ix.byName["left"] == nil {
		t.Fatalf("index : %+v", ix)
	}
	// Un nouveau nom relit l'archive (union des noms demandés).
	if m := states(t, db, comp("pkg:npm/right@1.0.0")); m["B-1"] != StateAffected {
		t.Fatalf("right : %v", m)
	}
	if ix := db.ecos["npm"]; ix.kept != 2 || ix.byName["other"] != nil {
		t.Fatalf("index après union : kept %d, noms %d", ix.kept, len(ix.byName))
	}
	// Échec fermé : une entrée illisible d'un autre paquet.
	db = open(t, map[string][]string{"npm": {doc("A-1", "left"), `{"id":"X","affected":[{"package":`}})
	if _, err := db.MatchAll([]sbom.Component{comp("pkg:npm/left@1.0.0")}); err == nil || !strings.Contains(err.Error(), "npm") {
		t.Fatalf("JSON invalide hors SBOM : %v", err)
	}
}
