package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tenon-code-eu/tenon/internal/audit"
	"github.com/tenon-code-eu/tenon/internal/cra/osv"
	"github.com/tenon-code-eu/tenon/internal/event"
)

const craProduct = `name = "P"
version = "1.0"
manufacturer = "M"
security_contact = "s@x.eu"
support_end = 2031-12-31
category = "default"
`

// craLeftDoc : left < 2.0.0 est vulnérable (npm).
const craLeftDoc = `{"id":"GHSA-1","aliases":["CVE-2026-1"],"summary":"prototype pollution","affected":[{"package":{"ecosystem":"npm","name":"left"},"ranges":[{"type":"SEMVER","events":[{"introduced":"0"},{"fixed":"2.0.0"}]}]}]}`

// craSBOM renvoie un SBOM CycloneDX 1.5 des purls donnés ; "" : composant sans purl.
func craSBOM(purls ...string) string {
	var cs []string
	for i, p := range purls {
		if p == "" {
			cs = append(cs, fmt.Sprintf(`{"name":"nopurl%d","version":"1"}`, i))
			continue
		}
		cs = append(cs, fmt.Sprintf(`{"name":"c%d","version":"1","purl":%q,"bom-ref":%q}`, i, p, p))
	}
	return `{"bomFormat":"CycloneDX","specVersion":"1.5","version":1,"components":[` + strings.Join(cs, ",") + `]}`
}

// craDossier écrit cra/product.toml et cra/sbom.cdx.json dans le projet.
func (e *env) craDossier(purls ...string) {
	e.t.Helper()
	e.writeFile(filepath.Join(e.wd, "cra", "product.toml"), craProduct)
	e.writeFile(filepath.Join(e.wd, "cra", "sbom.cdx.json"), craSBOM(purls...))
}

