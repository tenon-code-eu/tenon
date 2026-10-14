package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"codeberg.org/tenon/tenon/internal/event"
)

// fakeLLM est un serveur compatible OpenAI piloté par script : la requête n
// reçoit script[n] (la dernière entrée une fois le script épuisé).
type fakeLLM struct {
	srv    *httptest.Server
	script [][]string

	mu     sync.Mutex
	bodies []string
}

func newFakeLLM(t *testing.T, script ...[]string) *fakeLLM {
	t.Helper()
	f := &fakeLLM{script: script}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		i := len(f.bodies)
		f.bodies = append(f.bodies, string(b))
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
	cfg := fmt.Sprintf(`default_provider = "test"

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
	var out, errb bytes.Buffer
	code := run(context.Background(), args, &out, &errb, func(k string) string { return e.vars[k] }, e.wd)
	return code, out.String(), errb.String()
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
	if code != 0 || !strings.HasPrefix(out, "OK : ") || !strings.Contains(out, "dernier hash sha256:") {
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
	if code, out, errb := e.run("config", "check"); code != 0 || strings.TrimSpace(out) != "OK" {
		t.Fatalf("config saine : code=%d stdout=%q stderr=%q", code, out, errb)
	}
	e.writeFile(filepath.Join(e.wd, ".tenon", "config.toml"), `
[[permissions.rules]]
tool = "shell"
match = "*"
decision = "allow"
`)
	code, _, errb := e.run("config", "check")
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

func TestNoPromptExit1(t *testing.T) {
	e := newEnv(t, nil, "eu")
	code, _, errb := e.run()
	if code != 1 || !strings.Contains(errb, `l'interface interactive arrive bientôt : utilisez -p "<prompt>"`) {
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
