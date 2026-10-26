package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tenon-code-eu/tenon/internal/config"
	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/i18n"
	"github.com/tenon-code-eu/tenon/internal/mcp/mcptest"
	"github.com/tenon-code-eu/tenon/internal/sandbox"
	"github.com/tenon-code-eu/tenon/internal/session"
	"github.com/zalando/go-keyring"
)

// TestMain remplace le trousseau système par un trousseau en mémoire : aucun
// test ne lit ni n'écrit le trousseau réel. Relancé avec
// TENON_TEST_MCP_SERVER=1, le binaire de test joue le serveur MCP stdio ;
// relancé avec __sandbox, il joue l'assistant d'isolation (Linux).
func TestMain(m *testing.M) {
	sandbox.MaybeMain()
	// Isolation déclarée indisponible et avis muets par défaut : les tests
	// existants lancent leurs commandes sans confinement (hors de leur projet
	// temporaire, vers httptest). Les tests du bac à sable l'activent.
	probeSandbox = func() sandbox.Status {
		return sandbox.Status{Available: false, Mechanism: "none", Network: "none", Reason: "test"}
	}
	sandboxNotices = false
	mcptest.Main()
	keyring.MockInit()
	// Relancé avec TENON_TEST_CLI=1, le binaire de test joue la CLI tenon
	// (hook git des tests).
	if os.Getenv("TENON_TEST_CLI") == "1" {
		wd, _ := os.Getwd()
		os.Exit(run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getenv, wd, func() bool { return false }))
	}
	os.Exit(m.Run())
}

// fakeLLM est un serveur compatible OpenAI piloté par script : la requête n
// reçoit script[n] (la dernière entrée une fois le script épuisé).
type fakeLLM struct {
	srv    *httptest.Server
	script [][]string

	mu     sync.Mutex
	bodies []string
	auths  []string // en-tête Authorization de chaque requête
}

func newFakeLLM(t *testing.T, script ...[]string) *fakeLLM {
	t.Helper()
	f := &fakeLLM{script: script}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		i := len(f.bodies)
		f.bodies = append(f.bodies, string(b))
		f.auths = append(f.auths, r.Header.Get("Authorization"))
		f.mu.Unlock()
		if i >= len(f.script) {
			i = len(f.script) - 1
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, l := range f.script[i] {
			fmt.Fprintf(w, "data: %s\n\n", l)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeLLM) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.bodies...)
}

func (f *fakeLLM) authorizations() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.auths...)
}

func sseText(s string) []string {
	b, _ := json.Marshal(s)
	return []string{fmt.Sprintf(`{"choices":[{"delta":{"content":%s},"finish_reason":"stop"}]}`, b)}
}

func sseCall(name, args string) []string {
	a, _ := json.Marshal(args)
	return []string{fmt.Sprintf(`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":%q,"arguments":%s}}]},"finish_reason":"tool_calls"}]}`, name, a)}
}

// env est un environnement isolé : config, données et HOME temporaires.
type env struct {
	t       *testing.T
	vars    map[string]string
	wd      string
	dataDir string
}

func newEnv(t *testing.T, f *fakeLLM, jurisdiction string) *env {
	t.Helper()
	return newEnvExtra(t, f, jurisdiction, "")
}

// newEnvExtra ajoute extra (TOML) à la fin de la configuration globale.
func newEnvExtra(t *testing.T, f *fakeLLM, jurisdiction, extra string) *env {
	t.Helper()
	base := t.TempDir()
	e := &env{
		t:  t,
		wd: filepath.Join(base, "projet"),
		vars: map[string]string{
			"XDG_CONFIG_HOME": filepath.Join(base, "config"),
			"XDG_DATA_HOME":   filepath.Join(base, "data"),
			"HOME":            filepath.Join(base, "home"),
			"TEST_KEY":        "cle-de-test",
		},
		dataDir: filepath.Join(base, "data", "tenon"),
	}
	for _, d := range []string{e.wd, filepath.Join(base, "config", "tenon"), e.vars["HOME"]} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	url := "http://127.0.0.1:1"
	if f != nil {
		url = f.srv.URL
	}
	// Les tests vérifient les messages français : la langue par défaut
	// (anglais) est couverte par TestDefaultLanguageEnglish.
	cfg := fmt.Sprintf(`lang = "fr"
default_provider = "test"

[[providers]]
id = "test"
name = "Test SAS"
base_url = %q
api_key_env = "TEST_KEY"
model = "test-model"
country = "FR"
jurisdiction = %q
`, url, jurisdiction) + extra
	e.writeFile(filepath.Join(base, "config", "tenon", "config.toml"), cfg)
	return e
}

func (e *env) writeFile(path, content string) {
	e.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) run(args ...string) (int, string, string) {
	e.t.Helper()
	return e.runStdin("", args...)
}

// runStdin exécute la CLI avec stdin comme entrée standard.
func (e *env) runStdin(stdin string, args ...string) (int, string, string) {
	e.t.Helper()
	var out, errb bytes.Buffer
	code := run(context.Background(), args, strings.NewReader(stdin), &out, &errb, func(k string) string { return e.vars[k] }, e.wd, func() bool { return false })
	return code, out.String(), errb.String()
}

// sessionsDir est le dossier des sessions de l'environnement.
func (e *env) sessionsDir() string { return filepath.Join(e.dataDir, "sessions") }

