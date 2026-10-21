package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/tenon-code-eu/tenon/internal/tsp"
	"github.com/tenon-code-eu/tenon/internal/tsp/tsptest"
)

// writeCA écrit pem dans un fichier temporaire et renvoie son chemin.
func writeCA(t *testing.T, pem []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "tsa-ca.pem")
	if err := os.WriteFile(p, pem, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// timestampTOML renvoie la table [audit.timestamp] (ca_file omis si vide).
func timestampTOML(url, caFile string) string {
	s := fmt.Sprintf("\n[audit.timestamp]\nurl = %q\n", url)
	if caFile != "" {
		s += fmt.Sprintf("ca_file = %q\n", caFile)
	}
	return s
}

// tsaEnv : environnement dont la configuration globale horodate auprès de la
// fausse TSA tsa (racines : son certificat dans ca_file).
func tsaEnv(t *testing.T, f *fakeLLM, tsa *tsptest.Server) *env {
	t.Helper()
	return newEnvExtra(t, f, "eu", timestampTOML(tsa.URL, writeCA(t, tsa.CA.PEM)))
}

// tokens liste les jetons .tsr de l'environnement.
func (e *env) tokens() []string {
	e.t.Helper()
	ents, err := os.ReadDir(filepath.Join(e.dataDir, "audit", "timestamps"))
	if err != nil && !os.IsNotExist(err) {
		e.t.Fatal(err)
	}
	var out []string
	for _, de := range ents {
		if strings.HasSuffix(de.Name(), ".tsr") {
			out = append(out, de.Name())
		}
	}
	return out
}

// closedPort renvoie un port TCP local sur lequel rien n'écoute.
func closedPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

func TestTimestampEndOfSessionE2E(t *testing.T) {
	tsa := tsptest.NewServer(t, tsptest.Options{})
	e := tsaEnv(t, newFakeLLM(t, sseText("bonjour")), tsa)
	code, out, errb := e.run("-p", "salut")
	if code != 0 || !strings.Contains(out, "bonjour") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errb)
	}
	if strings.Contains(errb, "horodatage") {
		t.Fatalf("avis inattendu : %q", errb)
	}
	if tk := e.tokens(); len(tk) != 1 || tsa.Requests.Load() != 1 {
		t.Fatalf("jetons %v, %d requête(s)", tk, tsa.Requests.Load())
	}
	code, out, errb = e.run("--lang", "en", "audit", "verify")
	if code != 0 || !strings.HasPrefix(out, "OK: ") || !strings.Contains(out, "timestamps: 1 valid, last ") || !strings.Contains(out, ", 0 lines after") {
		t.Fatalf("audit verify : code=%d stdout=%q stderr=%q", code, out, errb)
	}
	code, out, _ = e.run("audit", "verify")
	if code != 0 || !strings.Contains(out, "horodatages — valides : 1, dernier ") {
		t.Fatalf("fr : code=%d stdout=%q", code, out)
	}
}

