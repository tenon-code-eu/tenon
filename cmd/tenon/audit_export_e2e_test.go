package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tenon-code-eu/tenon/internal/audit"
	"github.com/tenon-code-eu/tenon/internal/compliance"
	"github.com/tenon-code-eu/tenon/internal/event"
)

const allowEdit = `
[[permissions.rules]]
tool = "edit_file"
decision = "allow"
`

const (
	exportPrompt   = "MARQUEURPROMPT corrige le fichier"
	exportResponse = "MARQUEURREPONSE c'est corrigé"
)

// exportEnv joue une session headless qui lit puis modifie notes.txt.
func exportEnv(t *testing.T) *env {
	t.Helper()
	f := newFakeLLM(t,
		sseCall("read_file", `{"path":"notes.txt"}`),
		sseCall("edit_file", `{"path":"notes.txt","old_string":"bonjour","new_string":"salut"}`),
		sseText(exportResponse))
	e := newEnvExtra(t, f, "eu", allowEdit)
	e.writeFile(filepath.Join(e.wd, "notes.txt"), "bonjour\n")
	if code, _, errb := e.run("-p", exportPrompt); code != 0 {
		t.Fatalf("session : code=%d stderr=%q", code, errb)
	}
	if b, err := os.ReadFile(filepath.Join(e.wd, "notes.txt")); err != nil || string(b) != "salut\n" {
		t.Fatalf("notes.txt : %q %v", b, err)
	}
	return e
}

// exportPaths renvoie les chemins attendus pour la période par défaut.
func exportPaths(out string) (string, string) {
	until := time.Now().UTC()
	base := "tenon-audit-" + until.AddDate(0, 0, -29).Format("2006-01-02") + "_" + until.Format("2006-01-02")
	return filepath.Join(out, base+".html"), filepath.Join(out, base+".json")
}

// readExport lit les deux fichiers et vérifie leur mode 0600.
func readExport(t *testing.T, out string) (string, string) {
	t.Helper()
	hp, jp := exportPaths(out)
	var res []string
	for _, p := range []string{hp, jp} {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != 0o600 {
			t.Fatalf("%s : mode %v", p, st.Mode().Perm())
		}
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		res = append(res, string(b))
	}
	return res[0], res[1]
}

func listDir(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, de := range ents {
		names = append(names, de.Name())
	}
	return names
}

func TestAuditExportE2E(t *testing.T) {
	e := exportEnv(t)
	out := t.TempDir()
	code, stdout, errb := e.run("audit", "export", "--out", out)
	if code != 0 || errb != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, errb)
	}
	hp, jp := exportPaths(out)
	if !strings.Contains(stdout, "rapport écrit : "+hp+", "+jp) {
		t.Fatalf("stdout=%q", stdout)
	}
	html, js := readExport(t, out)
	if !strings.Contains(html, "Test SAS") || !strings.Contains(html, "notes.txt") {
		t.Fatalf("HTML sans fournisseur ou fichier modifié : %s", html)
	}
	for _, s := range []string{html, js} {
		if strings.Contains(s, "MARQUEURPROMPT") || strings.Contains(s, "MARQUEURREPONSE") || strings.Contains(s, "bonjour") {
			t.Fatalf("contenu de conversation exporté sans --details : %s", s)
		}
	}
	var env struct {
		SchemaVersion int               `json:"schema_version"`
		ReportID      string            `json:"report_id"`
		HTMLSHA256    string            `json:"html_sha256"`
		Report        compliance.Report `json:"report"`
	}
	if err := json.Unmarshal([]byte(js), &env); err != nil {
		t.Fatal(err)
	}
	id, err := compliance.ReportID(env.Report)
	if err != nil || env.ReportID != id || !strings.Contains(html, id) {
		t.Fatalf("report_id=%q recalculé=%q (%v)", env.ReportID, id, err)
	}
	sum := sha256.Sum256([]byte(html))
	if env.HTMLSHA256 != "sha256:"+hex.EncodeToString(sum[:]) {
		t.Fatalf("html_sha256=%q", env.HTMLSHA256)
	}
	if env.SchemaVersion != 1 || len(env.Report.Files) != 1 || env.Report.Scope.Kind != "project" {
		t.Fatalf("rapport : %+v", env.Report)
	}
}