// craZip renvoie une archive all.zip contenant docs.
func craZip(t *testing.T, docs ...string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for i, d := range docs {
		w, err := zw.Create(fmt.Sprintf("e%d.json", i))
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
	return buf.Bytes()
}

// craDB écrit la base OSV locale <data>/cra/osv : écosystème → documents, datée de date.
func (e *env) craDB(date time.Time, docs map[string][]string) {
	e.t.Helper()
	dir := filepath.Join(e.dataDir, "cra", "osv")
	index := map[string]any{}
	for eco, ds := range docs {
		data := craZip(e.t, ds...)
		e.writeFile(filepath.Join(dir, eco, "all.zip"), string(data))
		sum := sha256.Sum256(data)
		index[eco] = map[string]any{"date": date.UTC().Format(time.RFC3339), "sha256": hex.EncodeToString(sum[:]), "entries": len(ds)}
	}
	b, err := json.Marshal(index)
	if err != nil {
		e.t.Fatal(err)
	}
	e.writeFile(filepath.Join(dir, "index.json"), string(b))
}

// auditLines renvoie les lignes du journal d'audit de l'environnement.
func (e *env) auditLines() []audit.Line {
	e.t.Helper()
	var out []audit.Line
	err := audit.ReadLines(filepath.Join(e.dataDir, "audit"), func(_ string, _ int, l audit.Line, err error) error {
		if err != nil {
			return err
		}
		out = append(out, l)
		return nil
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return out
}

// craEvents renvoie les charges des événements du type donné, et vérifie que
// chaque session CRA commence par SessionStarted{kind:"cra"}.
func craEvents[T any](t *testing.T, lines []audit.Line, typ event.Type) []T {
	t.Helper()
	started := map[string]bool{}
	var out []T
	for _, l := range lines {
		if l.Type == event.SessionStarted {
			var p event.SessionStartedPayload
			if err := json.Unmarshal(l.Payload, &p); err != nil {
				t.Fatal(err)
			}
			started[l.Session] = p.Kind == "cra"
			continue
		}
		if l.Type != typ {
			continue
		}
		if !started[l.Session] {
			t.Fatalf("%s sans SessionStarted{kind:cra} dans la session %s", typ, l.Session)
		}
		var p T
		if err := json.Unmarshal(l.Payload, &p); err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

func TestCRAInit(t *testing.T) {
	e := newEnv(t, nil, "eu")
	code, out, errb := e.run("cra", "init")
	if code != 0 {
		t.Fatalf("code %d, stderr %q", code, errb)
	}
	if !strings.Contains(out, "cra/product.toml") {
		t.Errorf("stdout %q", out)
	}
	b, err := os.ReadFile(filepath.Join(e.wd, "cra", "product.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"name", "version", "manufacturer", "security_contact", "support_end", "category"} {
		if !strings.Contains(string(b), k) {
			t.Errorf("modèle sans %s :\n%s", k, b)
		}
	}
	if st, err := os.Stat(filepath.Join(e.wd, "cra", "reports")); err != nil || !st.IsDir() {
		t.Fatalf("cra/reports absent : %v", err)
	}
	code, _, errb = e.run("cra", "init")
	if code != 1 || !strings.Contains(errb, "cra: cra/product.toml already exists") {
		t.Fatalf("second init : code %d, stderr %q", code, errb)
	}
}

func TestCRAUnknownSubcommand(t *testing.T) {
	e := newEnv(t, nil, "eu")
	if code, _, _ := e.run("cra", "frobnicate"); code != 1 {
		t.Fatalf("code %d", code)
	}
	if code, _, _ := e.run("cra"); code != 1 {
		t.Fatalf("code %d", code)
	}
}

func TestCRAReportOpenMark(t *testing.T) {
	e := newEnv(t, nil, "eu")
	e.craDossier()
	code, _, errb := e.run("cra", "report", "open", "--kind", "vulnerability", "--summary", "RCE", "--aware-at", "2026-10-22T08:00:00")
	if code != 1 || !strings.Contains(errb, "cra: --aware-at must be RFC 3339 with a time zone") {
		t.Fatalf("aware-at sans fuseau : code %d, stderr %q", code, errb)
	}
	code, _, errb = e.run("cra", "report", "open", "--kind", "bogus", "--summary", "RCE")
	if code != 1 || !strings.Contains(errb, "cra: --kind must be vulnerability or incident") {
		t.Fatalf("kind : code %d, stderr %q", code, errb)
	}
	code, out, errb := e.run("cra", "report", "open", "--kind", "vulnerability", "--summary", "RCE in parser", "--aware-at", "2026-10-22T08:00:00+02:00")
	if code != 0 {
		t.Fatalf("open : code %d, stderr %q", code, errb)
	}
	id := time.Now().UTC().Format("2006-01-02") + "-1"
	if !strings.Contains(out, id) || !strings.Contains(out, "early-warning") {
		t.Fatalf("stdout %q (id %s)", out, id)
	}
	path := filepath.Join(e.wd, "cra", "reports", id+".toml")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "aware_at = 2026-10-22T06:00:00Z") || !strings.Contains(string(b), `summary = "RCE in parser"`) {
		t.Fatalf("contenu :\n%s", b)
	}
	// Options après les arguments positionnels.
	code, _, errb = e.run("cra", "report", "mark", id, "early-warning", "--at", "2026-10-22T09:00:00Z")
	if code != 0 {
		t.Fatalf("mark : code %d, stderr %q", code, errb)
	}
	b, _ = os.ReadFile(path)
	if !strings.Contains(string(b), "early_warning_at = 2026-10-22T09:00:00Z") {
		t.Fatalf("contenu après mark :\n%s", b)
	}
	code, _, errb = e.run("cra", "report", "mark", id, "bogus")
	if code != 1 {
		t.Fatalf("étape inconnue : code %d, stderr %q", code, errb)
	}
	code, _, errb = e.run("cra", "report", "mark", "2026-01-01-9", "notification")
	if code != 1 || !strings.Contains(errb, "cra: report 2026-01-01-9 not found") {
		t.Fatalf("absent : code %d, stderr %q", code, errb)
	}
	if _, err := os.Stat(filepath.Join(e.wd, "cra", "reports", "2026-01-01-9.toml")); err == nil {
		t.Fatal("mark a créé un signalement absent")
	}
	evs := craEvents[event.CRAReportPayload](t, e.auditLines(), event.CRAReport)
	want := []event.CRAReportPayload{
		{ID: id, Kind: "vulnerability", Step: "open", At: "2026-10-22T06:00:00Z"},
		{ID: id, Kind: "vulnerability", Step: "early-warning", At: "2026-10-22T09:00:00Z"},
	}
	if len(evs) != len(want) {
		t.Fatalf("événements %+v", evs)
	}
	for i := range want {
		if evs[i] != want[i] {
			t.Errorf("événement %d : %+v, attendu %+v", i, evs[i], want[i])
		}
	}
}

func TestCRAStatusOverdue(t *testing.T) {
	e := newEnv(t, nil, "eu")
	e.craDossier("pkg:npm/left@1.0.0")
	aware := time.Now().UTC().Add(-48 * time.Hour).Format(time.RFC3339)
	e.writeFile(filepath.Join(e.wd, "cra", "reports", "2026-10-21-1.toml"),
		fmt.Sprintf("kind = \"vulnerability\"\nsummary = \"x\"\naware_at = %s\n", aware))
	code, out, errb := e.run("--lang", "en", "cra", "status")
	if code != 7 {
		t.Fatalf("code %d, stdout %q, stderr %q", code, out, errb)
	}
	for _, w := range []string{"early-warning overdue since", "notification due", "final unknown", "last scan: never", "product: ok"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout sans %q :\n%s", w, out)
		}
	}
	// Français.
	_, out, _ = e.run("cra", "status")
	if !strings.Contains(out, "early-warning en retard depuis") {
		t.Errorf("stdout FR :\n%s", out)
	}
}

func TestCRAStatusOKAndIncomplete(t *testing.T) {
	e := newEnv(t, nil, "eu")
	e.craDossier("pkg:npm/left@1.0.0")
	e.writeFile(filepath.Join(e.wd, "cra", "product.toml"), strings.Replace(craProduct, "2031-12-31", "2020-01-01", 1))
	e.craDB(time.Now(), map[string][]string{"npm": {craLeftDoc}})
	if code, _, errb := e.run("cra", "scan"); code != 0 {
		t.Fatalf("scan : code %d, stderr %q", code, errb)
	}
	code, out, errb := e.run("--lang", "en", "cra", "status")
	if code != 0 {
		t.Fatalf("code %d, stdout %q, stderr %q", code, out, errb)
	}
	for _, w := range []string{"incomplete: support period ended on 2020-01-01", "sbom: ok (1)", "open 1, to verify 0", "reports: none"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout sans %q :\n%s", w, out)
		}
	}
	// Un scan d'un autre projet n'est pas « le dernier scan » de celui-ci.
	other := newEnv(t, nil, "eu")
	other.dataDir, other.vars = e.dataDir, e.vars
	other.writeFile(filepath.Join(other.wd, "cra", "product.toml"), craProduct)
	_, out, _ = other.run("--lang", "en", "cra", "status")
	if !strings.Contains(out, "last scan: never") || !strings.Contains(out, "sbom: missing") {
		t.Errorf("autre projet :\n%s", out)
	}
}

func TestCRAStatusInvalid(t *testing.T) {
	e := newEnv(t, nil, "eu")
	e.craDossier()
	e.writeFile(filepath.Join(e.wd, "cra", "product.toml"), strings.Replace(craProduct, `name = "P"`, "", 1))
	code, _, errb := e.run("cra", "status")
	if code != 1 || !strings.Contains(errb, "cra: product.toml: name required") {
		t.Fatalf("code %d, stderr %q", code, errb)
	}
	e2 := newEnv(t, nil, "eu")
	if code, _, errb := e2.run("cra", "status"); code != 1 || !strings.Contains(errb, "cra: no dossier") {
		t.Fatalf("sans dossier : code %d, stderr %q", code, errb)
	}
}

func TestCRAScanNoDB(t *testing.T) {
	e := newEnv(t, nil, "eu")
	e.craDossier("pkg:npm/left@1.0.0")
	code, _, errb := e.run("cra", "scan")
	if code != 1 || !strings.Contains(errb, "cra: no vulnerability database (run tenon cra db update)") {
		t.Fatalf("code %d, stderr %q", code, errb)
	}
	// Base présente sans l'écosystème npm.
	e.craDB(time.Now(), map[string][]string{"Go": {`{"id":"GO-1","affected":[]}`}})
	code, _, errb = e.run("cra", "scan")
	if code != 1 || !strings.Contains(errb, "cra: no vulnerability database for npm (run tenon cra db update)") {
		t.Fatalf("écosystème absent : code %d, stderr %q", code, errb)
	}
	// Sans SBOM.
	os.Remove(filepath.Join(e.wd, "cra", "sbom.cdx.json"))
	code, _, errb = e.run("cra", "scan")
	if code != 1 || !strings.Contains(errb, "cra: no SBOM found (cra/sbom*.cdx.json)") {
		t.Fatalf("sans SBOM : code %d, stderr %q", code, errb)
	}
	if n := len(craEvents[event.CRAScanPayload](t, e.auditLines(), event.CRAScan)); n != 0 {
		t.Fatalf("%d CRAScan après des échecs", n)
	}
}

func TestCRAScanFailOn(t *testing.T) {
	e := newEnv(t, nil, "eu")
	e.craDossier("pkg:npm/left@1.0.0", "pkg:npm/right@1.0.0", "")
	e.craDB(time.Now(), map[string][]string{"npm": {craLeftDoc}})
	code, out, errb := e.run("cra", "scan", "--fail-on", "open")
	if code != 7 {
		t.Fatalf("--fail-on open : code %d, stdout %q, stderr %q", code, out, errb)
	}
	code, out, errb = e.run("--lang", "en", "cra", "scan")
	if code != 0 {
		t.Fatalf("code %d, stderr %q", code, errb)
	}
	if !strings.Contains(errb, "cra: 1 components not checked, 0 VEX conflicts (see below)") {
		t.Errorf("avis absent : %q", errb)
	}
	for _, w := range []string{"1 open, 0 to verify, 0 not affected, 0 fixed, 0 conflicts, 1 unchecked", "GHSA-1 pkg:npm/left@1.0.0 prototype pollution", "unchecked: nopurl2@1 (no purl)"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout sans %q :\n%s", w, out)
		}
	}
	if strings.Contains(errb, "days old") {
		t.Errorf("avis d'ancienneté pour une base récente : %q", errb)
	}
	code, out, _ = e.run("cra", "scan", "--json")
	if code != 0 {
		t.Fatalf("--json : code %d", code)
	}
	var res map[string]any
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("JSON invalide : %v\n%s", err, out)
	}
	for _, k := range []string{"sboms", "components", "findings", "open", "to_verify", "not_affected", "fixed", "conflicts", "unchecked", "unchecked_components", "db_date"} {
		if _, ok := res[k]; !ok {
			t.Errorf("clé %q absente : %s", k, out)
		}
	}
	if res["open"] != float64(1) || res["components"] != float64(3) {
		t.Errorf("valeurs : %s", out)
	}
	fs, _ := res["findings"].([]any)
	if len(fs) != 1 {
		t.Fatalf("findings : %s", out)
	}
	f := fs[0].(map[string]any)
	if f["id"] != "GHSA-1" || f["status"] != "open" {
		t.Errorf("finding : %v", f)
	}
	if code, _, errb := e.run("cra", "scan", "--fail-on", "to_verify"); code != 1 || !strings.Contains(errb, "cra: --fail-on must be open") {
		t.Errorf("--fail-on invalide : code %d, stderr %q", code, errb)
	}
	evs := craEvents[event.CRAScanPayload](t, e.auditLines(), event.CRAScan)
	if len(evs) != 3 {
		t.Fatalf("CRAScan : %+v", evs)
	}
	if p := evs[0]; p.SBOMs != 1 || p.Components != 3 || p.Open != 1 || p.DBDate == "" {
		t.Errorf("charge : %+v", p)
	}
}

func TestCRADBOld(t *testing.T) {
	e := newEnv(t, nil, "eu")
	e.craDossier("pkg:npm/right@1.0.0")
	e.craDB(time.Now().Add(-10*24*time.Hour-time.Hour), map[string][]string{"npm": {craLeftDoc}})
	code, _, errb := e.run("cra", "scan")
	if code != 0 || !strings.Contains(errb, "cra: vulnerability database is 10 days old") {
		t.Fatalf("code %d, stderr %q", code, errb)
	}
}

func TestCRADBUpdateStrictLocal(t *testing.T) {
	e := newEnv(t, nil, "eu")
	e.editGlobal("profile = \"strict-local\"\n")
	e.craDossier("pkg:npm/left@1.0.0")
	for _, args := range [][]string{
		{"cra", "db", "update"},
		{"cra", "db", "update", "--mirror", "https://osv.example.eu"},
	} {
		code, _, errb := e.run(args...)
		if code != 1 || !strings.Contains(errb, "cra: profile strict-local requires a local osv mirror") {
			t.Fatalf("%v : code %d, stderr %q", args, code, errb)
		}
	}
}

// mirror démarre un miroir OSV TLS servant <eco>/all.zip et fait confiance à
// son certificat pendant le test.
func mirror(t *testing.T, ecos ...string) (*httptest.Server, *hitLog) {
	t.Helper()
	hits := &hitLog{}
	data := craZip(t, craLeftDoc)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.add(r.URL.Path)
		for _, eco := range ecos {
			if r.URL.Path == "/"+eco+"/all.zip" {
				_, _ = w.Write(data)
				return
			}
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	prev := craHTTPClient
	craHTTPClient = func(noProxy, localOnly bool) *http.Client {
		c := osv.Client(noProxy, localOnly)
		c.Transport.(*http.Transport).TLSClientConfig = &tls.Config{RootCAs: pool}
		return c
	}
	t.Cleanup(func() { craHTTPClient = prev })
	return srv, hits
}

// hitLog enregistre les chemins demandés au miroir (accès concurrents).
type hitLog struct {
	mu    sync.Mutex
	paths []string
}

func (h *hitLog) add(p string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.paths = append(h.paths, p)
}

// take renvoie les chemins demandés depuis le dernier appel.
func (h *hitLog) take() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := h.paths
	h.paths = nil
	return out
}

func TestCRADBUpdateMirror(t *testing.T) {
	srv, hits := mirror(t, "npm")
	e := newEnv(t, nil, "eu")
	// Sans dossier ni --ecosystem : rien à télécharger.
	code, _, errb := e.run("cra", "db", "update", "--mirror", srv.URL)
	if code != 1 || !strings.Contains(errb, "--ecosystem") {
		t.Fatalf("sans dossier : code %d, stderr %q", code, errb)
	}
	code, out, errb := e.run("cra", "db", "update", "--mirror", srv.URL, "--ecosystem", "npm")
	if code != 0 {
		t.Fatalf("code %d, stderr %q", code, errb)
	}
	if !strings.Contains(out, "npm") {
		t.Errorf("stdout %q", out)
	}
	if _, err := os.Stat(filepath.Join(e.dataDir, "cra", "osv", "npm", "all.zip")); err != nil {
		t.Fatal(err)
	}
	// Écosystèmes des SBOM du dossier ; miroir de la configuration ;
	// strict-local accepte un miroir local.
	e.craDossier("pkg:npm/left@1.0.0")
	e.editGlobal("profile = \"strict-local\"\n")
	cfg, err := os.ReadFile(e.globalConfig())
	if err != nil {
		t.Fatal(err)
	}
	e.writeFile(e.globalConfig(), string(cfg)+fmt.Sprintf("\n[cra]\nosv_mirror = %q\n", srv.URL))
	hits.take()
	code, _, errb = e.run("cra", "db", "update")
	if got := hits.take(); code != 0 || len(got) != 1 || got[0] != "/npm/all.zip" {
		t.Fatalf("config : code %d, stderr %q, requêtes %v", code, errb, got)
	}
	if code, _, errb := e.run("cra", "scan"); code != 0 {
		t.Fatalf("scan après update : code %d, stderr %q", code, errb)
	}
	// Index illisible : mis de côté avec un avis.
	e.writeFile(filepath.Join(e.dataDir, "cra", "osv", "index.json"), "{")
	code, _, errb = e.run("cra", "db", "update")
	if code != 0 || !strings.Contains(errb, "index.json") {
		t.Fatalf("index corrompu : code %d, stderr %q", code, errb)
	}
	// Échec de téléchargement : code 1, écosystème nommé.
	code, _, errb = e.run("cra", "db", "update", "--ecosystem", "PyPI")
	if code != 1 || !strings.Contains(errb, "PyPI") {
		t.Fatalf("404 : code %d, stderr %q", code, errb)
	}
}

func TestCRAConfigCheck(t *testing.T) {
	e := newEnv(t, nil, "eu")
	code, out, errb := e.run("config", "check")
	if code != 0 || !strings.Contains(out, "cra: dossier absent, osv db absent") {
		t.Fatalf("code %d, stdout %q, stderr %q", code, out, errb)
	}
	e.craDossier("pkg:npm/left@1.0.0")
	e.craDB(time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC), map[string][]string{"npm": {craLeftDoc}})
	code, out, errb = e.run("config", "check")
	if code != 0 || !strings.Contains(out, "cra: dossier ok, osv db 2026-10-20") {
		t.Fatalf("code %d, stdout %q, stderr %q", code, out, errb)
	}
	e.writeFile(filepath.Join(e.wd, "cra", "product.toml"), "name = 1")
	code, out, _ = e.run("config", "check")
	if code != 0 || !strings.Contains(out, "cra: dossier invalid, osv db 2026-10-20") {
		t.Fatalf("code %d, stdout %q", code, out)
	}
}