// auditLine reproduit audit.Line (même ordre de champs, mêmes balises).
type auditLine struct {
	Seq     uint64          `json:"seq"`
	TS      string          `json:"ts"`
	Session string          `json:"session"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
	Prev    string          `json:"prev"`
	Hash    string          `json:"hash,omitempty"`
}

// chainHash reproduit le calcul de hash de internal/audit (writer.go).
func chainHash(t *testing.T, l auditLine) string {
	t.Helper()
	l.Hash = ""
	b, err := json.Marshal(l)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	h.Write([]byte(l.Prev))
	h.Write(b)
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// rewriteAudit réécrit toute la chaîne d'audit (ts de la première ligne
// modifié) en recalculant chaque hash : la chaîne reste valide.
func (e *env) rewriteAudit() {
	e.t.Helper()
	dir := filepath.Join(e.dataDir, "audit")
	matches, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil || len(matches) == 0 {
		e.t.Fatalf("journaux : %v %v", matches, err)
	}
	sort.Strings(matches)
	prev, first := "sha256:0", true
	for _, p := range matches {
		b, err := os.ReadFile(p)
		if err != nil {
			e.t.Fatal(err)
		}
		var buf bytes.Buffer
		sc := bufio.NewScanner(bytes.NewReader(b))
		sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
		for sc.Scan() {
			var l auditLine
			if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
				e.t.Fatal(err)
			}
			if first {
				l.TS = "2020-01-01T00:00:00Z"
				first = false
			}
			l.Prev = prev
			l.Hash = chainHash(e.t, l)
			prev = l.Hash
			out, err := json.Marshal(l)
			if err != nil {
				e.t.Fatal(err)
			}
			buf.Write(out)
			buf.WriteByte('\n')
		}
		if err := os.WriteFile(p, buf.Bytes(), 0o600); err != nil {
			e.t.Fatal(err)
		}
	}
}

func TestTimestampRewriteDetectedE2E(t *testing.T) {
	tsa := tsptest.NewServer(t, tsptest.Options{})
	e := tsaEnv(t, newFakeLLM(t, sseText("bonjour")), tsa)
	if code, _, errb := e.run("-p", "salut"); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	e.rewriteAudit()
	code, out, errb := e.run("--lang", "en", "audit", "verify")
	if code != 1 || !strings.HasPrefix(out, "OK: ") || !strings.Contains(errb, "anchored hash not in chain") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errb)
	}
}

func TestTimestampTSADownE2E(t *testing.T) {
	ca := tsptest.NewCA(t)
	down := fmt.Sprintf("http://127.0.0.1:%d/", closedPort(t))
	html := tsptest.NewServer(t, tsptest.Options{Tamper: tsptest.HTMLBody})
	for name, url := range map[string]string{"closed": down, "html": html.URL} {
		t.Run(name, func(t *testing.T) {
			e := newEnvExtra(t, newFakeLLM(t, sseText("bonjour")), "eu", timestampTOML(url, writeCA(t, ca.PEM)))
			code, out, errb := e.run("--lang", "en", "-p", "salut")
			if code != 0 || !strings.Contains(out, "bonjour") || !strings.Contains(errb, "audit timestamp: ") {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errb)
			}
			if tk := e.tokens(); len(tk) != 0 {
				t.Fatalf("jetons : %v", tk)
			}
			// L'échec ne casse pas la chaîne.
			if code, out, errb := e.run("--lang", "en", "audit", "verify"); code != 0 || !strings.Contains(out, "timestamps: none") {
				t.Fatalf("audit verify : code=%d stdout=%q stderr=%q", code, out, errb)
			}
		})
	}
}

func TestTimestampStrictLocalRemoteE2E(t *testing.T) {
	f := newFakeLLM(t, sseText("bonjour"))
	e := newEnvExtra(t, f, "local", timestampTOML("https://tsa.example.eu/tsr", ""))
	e.editGlobal("profile = \"strict-local\"\n")
	code, _, errb := e.run("--lang", "en", "-p", "salut")
	if code != 1 || !strings.Contains(errb, `profile strict-local: audit timestamp host "tsa.example.eu" is not local`) {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	if n := len(f.requests()); n != 0 {
		t.Fatalf("%d requête(s) envoyée(s)", n)
	}
	code, _, errb = e.run("-p", "salut")
	if code != 1 || !strings.Contains(errb, "profil strict-local : l'hôte « tsa.example.eu » de l'horodatage d'audit n'est pas une adresse locale") {
		t.Fatalf("fr : code=%d stderr=%q", code, errb)
	}
	// Même refus pour config check et audit timestamp.
	if code, _, errb := e.run("--lang", "en", "config", "check"); code != 1 || !strings.Contains(errb, `audit timestamp host "tsa.example.eu" is not local`) {
		t.Fatalf("config check : code=%d stderr=%q", code, errb)
	}
	if code, _, errb := e.run("--lang", "en", "audit", "timestamp"); code != 1 || !strings.Contains(errb, `audit timestamp host "tsa.example.eu" is not local`) {
		t.Fatalf("audit timestamp : code=%d stderr=%q", code, errb)
	}
	if ents, _ := e.sessionFiles(); len(ents) != 0 {
		t.Fatalf("session créée : %v", ents)
	}
}

// policyTimestamp renvoie la table [audit.timestamp] d'une politique.
func policyTimestamp(url string, pem []byte) string {
	s := fmt.Sprintf("[audit.timestamp]\nurl = %q\n", url)
	if len(pem) > 0 {
		s += "ca = '''\n" + string(pem) + "'''\n"
	}
	return s
}

func TestTimestampPolicyImposedE2E(t *testing.T) {
	tsa := tsptest.NewServer(t, tsptest.Options{})
	e := newEnv(t, newFakeLLM(t, sseText("bonjour")), "eu")
	e.withPolicy(policyHead + policyTimestamp(tsa.URL, tsa.CA.PEM))
	code, out, errb := e.run("--lang", "en", "-p", "salut")
	want := "policy acme-dev: audit timestamp imposed (" + tsa.URL + ")"
	if code != 0 || !strings.Contains(out, "bonjour") || !strings.Contains(errb, want) {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errb)
	}
	if strings.Contains(errb, "audit timestamp: ") {
		t.Fatalf("échec d'horodatage : %q", errb)
	}
	if tk := e.tokens(); len(tk) != 1 {
		t.Fatalf("jetons : %v", tk)
	}
	if _, _, errb := e.run("-p", "salut"); !strings.Contains(errb, "politique acme-dev : horodatage d'audit imposé ("+tsa.URL+")") {
		t.Fatalf("fr : stderr=%q", errb)
	}
	// Racines de la politique pour la vérification et config check.
	if code, out, errb := e.run("--lang", "en", "audit", "verify"); code != 0 || !strings.Contains(out, "timestamps: 2 valid") {
		t.Fatalf("audit verify : code=%d stdout=%q stderr=%q", code, out, errb)
	}
	if code, out, errb := e.run("--lang", "en", "config", "check"); code != 0 || !strings.Contains(out, "audit.timestamp: "+tsa.URL+" (roots: policy)") {
		t.Fatalf("config check : code=%d stdout=%q stderr=%q", code, out, errb)
	}
}

func TestTimestampPolicyStrictLocalE2E(t *testing.T) {
	f := newFakeLLM(t, sseText("bonjour"))
	e := newEnv(t, f, "local")
	e.withPolicy(policyHead + "profile = \"strict-local\"\n" + policyTimestamp("https://tsa.example.eu/tsr", nil))
	code, _, errb := e.run("--lang", "en", "-p", "salut")
	if code != 1 || !strings.Contains(errb, `audit.timestamp.url host "tsa.example.eu" is not local (profile strict-local)`) {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	if n := len(f.requests()); n != 0 {
		t.Fatalf("%d requête(s) envoyée(s)", n)
	}
}

func TestAuditTimestampCommandE2E(t *testing.T) {
	tsa := tsptest.NewServer(t, tsptest.Options{})
	e := newEnv(t, newFakeLLM(t, sseText("bonjour")), "eu")
	if code, _, errb := e.run("-p", "salut"); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	if tk := e.tokens(); len(tk) != 0 {
		t.Fatalf("jetons sans horodatage : %v", tk)
	}
	code, out, errb := e.run("--lang", "en", "audit", "timestamp")
	if code != 1 || out != "" || !strings.Contains(errb, "audit timestamp: not configured ([audit.timestamp] url)") {
		t.Fatalf("non configuré : code=%d stdout=%q stderr=%q", code, out, errb)
	}
	// Table vide : horodatage désactivé.
	e.editGlobal("", `jurisdiction = "eu"`, "jurisdiction = \"eu\"\n\n[audit.timestamp]\nurl = \"\"\n")
	if code, _, errb := e.run("--lang", "en", "audit", "timestamp"); code != 1 || !strings.Contains(errb, "not configured") {
		t.Fatalf("url vide : code=%d stderr=%q", code, errb)
	}
	e.editGlobal("", `url = ""`, fmt.Sprintf("url = %q\nca_file = %q", tsa.URL, writeCA(t, tsa.CA.PEM)))
	code, out, errb = e.run("--lang", "en", "audit", "timestamp")
	tk := e.tokens()
	okLine := regexp.MustCompile(`^audit timestamp: [^/\\\s]+\.tsr \(\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ, tsptest TSA\)\n$`)
	if code != 0 || len(tk) != 1 || !okLine.MatchString(out) || !strings.Contains(out, tk[0]) {
		t.Fatalf("code=%d stdout=%q stderr=%q jetons=%v", code, out, errb, tk)
	}
	code, out, errb = e.run("--lang", "en", "audit", "timestamp")
	if code != 0 || strings.TrimSpace(out) != "audit timestamp: already anchored" || tsa.Requests.Load() != 1 {
		t.Fatalf("déjà ancré : code=%d stdout=%q stderr=%q requêtes=%d", code, out, errb, tsa.Requests.Load())
	}
	if code, out, _ := e.run("audit", "timestamp"); code != 0 || strings.TrimSpace(out) != "horodatage d'audit : déjà ancré" {
		t.Fatalf("fr : code=%d stdout=%q", code, out)
	}

	// Journal vide : aucun appel réseau.
	tsa2 := tsptest.NewServer(t, tsptest.Options{})
	e2 := tsaEnv(t, nil, tsa2)
	code, _, errb = e2.run("--lang", "en", "audit", "timestamp")
	if code != 1 || !strings.Contains(errb, "audit timestamp: audit log is empty") || tsa2.Requests.Load() != 0 {
		t.Fatalf("journal vide : code=%d stderr=%q requêtes=%d", code, errb, tsa2.Requests.Load())
	}
}

func TestAuditVerifyTSACAE2E(t *testing.T) {
	tsa := tsptest.NewServer(t, tsptest.Options{})
	e := tsaEnv(t, newFakeLLM(t, sseText("bonjour")), tsa)
	if code, _, errb := e.run("-p", "salut"); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	other := writeCA(t, tsptest.NewCA(t).PEM)
	code, out, errb := e.run("--lang", "en", "audit", "verify", "--tsa-ca", other)
	if code != 1 || !strings.HasPrefix(out, "OK: ") || !strings.Contains(errb, "timestamp ") {
		t.Fatalf("autre CA : code=%d stdout=%q stderr=%q", code, out, errb)
	}
	if !strings.HasSuffix(out, "timestamps: 0 valid, 1 invalid\n") {
		t.Fatalf("autre CA : stdout=%q", out)
	}
	code, out, errb = e.run("--lang", "en", "audit", "verify", "--tsa-ca", writeCA(t, tsa.CA.PEM))
	if code != 0 || !strings.Contains(out, "timestamps: 1 valid") || errb != "" {
		t.Fatalf("bonne CA : code=%d stdout=%q stderr=%q", code, out, errb)
	}
	// --tsa-ca prime la configuration, même illisible.
	if code, _, errb := e.run("--lang", "en", "audit", "verify", "--tsa-ca", filepath.Join(t.TempDir(), "absent.pem")); code != 1 || !strings.Contains(errb, "--tsa-ca") {
		t.Fatalf("fichier absent : code=%d stderr=%q", code, errb)
	}
	if code, _, errb := e.run("--lang", "en", "audit", "verify", "--tsa-ca", writeCA(t, []byte("pas un PEM"))); code != 1 || !strings.Contains(errb, "no certificate") {
		t.Fatalf("sans certificat : code=%d stderr=%q", code, errb)
	}
}

func TestAuditVerifyNoTokensE2E(t *testing.T) {
	e := newEnv(t, newFakeLLM(t, sseText("bonjour")), "eu")
	if code, _, errb := e.run("-p", "salut"); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	code, out, errb := e.run("--lang", "en", "audit", "verify")
	if code != 0 || !strings.HasPrefix(out, "OK: ") || !strings.HasSuffix(out, "\ntimestamps: none\n") || errb != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errb)
	}
	if _, out, _ := e.run("audit", "verify"); !strings.HasSuffix(out, "\nhorodatages : aucun\n") {
		t.Fatalf("fr : stdout=%q", out)
	}
}

func TestConfigCheckTimestampE2E(t *testing.T) {
	tsa := tsptest.NewServer(t, tsptest.Options{})
	e := tsaEnv(t, nil, tsa)
	code, out, errb := e.run("--lang", "en", "config", "check")
	if code != 0 || !strings.Contains(out, "audit.timestamp: "+tsa.URL+" (roots: ca_file)\n") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errb)
	}
	if _, out, _ := e.run("config", "check"); !strings.Contains(out, "audit.timestamp : "+tsa.URL+" (racines : ca_file)") {
		t.Fatalf("fr : stdout=%q", out)
	}
	if tsa.Requests.Load() != 0 {
		t.Fatalf("config check a contacté la TSA")
	}
	// Sans horodatage : pas de ligne.
	e2 := newEnv(t, nil, "eu")
	if _, out, _ := e2.run("--lang", "en", "config", "check"); strings.Contains(out, "audit.timestamp") {
		t.Fatalf("ligne inattendue : %q", out)
	}
}

// Une redirection n'est jamais suivie : https → http contournerait CheckURL.
func TestTimestampNoRedirectE2E(t *testing.T) {
	tsa := tsptest.NewServer(t, tsptest.Options{})
	redir := httptest.NewServer(http.RedirectHandler(tsa.URL, http.StatusTemporaryRedirect))
	t.Cleanup(redir.Close)
	e := newEnvExtra(t, newFakeLLM(t, sseText("bonjour")), "eu", timestampTOML(redir.URL, writeCA(t, tsa.CA.PEM)))
	code, _, errb := e.run("--lang", "en", "-p", "salut")
	if code != 0 || !strings.Contains(errb, "audit timestamp: HTTP 307") || tsa.Requests.Load() != 0 || len(e.tokens()) != 0 {
		t.Fatalf("code=%d stderr=%q requêtes=%d", code, errb, tsa.Requests.Load())
	}
}

// Un jeton illisible dont le nom contient une séquence d'échappement : la
// sortie de audit verify ne doit transmettre aucun caractère de contrôle.
func TestAuditVerifyStripsControlE2E(t *testing.T) {
	tsa := tsptest.NewServer(t, tsptest.Options{})
	e := tsaEnv(t, newFakeLLM(t, sseText("bonjour")), tsa)
	if code, _, errb := e.run("-p", "salut"); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	bad := filepath.Join(e.dataDir, "audit", "timestamps", "\x1b[31mevil.tsr")
	if err := os.WriteFile(bad, []byte("junk"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errb := e.run("--lang", "en", "audit", "verify")
	if code != 1 || !strings.Contains(errb, "evil.tsr") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errb)
	}
	if strings.ContainsRune(out+errb, 0x1b) {
		t.Fatalf("caractère de contrôle en sortie : %q", errb)
	}
}

func TestStripControl(t *testing.T) {
	if got := stripControl("a\x1b[31mb\u009bc\u202ed\te"); got != "a?[31mb?c?d?e" {
		t.Fatalf("got %q", got)
	}
}

// Racines nil sous darwin : l'ancrage échoue avant tout contact avec la TSA.
func TestAnchorNoRootsFailsFastE2E(t *testing.T) {
	tsa := tsptest.NewServer(t, tsptest.Options{})
	e := newEnvExtra(t, newFakeLLM(t, sseText("bonjour")), "eu", timestampTOML(tsa.URL, ""))
	defer tsp.SetGOOS("darwin")()
	code, _, errb := e.run("--lang", "en", "-p", "salut")
	if code != 0 || !strings.Contains(errb, "no trust roots: system roots cannot verify timestamp certificates on darwin; configure a CA file") {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	if n := tsa.Requests.Load(); n != 0 {
		t.Fatalf("%d requête(s) à la TSA", n)
	}
}