func TestAuditExportDetailsE2E(t *testing.T) {
	e := exportEnv(t)
	out := t.TempDir()
	if code, stdout, errb := e.run("audit", "export", "--details", "--lang", "en", "--out", out); code != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, errb)
	}
	html, js := readExport(t, out)
	for _, s := range []string{html, js} {
		if !strings.Contains(s, "MARQUEURPROMPT") {
			t.Fatalf("prompt absent avec --details : %s", s)
		}
	}
	if !strings.Contains(html, "This report contains conversation data.") {
		t.Fatalf("bandeau --details absent : %s", html)
	}
}

func TestAuditExportOtherProjectE2E(t *testing.T) {
	e := exportEnv(t)
	project := e.wd
	e.wd = filepath.Join(filepath.Dir(project), "autre")
	if err := os.MkdirAll(e.wd, 0o700); err != nil {
		t.Fatal(err)
	}
	sessions := func(args ...string) int {
		t.Helper()
		out := t.TempDir()
		code, stdout, errb := e.run(append([]string{"audit", "export", "--out", out}, args...)...)
		if code != 0 {
			t.Fatalf("%v : code=%d stdout=%q stderr=%q", args, code, stdout, errb)
		}
		_, js := readExport(t, out)
		var env struct {
			Report compliance.Report `json:"report"`
		}
		if err := json.Unmarshal([]byte(js), &env); err != nil {
			t.Fatal(err)
		}
		return env.Report.Summary.Sessions
	}
	if n := sessions(); n != 0 {
		t.Fatalf("projet courant : %d sessions", n)
	}
	if n := sessions("--all-projects"); n != 1 {
		t.Fatalf("--all-projects : %d sessions", n)
	}
	if n := sessions("--project", "../projet"); n != 1 {
		t.Fatalf("--project relatif : %d sessions", n)
	}
}

func TestAuditExportBrokenChainE2E(t *testing.T) {
	e := exportEnv(t)
	dir := filepath.Join(e.dataDir, "audit")
	files, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil || len(files) == 0 {
		t.Fatalf("journaux : %v %v", files, err)
	}
	b, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	altered := strings.Replace(string(b), "test-model", "faux-model", 1)
	if altered == string(b) {
		t.Fatal("rien à altérer")
	}
	if err := os.WriteFile(files[0], []byte(altered), 0o600); err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	code, stdout, errb := e.run("audit", "export", "--lang", "en", "--out", out)
	if code != 2 || !strings.Contains(stdout, "report written: ") || !strings.Contains(errb, "audit log tampered: ") || !strings.Contains(errb, "(report written)") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, errb)
	}
	html, _ := readExport(t, out)
	if !strings.Contains(html, "audit log tampered") {
		t.Fatalf("bandeau absent : %s", html)
	}
}

func TestAuditExportExistingFileE2E(t *testing.T) {
	e := exportEnv(t)
	out := t.TempDir()
	hp, jp := exportPaths(out)
	e.writeFile(jp, "déjà là")
	code, stdout, errb := e.run("audit", "export", "--lang", "en", "--out", out)
	if code != 1 || !strings.Contains(errb, jp+" already exists") || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, errb)
	}
	if _, err := os.Lstat(hp); !os.IsNotExist(err) {
		t.Fatalf("HTML créé : %v", err)
	}
	if b, _ := os.ReadFile(jp); string(b) != "déjà là" {
		t.Fatalf("JSON existant modifié : %q", b)
	}

	if os.Geteuid() == 0 {
		t.Skip("root ignore les droits du dossier")
	}
	ro := t.TempDir()
	if err := os.Chmod(ro, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(ro, 0o700) })
	code, stdout, errb = e.run("audit", "export", "--out", ro)
	if code != 1 || !strings.HasPrefix(errb, "tenon : ") || stdout != "" {
		t.Fatalf("dossier en lecture seule : code=%d stdout=%q stderr=%q", code, stdout, errb)
	}
	if names := listDir(t, ro); len(names) != 0 {
		t.Fatalf("fichiers laissés : %v", names)
	}
}