// sessionFiles liste les fichiers <id>.jsonl (sans les fichiers de verrou).
func (e *env) sessionFiles() ([]os.DirEntry, error) {
	ents, err := os.ReadDir(e.sessionsDir())
	var out []os.DirEntry
	for _, de := range ents {
		if strings.HasSuffix(de.Name(), ".jsonl") {
			out = append(out, de)
		}
	}
	return out, err
}

func (e *env) auditContents() string {
	e.t.Helper()
	var sb strings.Builder
	err := filepath.WalkDir(filepath.Join(e.dataDir, "audit"), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			sb.Write(b)
		}
		return nil
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return sb.String()
}

func decodeEvents(t *testing.T, out string) []event.Event {
	t.Helper()
	var evs []event.Event
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var e event.Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("ligne %q : %v", sc.Text(), err)
		}
		evs = append(evs, e)
	}
	return evs
}

func TestHeadlessReadsFileAndAnswers(t *testing.T) {
	f := newFakeLLM(t, sseCall("read_file", `{"path":"README.md"}`), sseText("Le fichier contient bonjour"))
	e := newEnv(t, f, "eu")
	e.writeFile(filepath.Join(e.wd, "README.md"), "bonjour\n")

	code, out, errb := e.run("-p", "que contient README.md ?")
	if code != 0 || !strings.Contains(out, "Le fichier contient bonjour") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errb)
	}
	reqs := f.requests()
	if len(reqs) != 2 || !strings.Contains(reqs[1], "bonjour") {
		t.Fatalf("requêtes : %q", reqs)
	}

	code, out, errb = e.run("audit", "verify")
	// Sans horodatage configuré : aucun jeton.
	if code != 0 || !strings.HasPrefix(out, "OK : ") || !strings.Contains(out, "dernier hash sha256:") || !strings.HasSuffix(out, "\nhorodatages : aucun\n") {
		t.Fatalf("audit verify : code=%d stdout=%q stderr=%q", code, out, errb)
	}
}

func TestHeadlessWriteDeniedExit3(t *testing.T) {
	f := newFakeLLM(t, sseCall("write_file", `{"path":"x.txt","content":"pirate"}`), sseText("ok"))
	e := newEnv(t, f, "eu")

	code, _, errb := e.run("-p", "écris x.txt")
	if code != 3 {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	if _, err := os.Stat(filepath.Join(e.wd, "x.txt")); !os.IsNotExist(err) {
		t.Fatalf("fichier écrit : %v", err)
	}
}

func TestHeadlessNonEUWithoutFlagExit3(t *testing.T) {
	f := newFakeLLM(t, sseText("salut"))
	e := newEnv(t, f, "non-eu")

	code, _, errb := e.run("-p", "salut")
	if code != 3 || len(f.requests()) != 0 {
		t.Fatalf("code=%d requêtes=%d stderr=%q", code, len(f.requests()), errb)
	}
}

func TestHeadlessNonEUWithFlag(t *testing.T) {
	f := newFakeLLM(t, sseText("salut"))
	e := newEnv(t, f, "non-eu")

	code, out, errb := e.run("-p", "salut", "--allow-non-eu")
	if code != 0 || !strings.Contains(errb, "hors UE") || !strings.Contains(out, "salut") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errb)
	}
}