func TestCRAJournalFailureKeepsReport(t *testing.T) {
	e := newEnv(t, nil, "eu")
	e.craDossier()
	// Le dossier d'audit est un fichier : le journal ne peut pas s'ouvrir.
	e.writeFile(filepath.Join(e.dataDir, "audit"), "x")
	code, _, errb := e.run("cra", "report", "open", "--kind", "incident", "--summary", "outage")
	if code != 1 || !strings.Contains(errb, "cra: audit log:") {
		t.Fatalf("code %d, stderr %q", code, errb)
	}
	id := time.Now().UTC().Format("2006-01-02") + "-1"
	if _, err := os.Stat(filepath.Join(e.wd, "cra", "reports", id+".toml")); err != nil {
		t.Fatalf("signalement perdu : %v", err)
	}
}

func TestCRAStatusMetadataLevel(t *testing.T) {
	e := newEnvExtra(t, nil, "eu", "\n[audit]\nlevel = \"metadata\"\n")
	e.craDossier("pkg:npm/left@1.0.0")
	e.craDB(time.Now(), map[string][]string{"npm": {craLeftDoc}})
	if code, _, errb := e.run("cra", "scan"); code != 0 {
		t.Fatalf("scan : code %d, stderr %q", code, errb)
	}
	_, out, _ := e.run("--lang", "en", "cra", "status")
	if !strings.Contains(out, "last scan: 20") || strings.Contains(out, "open 1") {
		t.Fatalf("stdout :\n%s", out)
	}
}

