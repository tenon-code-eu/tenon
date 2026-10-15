package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/charmbracelet/x/exp/teatest"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"codeberg.org/tenon/tenon/internal/mcp/mcptest"
)

// stdioMCP déclare le serveur MCP stdio de test « test » (binaire de test
// relancé en mode mode) ; ses appels sont consignés dans le fichier renvoyé.
func stdioMCP(t *testing.T, e *env, mode string) string {
	t.Helper()
	log := filepath.Join(t.TempDir(), "mcp.log")
	e.vars[mcptest.EnvServer] = "1"
	e.vars[mcptest.EnvMode] = mode
	e.vars[mcptest.EnvLog] = log
	e.vars["PATH"] = os.Getenv("PATH")
	return log
}

func stdioDecl(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf(`
[[mcp.servers]]
id = "test"
transport = "stdio"
command = [%q]
env_from = [%q, %q, %q]
`, exe, mcptest.EnvServer, mcptest.EnvMode, mcptest.EnvLog)
}

func allowRule(tool string) string {
	return fmt.Sprintf(`
[[permissions.rules]]
tool = %q
decision = "allow"
`, tool)
}

// mcpLog renvoie les appels d'outil (noms) et les pid consignés par le serveur.
func mcpLog(t *testing.T, path string) (calls []string, pids []int) {
	t.Helper()
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if p, ok := strings.CutPrefix(l, "pid "); ok {
			n, err := strconv.Atoi(p)
			if err != nil {
				t.Fatalf("pid %q", p)
			}
			pids = append(pids, n)
		} else if l != "" {
			calls = append(calls, l)
		}
	}
	return calls, pids
}

// waitGone attend la fin du processus pid (borne : 5 s).
func waitGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("serveur MCP %d toujours vivant", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestMCPToolAllowedByRule(t *testing.T) {
	f := newFakeLLM(t, sseCall("mcp__test__echo", `{"text":"coucou-mcp"}`), sseText("fini"))
	e := newEnvExtra(t, f, "eu", stdioDecl(t)+allowRule("mcp__test__echo"))
	log := stdioMCP(t, e, "")

	code, out, errb := e.run("-p", "appelle echo")
	if code != 0 || !strings.Contains(out, "fini") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errb)
	}
	if !strings.Contains(errb, "MCP : test (locale)") {
		t.Fatalf("bannière MCP absente : %q", errb)
	}
	reqs := f.requests()
	if len(reqs) != 2 || !strings.Contains(reqs[1], "coucou-mcp") || !strings.Contains(reqs[0], "mcp__test__echo") {
		t.Fatalf("requêtes : %q", reqs)
	}
	calls, pids := mcpLog(t, log)
	if len(calls) != 1 || calls[0] != "echo" || len(pids) != 1 {
		t.Fatalf("appels %v pids %v", calls, pids)
	}
	// Manager.Close à la sortie : le serveur stdio ne survit pas.
	waitGone(t, pids[0])
	if code, out, errb := e.run("audit", "verify"); code != 0 {
		t.Fatalf("audit verify : %q %q", out, errb)
	}
}

func TestMCPToolAskDeniedHeadless(t *testing.T) {
	f := newFakeLLM(t, sseCall("mcp__test__echo", `{"text":"x"}`), sseText("fini"))
	e := newEnvExtra(t, f, "eu", stdioDecl(t))
	log := stdioMCP(t, e, "")

	code, _, errb := e.run("-p", "appelle echo")
	if code != 3 {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	if calls, _ := mcpLog(t, log); len(calls) != 0 {
		t.Fatalf("appels reçus : %v", calls)
	}
}

func TestServerCrashMidCall(t *testing.T) {
	f := newFakeLLM(t, sseCall("mcp__test__crash", `{}`), sseText("on continue"))
	e := newEnvExtra(t, f, "eu", stdioDecl(t)+allowRule("mcp__test__crash"))
	stdioMCP(t, e, "")

	code, out, errb := e.run("-p", "plante")
	if code != 0 || !strings.Contains(out, "on continue") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errb)
	}
	if !strings.Contains(errb, "serveur MCP test indisponible") {
		t.Fatalf("avis mcp.down absent : %q", errb)
	}
	if code, out, errb := e.run("audit", "verify"); code != 0 {
		t.Fatalf("audit verify : %q %q", out, errb)
	}
}

func TestUnreachableServerAtStart(t *testing.T) {
	f := newFakeLLM(t, sseText("salut"))
	e := newEnvExtra(t, f, "eu", `
[[mcp.servers]]
id = "absent"
transport = "stdio"
command = ["/nonexistent/tenon-mcp-server"]
`)
	code, out, errb := e.run("-p", "salut")
	if code != 0 || !strings.Contains(out, "salut") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errb)
	}
	if !strings.Contains(errb, "serveur MCP absent indisponible") {
		t.Fatalf("avis mcp.down absent : %q", errb)
	}
	if strings.Contains(errb, "MCP : absent") {
		t.Fatalf("serveur injoignable listé comme actif : %q", errb)
	}
}