func TestAuditExportUsageE2E(t *testing.T) {
	e := newEnv(t, nil, "eu")
	out := t.TempDir()
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"--since", "2026-13-01"}, `invalid date "2026-13-01" (expected YYYY-MM-DD)`},
		{[]string{"--until", "hier"}, `invalid date "hier"`},
		{[]string{"--since", "2026-10-10", "--until", "2026-10-01"}, "--since is after --until"},
		{[]string{"--project", "x", "--all-projects"}, "--project, --all-projects and --session cannot be combined"},
		{[]string{"--session", "s_1", "--all-projects"}, "cannot be combined"},
		{[]string{"--project", "x", "--session", "s_1"}, "cannot be combined"},
		{[]string{"en", "trop"}, "unexpected argument: en"},
		{[]string{"--lang", "de"}, ""},
	} {
		args := append([]string{"audit", "export", "--lang", "en", "--out", out}, c.args...)
		code, stdout, errb := e.run(args...)
		if code != 1 || stdout != "" || !strings.Contains(errb, c.want) {
			t.Fatalf("%v : code=%d stdout=%q stderr=%q", c.args, code, stdout, errb)
		}
	}
	if names := listDir(t, out); len(names) != 0 {
		t.Fatalf("fichiers écrits : %v", names)
	}
	// Bornes explicites : nom de fichier tiré des dates données.
	code, stdout, errb := e.run("audit", "export", "--since", "2026-10-01", "--until", "2026-10-01", "--out", out)
	if code != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, errb)
	}
	if names := listDir(t, out); len(names) != 2 || names[0] != "tenon-audit-2026-10-01_2026-10-01.html" || names[1] != "tenon-audit-2026-10-01_2026-10-01.json" {
		t.Fatalf("fichiers : %v", names)
	}
}

func TestAuditExportNoAuditDirE2E(t *testing.T) {
	e := newEnv(t, nil, "eu")
	code, stdout, errb := e.run("audit", "export", "--lang", "en")
	if code != 0 || errb != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, errb)
	}
	// --out par défaut : le dossier de travail.
	html, _ := readExport(t, e.wd)
	if !strings.Contains(html, "no activity in this period") {
		t.Fatalf("mention absente : %s", html)
	}
	if _, err := os.Stat(filepath.Join(e.dataDir, "audit")); !os.IsNotExist(err) {
		t.Fatalf("dossier d'audit créé : %v", err)
	}
}

func TestAuditExportRedactsE2E(t *testing.T) {
	e := exportEnv(t)
	ents, err := e.sessionFiles()
	if err != nil || len(ents) != 1 {
		t.Fatalf("sessions : %v %v", ents, err)
	}
	sid := strings.TrimSuffix(ents[0].Name(), ".jsonl")
	// Ligne chaînée valide, secret en clair (aucun masquage à l'écriture).
	w, err := audit.Open(filepath.Join(e.dataDir, "audit"), audit.Full, nil)
	if err != nil {
		t.Fatal(err)
	}
	ev, err := event.New(sid, 999, time.Now().UTC(), event.Error, event.ErrorPayload{Code: "x", Message: "échec MISTRAL_API_KEY=Zx8kq2LmNp4Rt6Vw9YbC1dEf"})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Write(ev); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.auditContents(), "Zx8kq") {
		t.Fatal("secret absent du journal : le test ne prouve rien")
	}
	out := t.TempDir()
	if code, stdout, errb := e.run("audit", "export", "--all-projects", "--out", out); code != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, errb)
	}
	html, js := readExport(t, out)
	for _, s := range []string{html, js} {
		if strings.Contains(s, "Zx8kq") || !strings.Contains(s, "MISTRAL_API_KEY=[SECRET_1]") {
			t.Fatalf("secret non masqué : %s", s)
		}
	}
}