func TestCRAScanUnsupportedOnly(t *testing.T) {
	e := newEnv(t, nil, "eu")
	e.craDossier("pkg:deb/debian/openssl@3.0.0", "pkg:deb/debian/curl@8.0.0")
	e.craDB(time.Now(), map[string][]string{"npm": {craLeftDoc}})
	code, out, errb := e.run("--lang", "en", "cra", "scan", "--fail-on", "open")
	if code != 0 {
		t.Fatalf("code %d, stderr %q", code, errb)
	}
	for _, w := range []string{"0 open, 0 to verify, 0 not affected, 0 fixed, 0 conflicts, 2 unchecked", "unchecked: c0@1 (unsupported type deb)"} {
		if !strings.Contains(out, w) {
			t.Errorf("stdout sans %q :\n%s", w, out)
		}
	}
	if !strings.Contains(errb, "cra: 2 components not checked, 0 VEX conflicts (see below)") {
		t.Errorf("avis absent : %q", errb)
	}
	_, out, errb = e.run("cra", "scan")
	if !strings.Contains(out, "0 conflits, 2 non vérifiés") || !strings.Contains(errb, "cra : 2 composants non vérifiés, 0 conflits VEX (voir ci-dessous)") {
		t.Errorf("FR : stdout %q, stderr %q", out, errb)
	}
}