// httpMCP démarre un serveur MCP HTTP de test (outils echo et whoami, ce
// dernier renvoyant le jeton Bearer reçu) et compte les appels d'outil reçus.
func httpMCP(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	s := sdk.NewServer(&sdk.Implementation{Name: "h", Version: "1"}, nil)
	obj := map[string]any{"type": "object"}
	s.AddTool(&sdk.Tool{Name: "echo", InputSchema: obj}, func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		var a struct{ Text string }
		_ = json.Unmarshal(req.Params.Arguments, &a)
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "écho " + a.Text}}}, nil
	})
	s.AddTool(&sdk.Tool{Name: "whoami", InputSchema: obj}, func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		tok := strings.TrimPrefix(req.Extra.Header.Get("Authorization"), "Bearer ")
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "jeton : " + tok}}}, nil
	})
	h := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return s }, nil)
	var calls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			b, _ := io.ReadAll(r.Body)
			if strings.Contains(string(b), `"tools/call"`) {
				calls.Add(1)
			}
			r.Body = io.NopCloser(strings.NewReader(string(b)))
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	return ts, &calls
}

func httpDecl(url, jurisdiction, extra string) string {
	return fmt.Sprintf(`
[[mcp.servers]]
id = "web"
transport = "http"
url = %q
jurisdiction = %q
country = "US"
%s`, url+"/mcp", jurisdiction, extra)
}

func TestNonEUServerRefusedHeadless(t *testing.T) {
	ts, calls := httpMCP(t)
	f := newFakeLLM(t, sseCall("mcp__web__echo", `{"text":"x"}`), sseText("fini"))
	e := newEnvExtra(t, f, "eu", httpDecl(ts.URL, "non-eu", "")+allowRule("mcp__web__echo"))

	code, _, errb := e.run("-p", "appelle echo")
	if code != 3 || calls.Load() != 0 {
		t.Fatalf("code=%d appels=%d stderr=%q", code, calls.Load(), errb)
	}
}

func TestNonEUServerWithFlag(t *testing.T) {
	ts, calls := httpMCP(t)
	f := newFakeLLM(t, sseCall("mcp__web__echo", `{"text":"x"}`), sseText("fini"))
	e := newEnvExtra(t, f, "eu", httpDecl(ts.URL, "non-eu", "")+allowRule("mcp__web__echo"))

	code, out, errb := e.run("-p", "appelle echo", "--allow-non-eu")
	if code != 0 || calls.Load() != 1 || !strings.Contains(out, "fini") {
		t.Fatalf("code=%d appels=%d stdout=%q stderr=%q", code, calls.Load(), out, errb)
	}
	if reqs := f.requests(); len(reqs) != 2 || !strings.Contains(reqs[1], "écho x") {
		t.Fatalf("requêtes : %q", reqs)
	}
}

func TestMCPSecretNeverLeaks(t *testing.T) {
	const tok = "jeton-mcp-Qw7rT9yU3iO"
	ts, calls := httpMCP(t)
	f := newFakeLLM(t, sseCall("mcp__web__whoami", `{}`), sseText("fini"))
	e := newEnvExtra(t, f, "eu", httpDecl(ts.URL, "eu", `bearer_env = "TOKEN_MCP"`)+allowRule("mcp__web__whoami"))
	e.vars["TOKEN_MCP"] = tok

	code, out, errb := e.run("-p", "qui suis-je ?", "--output", "json")
	if code != 0 || calls.Load() != 1 {
		t.Fatalf("code=%d appels=%d stderr=%q", code, calls.Load(), errb)
	}
	reqs := f.requests()
	if len(reqs) != 2 || !strings.Contains(reqs[1], "jeton : [SECRET_") {
		t.Fatalf("requêtes : %q", reqs)
	}
	for _, b := range reqs {
		if strings.Contains(b, tok) {
			t.Fatalf("jeton envoyé : %s", b)
		}
	}
	if a := e.auditContents(); strings.Contains(a, tok) {
		t.Fatalf("audit : %q", a)
	}
	ents, err := e.sessionFiles()
	if err != nil || len(ents) != 1 {
		t.Fatalf("sessions %v %v", ents, err)
	}
	b, err := os.ReadFile(filepath.Join(e.sessionsDir(), ents[0].Name()))
	if err != nil || strings.Contains(string(b), tok) {
		t.Fatalf("session : %q %v", b, err)
	}
	if strings.Contains(out, tok) || strings.Contains(errb, tok) {
		t.Fatalf("sortie : %q %q", out, errb)
	}
}

func TestConfigCheckMCP(t *testing.T) {
	e := newEnvExtra(t, nil, "eu", `
[[mcp.servers]]
id = "web"
transport = "http"
url = "https://mcp.example.com/mcp"
`)
	code, out, errb := e.run("config", "check")
	if code != 1 || !strings.Contains(errb, "jurisdiction requise") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errb)
	}
	// Sans connexion : un serveur injoignable n'empêche pas la validation.
	e = newEnvExtra(t, nil, "eu", `
[[mcp.servers]]
id = "absent"
transport = "stdio"
command = ["/nonexistent/tenon-mcp-server"]
`)
	if code, out, errb := e.run("config", "check"); code != 0 || strings.TrimSpace(out) != "OK" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errb)
	}
}