func TestHeadlessJSONOutput(t *testing.T) {
	f := newFakeLLM(t, sseText("salut"))
	e := newEnv(t, f, "eu")

	code, out, errb := e.run("-p", "salut", "--output", "json")
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	evs := decodeEvents(t, out)
	if len(evs) == 0 || evs[0].Type != event.SessionStarted {
		t.Fatalf("événements : %+v", evs)
	}
	var p event.SessionStartedPayload
	if err := json.Unmarshal(evs[0].Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p.Provider.Jurisdiction != "eu" || p.ProjectRoot != e.wd {
		t.Fatalf("SessionStarted : %+v", p)
	}
	if !strings.HasPrefix(evs[0].Session, "s_") || len(evs[0].Session) != 14 {
		t.Fatalf("session %q", evs[0].Session)
	}
}

func TestSecretNeverInAuditOrRequest(t *testing.T) {
	for _, prompt := range []string{
		"MISTRAL_API_KEY=Zx8kq2LmNp4Rt6Vw9YbC1dEf",
		"configure cette clé :\nMISTRAL_API_KEY=Zx8kq2LmNp4Rt6Vw9YbC1dEf",
	} {
		f := newFakeLLM(t, sseText("noté"))
		e := newEnv(t, f, "eu")

		code, _, errb := e.run("-p", prompt)
		if code != 0 {
			t.Fatalf("%q : code=%d stderr=%q", prompt, code, errb)
		}
		reqs := f.requests()
		if len(reqs) == 0 {
			t.Fatalf("%q : aucune requête", prompt)
		}
		for _, b := range reqs {
			if strings.Contains(b, "Zx8kq") {
				t.Fatalf("%q : secret envoyé : %s", prompt, b)
			}
		}
		a := e.auditContents()
		if !strings.Contains(a, "[SECRET_1]") || strings.Contains(a, "Zx8kq") {
			t.Fatalf("%q : audit : %q", prompt, a)
		}
		if code, out, errb := e.run("audit", "verify"); code != 0 {
			t.Fatalf("%q : audit verify : %q %q", prompt, out, errb)
		}
	}
}

func TestConfigCheckRejectsProjectAllow(t *testing.T) {
	e := newEnv(t, nil, "eu")
	if code, out, errb := e.runCheck(); code != 0 || out != routingCheckFR+sandboxCheckFR+craCheck+"OK\n" {
		t.Fatalf("config saine : code=%d stdout=%q stderr=%q", code, out, errb)
	}
	e.writeFile(filepath.Join(e.wd, ".tenon", "config.toml"), `
[[permissions.rules]]
tool = "shell"
match = "*"
decision = "allow"
`)
	code, _, errb := e.runCheck()
	if code != 1 || !strings.Contains(errb, "allow") {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
}

func TestMissingAPIKey(t *testing.T) {
	f := newFakeLLM(t, sseText("salut"))
	e := newEnv(t, f, "eu")
	delete(e.vars, "TEST_KEY")

	code, _, errb := e.run("-p", "salut")
	if code != 1 || !strings.Contains(errb, "TEST_KEY") || len(f.requests()) != 0 {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
}

func TestBannerOnStderr(t *testing.T) {
	f := newFakeLLM(t, sseText("salut"))
	e := newEnv(t, f, "eu")

	code, _, errb := e.run("-p", "salut")
	if code != 0 || !strings.Contains(errb, "tenon · test-model · Test SAS · FR · juridiction UE") {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
}

func TestGrepSkipsSensitiveFiles(t *testing.T) {
	f := newFakeLLM(t, sseCall("grep", `{"pattern":"MOTDEPASSE"}`), sseText("fini"))
	e := newEnv(t, f, "eu")
	e.writeFile(filepath.Join(e.wd, ".env"), "MOTDEPASSE=1\n")
	e.writeFile(filepath.Join(e.wd, "a.txt"), "MOTDEPASSE ici\n")

	code, out, errb := e.run("-p", "cherche", "--output", "json")
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	found := false
	for _, ev := range decodeEvents(t, out) {
		if ev.Type != event.ToolResult {
			continue
		}
		var p event.ToolResultPayload
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			t.Fatal(err)
		}
		if p.Tool != "grep" {
			continue
		}
		found = true
		if !strings.Contains(p.Content, "a.txt") || strings.Contains(p.Content, ".env") {
			t.Fatalf("résultat grep : %q", p.Content)
		}
	}
	if !found {
		t.Fatalf("aucun ToolResult grep : %s", out)
	}
}

// --resume avec une sous-commande garde le message err.no_prompt.
func TestResumeWithSubcommand(t *testing.T) {
	e := newEnv(t, nil, "eu")
	code, _, errb := e.run("--resume", "last", "audit", "verify")
	if code != 1 || !strings.Contains(errb, "--resume ne s'utilise pas avec une sous-commande") {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
}

func TestUnknownProvider(t *testing.T) {
	e := newEnv(t, nil, "eu")
	code, _, errb := e.run("-p", "salut", "--provider", "nope")
	if code != 1 || !strings.Contains(errb, "nope") {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
}

func TestInvalidOutput(t *testing.T) {
	e := newEnv(t, nil, "eu")
	code, _, errb := e.run("-p", "salut", "--output", "xml")
	if code != 1 || !strings.Contains(errb, "xml") {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
}

func TestHeadlessPolicyDenyExit3(t *testing.T) {
	f := newFakeLLM(t, sseCall("write_file", `{"path":"x.txt","content":"pirate"}`), sseText("ok"))
	e := newEnvExtra(t, f, "eu", `
[[permissions.rules]]
tool = "write_file"
decision = "deny"

`)
	code, _, errb := e.run("-p", "écris x.txt")
	if code != 3 {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	if _, err := os.Stat(filepath.Join(e.wd, "x.txt")); !os.IsNotExist(err) {
		t.Fatalf("fichier écrit : %v", err)
	}
}

func TestJSONOutputNeverLeaksSecret(t *testing.T) {
	f := newFakeLLM(t, sseText("noté"))
	e := newEnv(t, f, "eu")
	code, out, errb := e.run("-p", "MISTRAL_API_KEY=Zx8kq2LmNp4Rt6Vw9YbC1dEf", "--output", "json")
	if code != 0 || strings.Contains(out, "Zx8kq") || !strings.Contains(out, "[SECRET_1]") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errb)
	}
	decodeEvents(t, out)
}

func TestToolOutputSecretsNeverLeak(t *testing.T) {
	f := newFakeLLM(t,
		sseCall("read_file", `{"path":"config/app.yml"}`),
		sseCall("grep", `{"pattern":"password"}`),
		sseText("fini"))
	e := newEnv(t, f, "eu")
	e.writeFile(filepath.Join(e.wd, "config", "app.yml"), "db:\n  user: app\n  password: Zx8kq2LmNp4Rt6Vw9YbC1dEf\n")

	code, out, errb := e.run("-p", "lis config/app.yml puis cherche password", "--output", "json")
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	reqs := f.requests()
	if len(reqs) != 3 {
		t.Fatalf("requêtes : %d", len(reqs))
	}
	for _, b := range reqs {
		if strings.Contains(b, "Zx8kq") {
			t.Fatalf("secret envoyé : %s", b)
		}
	}
	if !strings.Contains(reqs[1], "[SECRET_") || !strings.Contains(reqs[2], "app.yml") {
		t.Fatalf("requêtes : %q", reqs)
	}
	if a := e.auditContents(); strings.Contains(a, "Zx8kq") {
		t.Fatalf("audit : %q", a)
	}
	if strings.Contains(out, "Zx8kq") {
		t.Fatalf("stdout : %q", out)
	}
}

func TestProviderKeyValueNeverLeaks(t *testing.T) {
	f := newFakeLLM(t, sseText("noté"))
	e := newEnv(t, f, "eu")
	key := e.vars["TEST_KEY"]

	code, out, errb := e.run("-p", "ma clé est "+key+" voilà", "--output", "json")
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	reqs := f.requests()
	if len(reqs) == 0 {
		t.Fatal("aucune requête")
	}
	for _, b := range reqs {
		if strings.Contains(b, key) {
			t.Fatalf("clé envoyée : %s", b)
		}
	}
	if a := e.auditContents(); strings.Contains(a, key) {
		t.Fatalf("audit : %q", a)
	}
	if strings.Contains(out, key) {
		t.Fatalf("stdout : %q", out)
	}
}

func TestTextOutputFinalAnswerMasked(t *testing.T) {
	f := newFakeLLM(t, sseText("la clé : MISTRAL_API_KEY=Zx8kq2LmNp4Rt6Vw9YbC1dEf"))
	e := newEnv(t, f, "eu")
	code, out, errb := e.run("-p", "donne la clé")
	if code != 0 || strings.Contains(out, "Zx8kq") || !strings.Contains(out, "MISTRAL_API_KEY=[SECRET_") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errb)
	}
}

func TestResumeContinuesConversation(t *testing.T) {
	f := newFakeLLM(t, sseText("enchanté"), sseText("Ada"))
	e := newEnv(t, f, "eu")

	if code, out, errb := e.run("-p", "je m'appelle Ada"); code != 0 {
		t.Fatalf("run 1 : code=%d stdout=%q stderr=%q", code, out, errb)
	}
	code, out, errb := e.run("--resume", "last", "-p", "comment je m'appelle ?")
	if code != 0 || !strings.Contains(out, "Ada") {
		t.Fatalf("run 2 : code=%d stdout=%q stderr=%q", code, out, errb)
	}
	reqs := f.requests()
	if len(reqs) != 2 || !strings.Contains(reqs[1], "je m'appelle Ada") || !strings.Contains(reqs[1], "comment je m'appelle ?") {
		t.Fatalf("requêtes : %q", reqs)
	}
	ents, err := e.sessionFiles()
	if err != nil || len(ents) != 1 {
		t.Fatalf("sessions : %v %v", ents, err)
	}
}

func TestResumeUnknownID(t *testing.T) {
	f := newFakeLLM(t, sseText("salut"))
	e := newEnv(t, f, "eu")
	code, _, errb := e.run("--resume", "s_000000000000", "-p", "salut")
	if code != 1 || len(f.requests()) != 0 || errb == "" {
		t.Fatalf("code=%d requêtes=%d stderr=%q", code, len(f.requests()), errb)
	}
}

func TestResumeOtherProjectRefused(t *testing.T) {
	f := newFakeLLM(t, sseText("ok"))
	e := newEnv(t, f, "eu")
	if code, _, errb := e.run("-p", "secret du projet A"); code != 0 {
		t.Fatalf("run A : code=%d stderr=%q", code, errb)
	}
	ents, err := e.sessionFiles()
	if err != nil || len(ents) != 1 {
		t.Fatalf("sessions : %v %v", ents, err)
	}
	path := filepath.Join(e.sessionsDir(), ents[0].Name())
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	id := strings.TrimSuffix(ents[0].Name(), ".jsonl")

	e.wd = filepath.Join(filepath.Dir(e.wd), "projetB")
	if err := os.MkdirAll(e.wd, 0o700); err != nil {
		t.Fatal(err)
	}
	_, _, unknownErr := e.run("--resume", "s_000000000000", "-p", "x")
	code, _, errb := e.run("--resume", id, "-p", "x")
	if code != 1 || len(f.requests()) != 1 {
		t.Fatalf("code=%d requêtes=%d stderr=%q", code, len(f.requests()), errb)
	}
	// Même message qu'un identifiant inconnu (à l'identifiant près).
	if strings.ReplaceAll(errb, id, "s_000000000000") != unknownErr {
		t.Fatalf("stderr=%q, attendu comme %q", errb, unknownErr)
	}
	after, err := os.Stat(path)
	if err != nil || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("session A modifiée : %v → %v (%v)", before.Size(), after.Size(), err)
	}
}

func TestSessionsList(t *testing.T) {
	f := newFakeLLM(t, sseText("ok"))
	e := newEnv(t, f, "eu")

	if code, out, errb := e.run("sessions"); code != 0 || strings.TrimSpace(out) != "aucune session" {
		t.Fatalf("sessions vide : code=%d stdout=%q stderr=%q", code, out, errb)
	}
	if code, _, errb := e.run("-p", "premier prompt"); code != 0 {
		t.Fatalf("run 1 : code=%d stderr=%q", code, errb)
	}
	// La première session est datée d'une heure plus tôt : l'ordre ne dépend
	// pas de la résolution des horodatages du système de fichiers.
	ents, err := e.sessionFiles()
	if err != nil || len(ents) != 1 {
		t.Fatalf("sessions : %v %v", ents, err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(e.sessionsDir(), ents[0].Name()), old, old); err != nil {
		t.Fatal(err)
	}
	if code, _, errb := e.run("-p", "second prompt"); code != 0 {
		t.Fatalf("run 2 : code=%d stderr=%q", code, errb)
	}

	code, out, errb := e.run("sessions")
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 || lines[0] != "sessions (2) :" {
		t.Fatalf("stdout=%q", out)
	}
	line := regexp.MustCompile(`^s_[0-9a-f]{12}  \d{4}-\d{2}-\d{2} \d{2}:\d{2}  1  (.*)$`)
	for i, want := range []string{"second prompt", "premier prompt"} {
		m := line.FindStringSubmatch(lines[i+1])
		if m == nil || m[1] != want {
			t.Fatalf("ligne %d : %q (attendu %q)", i+1, lines[i+1], want)
		}
	}
}

func TestSessionFileHasNoSecret(t *testing.T) {
	f := newFakeLLM(t, sseText("noté"))
	e := newEnv(t, f, "eu")
	code, _, errb := e.run("-p", "MISTRAL_API_KEY=Zx8kq2LmNp4Rt6Vw9YbC1dEf")
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	ents, err := e.sessionFiles()
	if err != nil || len(ents) == 0 {
		t.Fatalf("sessions : %v %v", ents, err)
	}
	for _, de := range ents {
		b, err := os.ReadFile(filepath.Join(e.sessionsDir(), de.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "Zx8kq") || !strings.Contains(string(b), "[SECRET_1]") {
			t.Fatalf("session %s : %s", de.Name(), b)
		}
	}
}

func TestAgentsMdInSystemPrompt(t *testing.T) {
	f := newFakeLLM(t, sseText("ok"))
	e := newEnv(t, f, "eu")
	e.writeFile(filepath.Join(e.wd, "AGENTS.md"), "Utilise des tabulations.\n")

	code, _, errb := e.run("-p", "salut")
	if code != 0 || !strings.Contains(errb, "instructions : AGENTS.md") {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	reqs := f.requests()
	if len(reqs) != 1 {
		t.Fatalf("requêtes : %q", reqs)
	}
	var body struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(reqs[0]), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Messages) == 0 || body.Messages[0].Role != "system" ||
		!strings.Contains(body.Messages[0].Content, "Utilise des tabulations.") {
		t.Fatalf("messages : %+v", body.Messages)
	}
}

func TestEnglishBanner(t *testing.T) {
	f := newFakeLLM(t, sseText("hi"))
	e := newEnv(t, f, "eu")
	code, _, errb := e.run("--lang", "en", "-p", "hello")
	if code != 0 || !strings.Contains(errb, "jurisdiction EU") {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	if reqs := f.requests(); len(reqs) != 1 || !strings.Contains(reqs[0], "Answer in English") {
		t.Fatalf("requêtes : %q", reqs)
	}
}

func TestLangFlagOverrides(t *testing.T) {
	f := newFakeLLM(t, sseText("salut"))
	e := newEnv(t, f, "eu")
	e.vars["LANG"] = "en_US.UTF-8"

	code, _, errb := e.run("-p", "salut", "--lang", "fr")
	if code != 0 || !strings.Contains(errb, "juridiction UE") {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	if reqs := f.requests(); len(reqs) != 1 || !strings.Contains(reqs[0], "Réponds en français") {
		t.Fatalf("requêtes : %q", reqs)
	}
}

func TestCompactionEndToEnd(t *testing.T) {
	f := newFakeLLM(t, sseText("ok"))
	// La fenêtre doit contenir le prompt système et les schémas des outils
	// (environ 900 jetons estimés avec l'outil agent) : 1500 jetons, seuil 0,8.
	// Mémoire coupée : ses trois outils ne comptent pas dans ce calibrage.
	e := newEnvExtra(t, f, "eu", "context_window = 1500\n\n[memory]\nenabled = false\n")
	long := strings.Repeat("mot ", 200) // 200 jetons estimés

	for i := range 3 {
		args := []string{"-p", fmt.Sprintf("run %d : %s", i, long)}
		if i > 0 {
			args = append(args, "--resume", "last")
		}
		code, out, errb := e.run(args...)
		if code != 0 {
			t.Fatalf("run %d : code=%d stdout=%q stderr=%q", i, code, out, errb)
		}
		if i == 2 && !strings.Contains(errb, "contexte compacté : ") {
			t.Fatalf("run %d : stderr=%q", i, errb)
		}
	}
	summaries := 0
	for _, b := range f.requests() {
		if strings.Contains(b, "Faithfully summarize") {
			summaries++
		}
	}
	if summaries == 0 {
		t.Fatalf("aucune requête de résumé : %d requêtes", len(f.requests()))
	}
}

func TestKeyFromKeyring(t *testing.T) {
	keyring.MockInit()
	t.Cleanup(keyring.MockInit)
	f := newFakeLLM(t, sseText("salut"))
	e := newEnv(t, f, "eu")
	delete(e.vars, "TEST_KEY")
	const key = "cle-du-trousseau-42"

	code, out, errb := e.runStdin(key+"\n", "key", "set", "test")
	if code != 0 || !strings.Contains(out+errb, "clé enregistrée pour test") {
		t.Fatalf("key set : code=%d stdout=%q stderr=%q", code, out, errb)
	}
	code, out, errb = e.run("-p", "salut")
	if code != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errb)
	}
	if a := f.authorizations(); len(a) != 1 || a[0] != "Bearer "+key {
		t.Fatalf("Authorization : %q", a)
	}
	// La clé du trousseau est masquée comme une clé d'environnement.
	if strings.Contains(e.auditContents(), key) {
		t.Fatal("clé du trousseau dans l'audit")
	}
	if strings.Contains(out, key) || strings.Contains(errb, key) {
		t.Fatalf("clé du trousseau affichée : stdout=%q stderr=%q", out, errb)
	}
	ents, err := e.sessionFiles()
	if err != nil || len(ents) == 0 {
		t.Fatalf("sessions : %v %v", ents, err)
	}
	for _, de := range ents {
		b, err := os.ReadFile(filepath.Join(e.sessionsDir(), de.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), key) {
			t.Fatalf("clé du trousseau dans la session %s", de.Name())
		}
	}

	if code, out, errb := e.run("key", "delete", "test"); code != 0 || !strings.Contains(out+errb, "clé supprimée pour test") {
		t.Fatalf("key delete : code=%d stdout=%q stderr=%q", code, out, errb)
	}
	if code, _, _ := e.run("-p", "salut"); code != 1 {
		t.Fatalf("après suppression : code=%d", code)
	}
	if code, _, _ := e.runStdin("x\n", "key", "set", "nope"); code != 1 {
		t.Fatalf("fournisseur inconnu : code=%d", code)
	}
}

func TestContextTooLargeExit1(t *testing.T) {
	f := newFakeLLM(t, sseText("ok"))
	e := newEnvExtra(t, f, "eu", "context_window = 1200\n")
	code, out, errb := e.run("-p", strings.Repeat("mot ", 4000))
	if code != 1 || len(f.requests()) != 0 {
		t.Fatalf("code=%d requêtes=%d stdout=%q stderr=%q", code, len(f.requests()), out, errb)
	}
}

// requestMessages décode les messages d'une requête envoyée au fournisseur.
func requestMessages(t *testing.T, body string) []map[string]any {
	t.Helper()
	var b struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.Unmarshal([]byte(body), &b); err != nil {
		t.Fatal(err)
	}
	return b.Messages
}

// Après un tour context_too_large, la reprise compacte avec une requête de
// résumé qui tient dans la fenêtre.
func TestResumeAfterContextTooLarge(t *testing.T) {
	f := newFakeLLM(t, sseText("ok"))
	// Mémoire coupée : ses trois outils ne comptent pas dans ce calibrage.
	e := newEnvExtra(t, f, "eu", "context_window = 1200\n\n[memory]\nenabled = false\n")
	if code, out, errb := e.run("-p", strings.Repeat("mot ", 4000)); code != 1 {
		t.Fatalf("run 1 : code=%d stdout=%q stderr=%q", code, out, errb)
	}
	code, out, errb := e.run("--resume", "last", "-p", "petit")
	if code != 0 {
		t.Fatalf("run 2 : code=%d stdout=%q stderr=%q", code, out, errb)
	}
	reqs := f.requests()
	if len(reqs) != 2 || !strings.Contains(reqs[0], "Faithfully summarize") {
		t.Fatalf("requêtes : %d", len(reqs))
	}
	n := 0
	for _, m := range requestMessages(t, reqs[0]) {
		s, _ := m["content"].(string)
		n += len([]rune(s))
	}
	if n/4 > 960 {
		t.Fatalf("requête de résumé de %d jetons estimés (limite 960)", n/4)
	}
}

// La clé d'un autre fournisseur configuré, lue dans le trousseau, est masquée
// sous ses formes encodées quand un outil l'affiche.
func TestOtherProviderKeyringKeyNeverLeaks(t *testing.T) {
	keyring.MockInit()
	t.Cleanup(keyring.MockInit)
	const key = "cle-autre-fournisseur-77"
	b64 := base64.StdEncoding.EncodeToString([]byte(key))
	hx := hex.EncodeToString([]byte(key))
	if err := keyring.Set("tenon", "autre", key); err != nil {
		t.Fatal(err)
	}
	cmd := "echo go-keyring-base64:" + b64 + " go-keyring-encoded:" + hx
	f := newFakeLLM(t, sseCall("shell", `{"command":"`+cmd+`"}`), sseText("fini"))
	e := newEnvExtra(t, f, "eu", `
[[providers]]
id = "autre"
name = "Autre"
base_url = "http://127.0.0.1:1"
model = "m"
country = "FR"
jurisdiction = "eu"

[[permissions.rules]]
tool = "shell"
match = "echo *"
decision = "allow"
`)
	code, out, errb := e.run("-p", "affiche", "--output", "json")
	if code != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errb)
	}
	ents, err := e.sessionFiles()
	if err != nil || len(ents) != 1 {
		t.Fatalf("sessions : %v %v", ents, err)
	}
	sess, err := os.ReadFile(filepath.Join(e.sessionsDir(), ents[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	reqs := f.requests()
	if len(reqs) != 2 || !strings.Contains(reqs[1], `"role":"tool"`) {
		t.Fatalf("requêtes : %q", reqs)
	}
	for name, s := range map[string]string{
		"requêtes": strings.Join(reqs, "\n"), "audit": e.auditContents(), "session": string(sess), "stdout": out,
	} {
		for _, form := range []string{key, b64, hx} {
			if strings.Contains(s, form) {
				t.Errorf("%s contient %q : %s", name, form, s)
			}
		}
	}
}

// Un fournisseur local sans api_key_env ne consulte pas le trousseau.
func TestLocalProviderSkipsKeyring(t *testing.T) {
	keyring.MockInit()
	t.Cleanup(keyring.MockInit)
	if err := keyring.Set("tenon", "ollama", "cle-locale-inutile"); err != nil {
		t.Fatal(err)
	}
	v, src, err := providerKey(config.Provider{ID: "ollama", Jurisdiction: "local"}, func(string) string { return "" })
	if v != "" || src != "" || err != nil {
		t.Fatalf("providerKey : %q %q %v", v, src, err)
	}
	if v, src, _ := providerKey(config.Provider{ID: "ollama", Jurisdiction: "eu"}, func(string) string { return "" }); v == "" || src != "keyring" {
		t.Fatalf("fournisseur eu : %q %q", v, src)
	}
}

// Les marqueurs d'une session reprise ne sont jamais réattribués à un autre
// secret.
func TestResumeMarkersDoNotCollide(t *testing.T) {
	f := newFakeLLM(t, sseText("noté"))
	e := newEnv(t, f, "eu")
	if code, _, errb := e.run("-p", "MISTRAL_API_KEY=Zx8kq2LmNp4Rt6Vw9YbC1dEf"); code != 0 {
		t.Fatalf("run 1 : code=%d stderr=%q", code, errb)
	}
	if code, _, errb := e.run("--resume", "last", "-p", "OTHER_TOKEN=Qw3rTy7UiOp9AsDf2GhJ4kLm"); code != 0 {
		t.Fatalf("run 2 : code=%d stderr=%q", code, errb)
	}
	reqs := f.requests()
	if len(reqs) != 2 || !strings.Contains(reqs[1], "MISTRAL_API_KEY=[SECRET_1]") ||
		!strings.Contains(reqs[1], "OTHER_TOKEN=[SECRET_2]") {
		t.Fatalf("requêtes : %q", reqs)
	}
}

// Les messages des nouveaux chemins principaux suivent --lang en.
func TestEnglishCLIMessages(t *testing.T) {
	f := newFakeLLM(t, sseText("ok"))
	e := newEnv(t, f, "eu")
	french := []string{"aucune", "trouvée", "invalide", "clé vide", "lecture", "afficher", "fournisseur", "instructions du projet", "usage :"}
	check := func(name string, errb string, want ...string) {
		t.Helper()
		for _, w := range want {
			if !strings.Contains(errb, w) {
				t.Errorf("%s : stderr=%q, attendu %q", name, errb, w)
			}
		}
		for _, w := range french {
			if strings.Contains(errb, w) {
				t.Errorf("%s : stderr en français (%q) : %q", name, w, errb)
			}
		}
	}
	code, _, errb := e.run("--lang", "en", "--resume", "s_000000000000", "-p", "x")
	if code != 1 || errb != "tenon: session s_000000000000: no session found\n" {
		t.Errorf("session inconnue : code=%d stderr=%q", code, errb)
	}
	code, _, errb = e.run("--lang", "en", "--resume", "pas-un-id", "-p", "x")
	check("identifiant invalide", errb, "invalid session ID")
	if code != 1 {
		t.Errorf("identifiant invalide : code=%d", code)
	}
	code, _, errb = e.run("--lang", "en", "--resume", "last", "-p", "x")
	check("aucune session", errb, "no session")
	if code != 1 {
		t.Errorf("aucune session : code=%d", code)
	}
	code, _, errb = e.runStdin("\n", "--lang", "en", "key", "set", "test")
	check("clé vide", errb, "empty key")
	if code != 1 {
		t.Errorf("clé vide : code=%d", code)
	}
	code, _, errb = e.run("--lang", "en", "-h")
	check("aide", errb, "usage: tenon", "print the version and exit", "<provider>")
	if code != 0 {
		t.Errorf("aide : code=%d", code)
	}
	code, _, errb = e.run("-lang=en", "-h")
	check("aide -lang=", errb, "usage: tenon")
	if code != 0 {
		t.Errorf("aide -lang= : code=%d", code)
	}
	if _, err := readKey(i18n.EN, "test", nil, io.Discard); err == nil || err.Error() != "reading the key: standard input missing" {
		t.Errorf("readKey sans stdin : %v", err)
	}
	if len(f.requests()) != 0 {
		t.Errorf("%d requêtes", len(f.requests()))
	}

	agents := filepath.Join(e.wd, "AGENTS.md")
	e.writeFile(agents, "x")
	if err := os.Chmod(agents, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(agents, 0o600) })
	if _, err := os.ReadFile(agents); err == nil {
		t.Skip("fichier illisible lisible (root ?)")
	}
	code, _, errb = e.run("--lang", "en", "-p", "x")
	check("instructions illisibles", errb, "project instructions: ")
	if code != 1 {
		t.Errorf("instructions illisibles : code=%d", code)
	}
}

// Un dossier de travail atteint par un lien symbolique voit les sessions du
// projet.
func TestSymlinkedWorkdirSeesSessions(t *testing.T) {
	f := newFakeLLM(t, sseText("ok"))
	e := newEnv(t, f, "eu")
	if code, _, errb := e.run("-p", "premier"); code != 0 {
		t.Fatalf("run 1 : code=%d stderr=%q", code, errb)
	}
	link := filepath.Join(filepath.Dir(e.wd), "lien")
	if err := os.Symlink(e.wd, link); err != nil {
		t.Fatal(err)
	}
	e.wd = link
	if code, out, errb := e.run("sessions"); code != 0 || !strings.HasPrefix(out, "sessions (1) :") {
		t.Fatalf("sessions : code=%d stdout=%q stderr=%q", code, out, errb)
	}
	if code, _, errb := e.run("--resume", "last", "-p", "suite"); code != 0 {
		t.Fatalf("--resume last : code=%d stderr=%q", code, errb)
	}
}

// La reprise envoie exactement l'historique qu'aurait envoyé le processus
// d'origine : dernière requête du run 1, réponse finale, nouveau message.
func TestResumeMatchesLiveHistory(t *testing.T) {
	f := newFakeLLM(t, sseCall("write_file", `{"path":"x.txt","content":"pirate"}`), sseText("fini"), sseText("suite ok"))
	e := newEnvExtra(t, f, "eu", `
[[permissions.rules]]
tool = "write_file"
decision = "deny"
`)
	if code, _, errb := e.run("-p", "écris x.txt avec MISTRAL_API_KEY=Zx8kq2LmNp4Rt6Vw9YbC1dEf"); code != 3 {
		t.Fatalf("run 1 : code=%d stderr=%q", code, errb)
	}
	if code, _, errb := e.run("--resume", "last", "-p", "suite"); code != 0 {
		t.Fatalf("run 2 : code=%d stderr=%q", code, errb)
	}
	reqs := f.requests()
	if len(reqs) != 3 {
		t.Fatalf("requêtes : %d", len(reqs))
	}
	for _, b := range reqs {
		if strings.Contains(b, "Zx8kq") {
			t.Fatalf("secret envoyé : %s", b)
		}
	}
	live := requestMessages(t, reqs[1])
	want := append(live,
		map[string]any{"role": "assistant", "content": "fini"},
		map[string]any{"role": "user", "content": "suite"})
	got := requestMessages(t, reqs[2])
	gj, _ := json.Marshal(got)
	wj, _ := json.Marshal(want)
	if string(gj) != string(wj) {
		t.Fatalf("messages repris\n%s\nattendu\n%s", gj, wj)
	}
	if !strings.Contains(string(gj), "[SECRET_1]") || !strings.Contains(string(gj), "denied by policy") {
		t.Fatalf("messages repris : %s", gj)
	}
}

func TestResumeLockedSession(t *testing.T) {
	f := newFakeLLM(t, sseText("ok"))
	e := newEnv(t, f, "eu")
	if code, _, errb := e.run("-p", "bonjour"); code != 0 {
		t.Fatalf("run 1 : code=%d stderr=%q", code, errb)
	}
	ents, err := e.sessionFiles()
	if err != nil || len(ents) < 1 {
		t.Fatalf("sessions : %v %v", ents, err)
	}
	var id string
	for _, en := range ents {
		if strings.HasSuffix(en.Name(), ".jsonl") {
			id = strings.TrimSuffix(en.Name(), ".jsonl")
		}
	}
	held, err := session.Open(e.sessionsDir(), id, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	before := len(f.requests())

	code, _, errb := e.run("--lang", "en", "--resume", id, "-p", "x")
	if code != 1 || !strings.Contains(errb, "session "+id+" is already open in another terminal") {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	if len(f.requests()) != before {
		t.Fatalf("requêtes envoyées malgré le verrou : %d -> %d", before, len(f.requests()))
	}
}

// Le verrou de la session est pris avant sa lecture : une session ouverte
// ailleurs (dont la dernière ligne est en cours d'écriture) n'est ni lue ni
// signalée comme endommagée.
func TestResumeLockedBeforeLoad(t *testing.T) {
	f := newFakeLLM(t, sseText("ok"))
	e := newEnv(t, f, "eu")
	if code, _, errb := e.run("-p", "bonjour"); code != 0 {
		t.Fatalf("run 1 : code=%d stderr=%q", code, errb)
	}
	ents, err := e.sessionFiles()
	if err != nil || len(ents) != 1 {
		t.Fatalf("sessions : %v %v", ents, err)
	}
	id := strings.TrimSuffix(ents[0].Name(), ".jsonl")
	held, err := session.Open(e.sessionsDir(), id, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	// L'autre processus est en train d'écrire une ligne.
	p := filepath.Join(e.sessionsDir(), id+".jsonl")
	fh, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fh.WriteString(`{"type":"assistant_mes`); err != nil {
		t.Fatal(err)
	}
	fh.Close()
	before := len(f.requests())

	for _, args := range [][]string{{"--lang", "en", "--resume", id, "-p", "x"}, {"--lang", "en", "--resume", "last", "-p", "x"}} {
		code, _, errb := e.run(args...)
		if code != 1 || !strings.Contains(errb, "session "+id+" is already open in another terminal") {
			t.Fatalf("%v : code=%d stderr=%q", args, code, errb)
		}
		if strings.Contains(errb, "unreadable") {
			t.Fatalf("%v : session lue malgré le verrou : %q", args, errb)
		}
	}
	if len(f.requests()) != before {
		t.Fatalf("requêtes envoyées malgré le verrou : %d -> %d", before, len(f.requests()))
	}
	if _, err := os.Stat(p + ".corrupt"); !os.IsNotExist(err) {
		t.Fatalf("session réparée malgré le verrou : %v", err)
	}
}

// Sans lang dans la configuration, l'interface est en anglais, même avec
// LANG=fr_FR.UTF-8.
func TestDefaultLanguageEnglish(t *testing.T) {
	f := newFakeLLM(t, sseText("hello"))
	e := newEnv(t, f, "eu")
	p := filepath.Join(filepath.Dir(e.dataDir), "..", "config", "tenon", "config.toml")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	e.writeFile(p, strings.Replace(string(b), "lang = \"fr\"\n", "", 1))
	e.vars["LANG"] = "fr_FR.UTF-8"
	code, _, errb := e.run("-p", "hello")
	if code != 0 || !strings.Contains(errb, "tenon · test-model · Test SAS · FR · jurisdiction EU") {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
}