func TestCRAScanConflictNotice(t *testing.T) {
	e := newEnv(t, nil, "eu")
	e.craDossier("pkg:npm/left@1.0.0")
	e.writeFile(filepath.Join(e.wd, "cra", "vex.cdx.json"), `{"bomFormat":"CycloneDX","specVersion":"1.5","vulnerabilities":[{"id":"GHSA-1","analysis":{"state":"resolved"},"affects":[{"ref":"pkg:npm/left@1.0.0"}]}]}`)
	e.craDB(time.Now(), map[string][]string{"npm": {craLeftDoc}})
	code, out, errb := e.run("--lang", "en", "cra", "scan", "--fail-on", "open")
	if code != 0 {
		t.Fatalf("code %d, stderr %q", code, errb)
	}
	if !strings.Contains(out, "1 fixed, 1 conflicts, 0 unchecked") || !strings.Contains(out, "fixed per VEX but pkg:npm/left@1.0.0 is listed as affected by GHSA-1") {
		t.Errorf("stdout :\n%s", out)
	}
	if !strings.Contains(errb, "cra: 0 components not checked, 1 VEX conflicts (see below)") {
		t.Errorf("avis absent : %q", errb)
	}
}

// TestCRADBUpdateStrictLocalRedirect : sous strict-local, un miroir local qui
// redirige vers un hôte distant est refusé avant toute connexion à cet hôte.
func TestCRADBUpdateStrictLocalRedirect(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://storage.googleapis.com/osv-vulnerabilities"+r.URL.Path, http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	var dials hitLog
	prev := craHTTPClient
	craHTTPClient = func(noProxy, localOnly bool) *http.Client {
		c := osv.Client(noProxy, localOnly)
		tr := c.Transport.(*http.Transport)
		tr.TLSClientConfig = &tls.Config{RootCAs: pool}
		var d net.Dialer
		tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			dials.add(addr)
			return d.DialContext(ctx, network, addr)
		}
		return c
	}
	t.Cleanup(func() { craHTTPClient = prev })
	e := newEnv(t, nil, "eu")
	e.editGlobal("profile = \"strict-local\"\n")
	code, _, errb := e.run("cra", "db", "update", "--mirror", srv.URL, "--ecosystem", "npm")
	if code != 1 || !strings.Contains(errb, "redirect off local host refused") {
		t.Fatalf("code %d, stderr %q", code, errb)
	}
	for _, a := range dials.take() {
		if strings.Contains(a, "googleapis") {
			t.Fatalf("hôte distant contacté : %s", a)
		}
	}
	if _, err := os.Stat(filepath.Join(e.dataDir, "cra", "osv", "npm", "all.zip")); err == nil {
		t.Fatal("archive écrite malgré le refus")
	}
}