// Les avertissements du démarrage (outil au schéma invalide) sont affichés.
func TestMCPWarningNotice(t *testing.T) {
	f := newFakeLLM(t, sseText("salut"))
	e := newEnvExtra(t, f, "eu", stdioDecl(t))
	stdioMCP(t, e, "badschema")
	code, _, errb := e.run("-p", "salut")
	if code != 0 || !strings.Contains(errb, `MCP : serveur MCP test : outil "bad" ignoré`) {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	code, _, errb = e.run("--lang", "en", "-p", "salut")
	if code != 0 || !strings.Contains(errb, `MCP: serveur MCP test : outil "bad" ignoré`) {
		t.Fatalf("en : code=%d stderr=%q", code, errb)
	}
}

// Un échec de startEngine après le démarrage des serveurs MCP les ferme.
func TestMCPClosedWhenStartFails(t *testing.T) {
	f := newFakeLLM(t, sseText("ok"))
	e := newEnvExtra(t, f, "eu", stdioDecl(t))
	log := stdioMCP(t, e, "")
	if code, _, errb := e.run("-p", "bonjour"); code != 0 {
		t.Fatalf("run 1 : code=%d stderr=%q", code, errb)
	}
	ents, err := e.sessionFiles()
	if err != nil || len(ents) != 1 {
		t.Fatalf("sessions %v %v", ents, err)
	}
	id := strings.TrimSuffix(ents[0].Name(), ".jsonl")
	// Un événement illisible par Restore : la reprise échoue après mcp.Start.
	p := filepath.Join(e.sessionsDir(), ents[0].Name())
	fh, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(fh, `{"id":"%s-99","session":%q,"seq":99,"type":"UserMessage","payload":{"text":5}}`+"\n", id, id)
	fh.Close()

	code, _, errb := e.run("--resume", id, "-p", "x")
	if code != 1 {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	_, pids := mcpLog(t, log)
	if len(pids) != 2 {
		t.Fatalf("pids %v", pids)
	}
	waitGone(t, pids[1])
}

// TUI : serveur hors UE sans --allow-non-eu, règle allow → validation
// demandée (ForceAsk) avec la juridiction ; acceptée, l'appel a lieu.
func TestTUIMCPNonEUAsks(t *testing.T) {
	ts, calls := httpMCP(t)
	f := newFakeLLM(t, sseCall("mcp__web__echo", `{"text":"x"}`), sseText("terminé"))
	e := newEnvExtra(t, f, "eu", httpDecl(ts.URL, "non-eu", "")+allowRule("mcp__web__echo"))
	r := e.runTUI(func(tm *teatest.TestModel) {
		typeText(tm, "appelle echo")
		pressEnter(tm)
		// Même image que « Autoriser cette action ? » (WaitFor consomme la sortie).
		waitText(t, tm, "les arguments seront envoyés hors de l'UE")
		typeText(tm, "o")
		waitText(t, tm, "terminé")
		quitTUI(t, tm)
	})
	if r.code != 0 || calls.Load() != 1 {
		t.Fatalf("code=%d appels=%d stderr=%q", r.code, calls.Load(), r.stderr)
	}
}

// TUI : les avertissements du démarrage deviennent des avis de la conversation.
func TestTUIMCPWarnings(t *testing.T) {
	f := newFakeLLM(t, sseText("salut"))
	e := newEnvExtra(t, f, "eu", stdioDecl(t))
	stdioMCP(t, e, "badschema")
	r := e.runTUI(func(tm *teatest.TestModel) {
		waitText(t, tm, "ignoré")
		quitTUI(t, tm)
	})
	if r.code != 0 || len(r.opts.Notices) != 1 || !strings.HasPrefix(r.opts.Notices[0], `MCP : serveur MCP test : outil "bad" ignoré`) {
		t.Fatalf("code=%d notices=%q stderr=%q", r.code, r.opts.Notices, r.stderr)
	}
}

// Sortie immédiate (second Ctrl+C) : killLiveMCP tue les serveurs stdio de
// la session en cours.
func TestHardExitKillsMCP(t *testing.T) {
	f := newFakeLLM(t, sseText("salut"))
	e := newEnvExtra(t, f, "eu", stdioDecl(t))
	log := stdioMCP(t, e, "")
	r := e.runTUI(func(tm *teatest.TestModel) {
		_, pids := mcpLog(t, log)
		if len(pids) != 1 {
			t.Errorf("pids %v", pids)
		} else {
			killLiveMCP()
			waitGone(t, pids[0])
		}
		quitTUI(t, tm)
	})
	if r.code != 0 {
		t.Fatalf("code=%d stderr=%q", r.code, r.stderr)
	}
	if liveMCP.Load() != nil {
		t.Fatal("Manager toujours enregistré après la fermeture")
	}
}
