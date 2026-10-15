package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"codeberg.org/tenon/tenon/internal/config"
	"codeberg.org/tenon/tenon/internal/llm"
	"codeberg.org/tenon/tenon/internal/tools"
)

func start(t *testing.T, servers []config.MCPServer, opts Options) (*Manager, []Down) {
	t.Helper()
	if opts.ConnectTimeout == 0 {
		opts.ConnectTimeout = 10 * time.Second
	}
	if opts.CallTimeout == 0 {
		opts.CallTimeout = 10 * time.Second
	}
	m, down := Start(context.Background(), servers, opts)
	t.Cleanup(func() { _ = m.Close() })
	return m, down
}

func toolByName(t *testing.T, m *Manager, name string) tools.Tool {
	t.Helper()
	for _, tl := range m.Tools() {
		if tl.Name() == name {
			return tl
		}
	}
	names := []string{}
	for _, tl := range m.Tools() {
		names = append(names, tl.Name())
	}
	t.Fatalf("outil %q absent ; présents : %v", name, names)
	return nil
}

func run(t *testing.T, tl tools.Tool, args string) tools.Result {
	t.Helper()
	res, err := tl.Run(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("Run : %v", err)
	}
	return res
}

func TestToolNames(t *testing.T) {
	taken := map[string]bool{}
	if got := ToolName("github", "create issue!", taken); got != "mcp__github__create_issue_" {
		t.Fatalf("got %q", got)
	}
	if !taken["mcp__github__create_issue_"] {
		t.Fatal("nom non enregistré dans taken")
	}
	long := ToolName("srv", strings.Repeat("x", 100), taken)
	if len(long) != 64 || !strings.HasPrefix(long, "mcp__srv__xxx") {
		t.Fatalf("troncature : %q (%d)", long, len(long))
	}
	// Collision : "create issue?" donne le même nom nettoyé.
	coll := ToolName("github", "create issue?", taken)
	if coll == "mcp__github__create_issue_" || !strings.HasPrefix(coll, "mcp__github__create_issue__") || len(coll) != len("mcp__github__create_issue_")+5 {
		t.Fatalf("collision : %q", coll)
	}
	// Collision après troncature : reste à 64.
	coll2 := ToolName("srv", strings.Repeat("x", 101), taken)
	if len(coll2) != 64 || coll2 == long {
		t.Fatalf("collision tronquée : %q", coll2)
	}
	for _, n := range []string{long, coll, coll2, ToolName("é", "日本", nil)} {
		for _, r := range n {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
				t.Fatalf("caractère interdit dans %q", n)
			}
		}
	}
}

func echoServer(s *sdk.Server) {
	s.AddTool(&sdk.Tool{Name: "echo", Description: "renvoie text", InputSchema: map[string]any{
		"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}},
	}}, func(_ context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		var a struct{ Text string }
		_ = json.Unmarshal(req.Params.Arguments, &a)
		return textResult("écho : " + a.Text), nil
	})
}

func TestListAndCall(t *testing.T) {
	m, down := start(t, []config.MCPServer{httpDecl("mem")}, Options{dial: memServer(t, echoServer)})
	if len(down) != 0 {
		t.Fatalf("down : %v", down)
	}
	tl := toolByName(t, m, "mcp__mem__echo")
	if tl.Description() != "renvoie text" {
		t.Fatalf("description %q", tl.Description())
	}
	if tl.Risk() != tools.Network {
		t.Fatalf("risque %v", tl.Risk())
	}
	var sch map[string]any
	if err := json.Unmarshal(tl.Schema(), &sch); err != nil || sch["type"] != "object" {
		t.Fatalf("schéma %s", tl.Schema())
	}
	tg, err := tl.Target(json.RawMessage(`{ "text" : "salut" }`))
	if err != nil || tg.Command != `mcp mem.echo {"text":"salut"}` {
		t.Fatalf("target %q %v", tg.Command, err)
	}
	res := run(t, tl, `{"text":"salut"}`)
	if res.IsError || res.Content != "écho : salut" || res.ServerDown != "" {
		t.Fatalf("résultat %+v", res)
	}
	srv, jur, ok := Describe("mcp__mem__echo")
	if !ok || srv != "mem" || jur != llm.EU {
		t.Fatalf("Describe : %q %q %v", srv, jur, ok)
	}
	if _, _, ok := Describe("mcp__mem__absent"); ok {
		t.Fatal("Describe d'un outil inconnu")
	}
}

// Target contient tous les arguments : l'utilisateur voit tout ce qu'il
// valide et la clé « se souvenir » de l'engine est exacte.
func TestRiskOverrideAndTargetFull(t *testing.T) {
	decl := httpDecl("mem")
	decl.Risk = "read"
	m, _ := start(t, []config.MCPServer{decl}, Options{dial: memServer(t, echoServer)})
	tl := toolByName(t, m, "mcp__mem__echo")
	if tl.Risk() != tools.Read {
		t.Fatalf("risque %v", tl.Risk())
	}
	tg, err := tl.Target(json.RawMessage(`{"text":"` + strings.Repeat("a", 5000) + `"}`))
	want := `mcp mem.echo {"text":"` + strings.Repeat("a", 5000) + `"}`
	if err != nil || tg.Command != want {
		t.Fatalf("target %d %v", len(tg.Command), err)
	}
	if _, err := tl.Target(json.RawMessage(`{`)); err == nil {
		t.Fatal("arguments invalides acceptés")
	}
}

func TestAllowlistFilter(t *testing.T) {
	setup := func(s *sdk.Server) {
		for _, n := range []string{"a", "b"} {
			s.AddTool(&sdk.Tool{Name: n, InputSchema: objectSchema},
				func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) { return textResult(""), nil })
		}
	}
	decl := httpDecl("mem")
	decl.Tools = []string{"a"}
	m, _ := start(t, []config.MCPServer{decl}, Options{dial: memServer(t, setup)})
	got := m.Tools()
	if len(got) != 1 || got[0].Name() != "mcp__mem__a" {
		t.Fatalf("outils %v", got)
	}
}

func TestInvalidSchemaSkipped(t *testing.T) {
	setup := func(s *sdk.Server) {
		h := func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) { return textResult(""), nil }
		s.AddTool(&sdk.Tool{Name: "ok", InputSchema: objectSchema}, h)
		s.AddTool(&sdk.Tool{Name: "badprops", InputSchema: map[string]any{"type": "object", "properties": "x"}}, h)
		s.AddTool(&sdk.Tool{Name: "huge", InputSchema: map[string]any{"type": "object", "description": strings.Repeat("z", 70<<10)}}, h)
	}
	m, _ := start(t, []config.MCPServer{httpDecl("mem")}, Options{dial: memServer(t, setup)})
	got := m.Tools()
	if len(got) != 1 || got[0].Name() != "mcp__mem__ok" {
		names := []string{}
		for _, g := range got {
			names = append(names, g.Name())
		}
		t.Fatalf("outils %v", names)
	}
	if len(m.Warnings()) != 2 {
		t.Fatalf("avertissements %v", m.Warnings())
	}
}

func TestServerIsErrorResult(t *testing.T) {
	setup := func(s *sdk.Server) {
		s.AddTool(&sdk.Tool{Name: "fail", InputSchema: objectSchema},
			func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
				r := textResult("raté")
				r.IsError = true
				return r, nil
			})
	}
	m, _ := start(t, []config.MCPServer{httpDecl("mem")}, Options{dial: memServer(t, setup)})
	res := run(t, toolByName(t, m, "mcp__mem__fail"), `{}`)
	if !res.IsError || res.Content != "raté" || res.ServerDown != "" {
		t.Fatalf("résultat %+v", res)
	}
	// Outil inconnu côté serveur (erreur de protocole) : erreur, serveur toujours en service.
	tl := toolByName(t, m, "mcp__mem__fail").(*mcpTool)
	ghost := *tl
	ghost.tool = "inconnu"
	res = run(t, &ghost, `{}`)
	if !res.IsError || res.ServerDown != "" {
		t.Fatalf("outil inconnu %+v", res)
	}
	if res := run(t, tl, `{}`); res.Content != "raté" {
		t.Fatalf("serveur marqué hors service à tort : %+v", res)
	}
}

func TestNonTextContentSummarised(t *testing.T) {
	setup := func(s *sdk.Server) {
		s.AddTool(&sdk.Tool{Name: "mix", InputSchema: objectSchema},
			func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
				return &sdk.CallToolResult{Content: []sdk.Content{
					&sdk.TextContent{Text: "début"},
					&sdk.ImageContent{MIMEType: "image/png", Data: []byte{1, 2}},
					&sdk.ResourceLink{URI: "file:///a.txt", Name: "a"},
					&sdk.EmbeddedResource{Resource: &sdk.ResourceContents{URI: "file:///b.txt", Text: "b"}},
					&sdk.TextContent{Text: "fin"},
				}}, nil
			})
	}
	m, _ := start(t, []config.MCPServer{httpDecl("mem")}, Options{dial: memServer(t, setup), OutputLimit: 1000})
	res := run(t, toolByName(t, m, "mcp__mem__mix"), `{}`)
	want := "début\n[image image/png]\n[ressource file:///a.txt]\n[ressource file:///b.txt]\nfin"
	if res.Content != want {
		t.Fatalf("contenu %q", res.Content)
	}
	// Troncature de la sortie.
	m2, _ := start(t, []config.MCPServer{httpDecl("mem")}, Options{dial: memServer(t, setup), OutputLimit: 3})
	res = run(t, toolByName(t, m2, "mcp__mem__mix"), `{}`)
	if !strings.HasPrefix(res.Content, "déb\n[… sortie tronquée") {
		t.Fatalf("troncature %q", res.Content)
	}
}

func TestConsentDenied(t *testing.T) {
	var asked string
	opts := Options{dial: memServer(t, echoServer), Consent: func(id string) bool { asked = id; return false }}
	m, _ := start(t, []config.MCPServer{httpDecl("mem")}, opts)
	res := run(t, toolByName(t, m, "mcp__mem__echo"), `{"text":"x"}`)
	if !res.IsError || res.Denied != "policy" || asked != "mem" || res.Content != "serveur MCP hors UE sans consentement" {
		t.Fatalf("résultat %+v (demandé %q)", res, asked)
	}
}

func TestStdioMinimalEnv(t *testing.T) {
	t.Setenv("SECRET_PARENT", "fuite-parent")
	srv, getenv := stdioServer(t, "srv", "", map[string]string{"MY_TOKEN": "jeton-42", "SECRET_PARENT": "fuite-getenv", "LANG": "fr_FR.UTF-8"})
	srv.EnvFrom = append(srv.EnvFrom, "MY_TOKEN")
	root := t.TempDir()
	m, down := start(t, []config.MCPServer{srv}, Options{Getenv: getenv, Root: root})
	if len(down) != 0 {
		t.Fatalf("down %v", down)
	}
	tl := toolByName(t, m, "mcp__srv__env")
	if tl.Risk() != tools.Exec {
		t.Fatalf("risque %v", tl.Risk())
	}
	out := run(t, tl, `{}`).Content
	if !strings.Contains(out, "MY_TOKEN=jeton-42") || !strings.Contains(out, "LANG=fr_FR.UTF-8") || !strings.Contains(out, "\nPATH=") {
		t.Fatalf("env attendu absent :\n%s", out)
	}
	if strings.Contains(out, "SECRET_PARENT") || strings.Contains(out, "fuite") {
		t.Fatalf("variable non listée transmise :\n%s", out)
	}
	if !strings.Contains(out, "PWD=") || !strings.Contains(out, "PWD="+mustEval(t, root)+"\n") {
		t.Fatalf("répertoire de travail :\n%s", out)
	}
}

func TestConnectTimeout(t *testing.T) {
	srv, getenv := stdioServer(t, "lent", "hang", nil)
	t0 := time.Now()
	m, down := start(t, []config.MCPServer{srv}, Options{Getenv: getenv, ConnectTimeout: 300 * time.Millisecond})
	if el := time.Since(t0); el > 3*time.Second {
		t.Fatalf("Start trop long : %v", el)
	}
	if len(down) != 1 || down[0].Server != "lent" || down[0].Error == "" {
		t.Fatalf("down %v", down)
	}
	if len(m.Tools()) != 0 {
		t.Fatal("outils d'un serveur hors service exposés")
	}
}

func TestCallTimeout(t *testing.T) {
	srv, getenv := stdioServer(t, "srv", "", nil)
	m, _ := start(t, []config.MCPServer{srv}, Options{Getenv: getenv, CallTimeout: 300 * time.Millisecond})
	t0 := time.Now()
	res := run(t, toolByName(t, m, "mcp__srv__sleep"), `{}`)
	if el := time.Since(t0); el > 3*time.Second {
		t.Fatalf("appel trop long : %v", el)
	}
	if !res.IsError || !strings.Contains(res.Content, "délai") || res.ServerDown != "" {
		t.Fatalf("résultat %+v", res)
	}
	// Le serveur reste utilisable.
	if res := run(t, toolByName(t, m, "mcp__srv__echo"), `{"text":"encore"}`); res.IsError || res.Content != "encore" {
		t.Fatalf("après délai %+v", res)
	}
}

func TestServerCrashMidCall(t *testing.T) {
	srv, getenv := stdioServer(t, "srv", "", nil)
	m, _ := start(t, []config.MCPServer{srv}, Options{Getenv: getenv, CallTimeout: 5 * time.Second})
	res := run(t, toolByName(t, m, "mcp__srv__crash"), `{}`)
	if !res.IsError || res.ServerDown != "srv" {
		t.Fatalf("résultat %+v", res)
	}
	res = run(t, toolByName(t, m, "mcp__srv__echo"), `{"text":"x"}`)
	if !res.IsError || res.Content != "serveur MCP srv indisponible" || res.ServerDown != "" {
		t.Fatalf("appel suivant %+v", res)
	}
}

func TestHostileServerText(t *testing.T) {
	srv, getenv := stdioServer(t, "srv", "hostile", nil)
	m, _ := start(t, []config.MCPServer{srv}, Options{Getenv: getenv})
	got := m.Tools()
	if len(got) != 1 {
		t.Fatalf("outils %v", got)
	}
	tl := got[0]
	if tl.Name() != "mcp__srv__evil__31m_name_" {
		t.Fatalf("nom %q", tl.Name())
	}
	if d := tl.Description(); d != "a[2Jbcde" {
		t.Fatalf("description %q", d)
	}
	tg, _ := tl.Target(json.RawMessage(`{}`))
	if strings.ContainsAny(tg.Command, "\x1b‮") {
		t.Fatalf("target %q", tg.Command)
	}
	if res := run(t, tl, `{}`); res.IsError || res.Content != "ok" {
		t.Fatalf("appel %+v", res)
	}
}

func TestDescriptionTruncated(t *testing.T) {
	setup := func(s *sdk.Server) {
		s.AddTool(&sdk.Tool{Name: "long", Description: strings.Repeat("é", 3000), InputSchema: objectSchema},
			func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) { return textResult(""), nil })
	}
	m, _ := start(t, []config.MCPServer{httpDecl("mem")}, Options{dial: memServer(t, setup)})
	if n := len([]rune(m.Tools()[0].Description())); n != 1024 {
		t.Fatalf("longueur %d", n)
	}
}

func TestSecretsListed(t *testing.T) {
	env := map[string]string{"A_TOK": "secret-a", "B_TOK": "secret-b", "EMPTY": ""}
	servers := []config.MCPServer{
		{ID: "s1", Transport: "stdio", Command: []string{"/nonexistent/bin"}, EnvFrom: []string{"A_TOK", "EMPTY"}, Disabled: true},
		{ID: "s2", Transport: "http", URL: "http://127.0.0.1:1/mcp", BearerEnv: "B_TOK", Disabled: true},
	}
	m, _ := start(t, servers, Options{Getenv: func(k string) string { return env[k] }})
	got := strings.Join(m.Secrets(), ",")
	if !strings.Contains(got, "secret-a") || !strings.Contains(got, "secret-b") || strings.Contains(got, ",,") || len(m.Secrets()) != 2 {
		t.Fatalf("secrets %q", m.Secrets())
	}
}

func TestDisabledAndUnreachable(t *testing.T) {
	servers := []config.MCPServer{
		{ID: "off", Transport: "stdio", Command: []string{"/nonexistent/bin"}, Disabled: true},
		{ID: "absent", Transport: "stdio", Command: []string{"/nonexistent/bin"}},
	}
	m, down := start(t, servers, Options{Getenv: func(string) string { return "" }, ConnectTimeout: time.Second})
	if len(down) != 1 || down[0].Server != "absent" {
		t.Fatalf("down %v", down)
	}
	if len(m.Tools()) != 0 {
		t.Fatal("outils exposés")
	}
}

func TestCloseKillsStdio(t *testing.T) {
	srv, getenv := stdioServer(t, "srv", "", nil)
	m, _ := Start(context.Background(), []config.MCPServer{srv}, Options{Getenv: getenv, ConnectTimeout: 10 * time.Second, CallTimeout: 10 * time.Second})
	res := run(t, toolByName(t, m, "mcp__srv__spawn"), `{}`)
	pid, err := strconv.Atoi(res.Content)
	if err != nil {
		t.Fatalf("pid %q", res.Content)
	}
	t0 := time.Now()
	if err := m.Close(); err != nil {
		t.Logf("Close : %v", err)
	}
	if el := time.Since(t0); el > 4*time.Second {
		t.Fatalf("Close trop long : %v", el)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			break
		}
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("processus enfant %d toujours vivant (%v)", pid, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = m.Close() // idempotent
}

func TestHTTPBearerAndNoRedirect(t *testing.T) {
	s := sdk.NewServer(&sdk.Implementation{Name: "h", Version: "1"}, nil)
	echoServer(s)
	h := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return s }, nil)
	var mu sync.Mutex
	var auths []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redir" {
			http.Redirect(w, r, "/mcp", http.StatusTemporaryRedirect)
			return
		}
		mu.Lock()
		auths = append(auths, r.Header.Get("Authorization"))
		mu.Unlock()
		h.ServeHTTP(w, r)
	}))
	defer ts.Close()
	env := map[string]string{"TOK": "jeton-http"}
	ok := config.MCPServer{ID: "web", Transport: "http", URL: ts.URL + "/mcp", BearerEnv: "TOK", Jurisdiction: "eu"}
	redir := config.MCPServer{ID: "redir", Transport: "http", URL: ts.URL + "/redir", Jurisdiction: "eu"}
	m, down := start(t, []config.MCPServer{ok, redir}, Options{Getenv: func(k string) string { return env[k] }, ConnectTimeout: 3 * time.Second})
	if len(down) != 1 || down[0].Server != "redir" {
		t.Fatalf("down %v", down)
	}
	if res := run(t, toolByName(t, m, "mcp__web__echo"), `{"text":"h"}`); res.IsError || res.Content != "écho : h" {
		t.Fatalf("appel %+v", res)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(auths) == 0 {
		t.Fatal("aucune requête")
	}
	for _, a := range auths {
		if a != "Bearer jeton-http" {
			t.Fatalf("en-tête %q", a)
		}
	}
}

func mustEval(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// httpEcho démarre un serveur HTTP streamable de test ; fail(true) le fait répondre 503.
func httpEcho(t *testing.T, body func(*sdk.Server)) (*httptest.Server, *atomic.Bool) {
	t.Helper()
	s := sdk.NewServer(&sdk.Implementation{Name: "h", Version: "1"}, nil)
	body(s)
	h := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return s }, nil)
	var unavailable atomic.Bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if unavailable.Load() {
			http.Error(w, "indisponible", http.StatusServiceUnavailable)
			return
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	return ts, &unavailable
}

func TestHTTPServerGoneMarksDown(t *testing.T) {
	ts, _ := httpEcho(t, echoServer)
	decl := config.MCPServer{ID: "web", Transport: "http", URL: ts.URL + "/mcp", Jurisdiction: "eu"}
	m, _ := start(t, []config.MCPServer{decl}, Options{ConnectTimeout: 3 * time.Second, CallTimeout: 3 * time.Second})
	tl := toolByName(t, m, "mcp__web__echo")
	if res := run(t, tl, `{"text":"a"}`); res.IsError {
		t.Fatalf("premier appel %+v", res)
	}
	ts.CloseClientConnections()
	ts.Close()
	res := run(t, tl, `{"text":"b"}`)
	if !res.IsError || res.ServerDown != "web" {
		t.Fatalf("serveur fermé %+v", res)
	}
	res = run(t, tl, `{"text":"c"}`)
	if !res.IsError || res.Content != "serveur MCP web indisponible" || res.ServerDown != "" {
		t.Fatalf("appel suivant %+v", res)
	}
}

func TestHTTP503NotDown(t *testing.T) {
	ts, unavailable := httpEcho(t, echoServer)
	decl := config.MCPServer{ID: "web", Transport: "http", URL: ts.URL + "/mcp", Jurisdiction: "eu"}
	m, _ := start(t, []config.MCPServer{decl}, Options{ConnectTimeout: 3 * time.Second, CallTimeout: 3 * time.Second})
	tl := toolByName(t, m, "mcp__web__echo")
	unavailable.Store(true)
	res := run(t, tl, `{"text":"a"}`)
	if !res.IsError || res.ServerDown != "" || strings.Contains(res.Content, "indisponible :") {
		t.Fatalf("503 %+v", res)
	}
	if !strings.Contains(res.Content, "Service Unavailable") {
		t.Fatalf("contenu opaque %q", res.Content)
	}
	unavailable.Store(false)
	if res := run(t, tl, `{"text":"b"}`); res.IsError || res.Content != "écho : b" {
		t.Fatalf("après 503 %+v", res)
	}
}

func TestHTTPBodyCapped(t *testing.T) {
	old := maxBody
	maxBody = 4096
	defer func() { maxBody = old }()
	ts, _ := httpEcho(t, echoServer)
	decl := config.MCPServer{ID: "web", Transport: "http", URL: ts.URL + "/mcp", Jurisdiction: "eu"}
	m, down := start(t, []config.MCPServer{decl}, Options{ConnectTimeout: 3 * time.Second, CallTimeout: 3 * time.Second})
	if len(down) != 0 {
		t.Fatalf("down %v", down)
	}
	tl := toolByName(t, m, "mcp__web__echo")
	big := strings.Repeat("z", 10000)
	res := run(t, tl, `{"text":"`+big+`"}`)
	if !res.IsError || strings.Contains(res.Content, big) {
		t.Fatalf("corps non borné : IsError=%v len=%d", res.IsError, len(res.Content))
	}
}

func TestLimitedBodyBoundary(t *testing.T) {
	read := func(n int) error {
		l := &limitedBody{rc: io.NopCloser(strings.NewReader(strings.Repeat("x", n))), left: 10}
		_, err := io.ReadAll(l)
		return err
	}
	if err := read(10); err != nil {
		t.Fatalf("10 octets : %v", err)
	}
	if err := read(11); !errors.Is(err, errBodyTooLarge) {
		t.Fatalf("11 octets : %v", err)
	}
}

func TestDeepSchemaSkippedFast(t *testing.T) {
	// {"not":{"not":…}} : 6500 niveaux en moins de 64 Ko.
	deep := any(map[string]any{})
	for range 6500 {
		deep = map[string]any{"not": deep}
	}
	if b, _ := json.Marshal(deep); len(b) > maxSchema {
		t.Fatalf("schéma de test trop gros : %d", len(b))
	}
	t0 := time.Now()
	_, err := validSchema(map[string]any{"type": "object", "not": deep})
	if el := time.Since(t0); err == nil || el > 2*time.Second {
		t.Fatalf("schéma profond : err=%v en %v", err, el)
	}
	// Profondeur raisonnable acceptée.
	ok := any(map[string]any{"type": "string"})
	for range 5 {
		ok = map[string]any{"type": "object", "properties": map[string]any{"a": ok}}
	}
	if _, err := validSchema(ok); err != nil {
		t.Fatalf("schéma de profondeur 11 refusé : %v", err)
	}
}

func TestToolCapPerServer(t *testing.T) {
	setup := func(s *sdk.Server) {
		for i := range 130 {
			s.AddTool(&sdk.Tool{Name: "t" + strconv.Itoa(i), InputSchema: objectSchema},
				func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) { return textResult(""), nil })
		}
	}
	m, _ := start(t, []config.MCPServer{httpDecl("mem")}, Options{dial: memServer(t, setup)})
	if n := len(m.Tools()); n != 128 {
		t.Fatalf("%d outils exposés", n)
	}
	if w := m.Warnings(); len(w) != 1 || !strings.Contains(w[0], "128") {
		t.Fatalf("avertissements %v", w)
	}
}

func TestCloseNoServerDown(t *testing.T) {
	srv, getenv := stdioServer(t, "srv", "", nil)
	m, _ := Start(context.Background(), []config.MCPServer{srv}, Options{Getenv: getenv, ConnectTimeout: 10 * time.Second, CallTimeout: 10 * time.Second})
	tl := toolByName(t, m, "mcp__srv__sleep")
	done := make(chan tools.Result, 1)
	go func() {
		res, _ := tl.Run(context.Background(), json.RawMessage(`{}`))
		done <- res
	}()
	time.Sleep(200 * time.Millisecond) // l'appel est en cours
	_ = m.Close()
	select {
	case res := <-done:
		if !res.IsError || res.ServerDown != "" {
			t.Fatalf("appel interrompu par Close %+v", res)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("appel toujours bloqué après Close")
	}
}

func TestSecretsExcludeBaseEnv(t *testing.T) {
	env := map[string]string{"PATH": "/usr/bin", "HOME": "/home/x", "LANG": "C", "TOK": "s"}
	servers := []config.MCPServer{{ID: "s", Transport: "stdio", Command: []string{"x"}, EnvFrom: []string{"PATH", "HOME", "LANG", "TOK"}, Disabled: true}}
	m, _ := start(t, servers, Options{Getenv: func(k string) string { return env[k] }})
	if got := m.Secrets(); len(got) != 1 || got[0] != "s" {
		t.Fatalf("secrets %q", got)
	}
}

// Kill (sortie immédiate du processus) tue sur-le-champ le groupe de chaque
// serveur stdio, sans attendre.
func TestKillStopsStdio(t *testing.T) {
	srv, getenv := stdioServer(t, "srv", "", nil)
	m, _ := start(t, []config.MCPServer{srv}, Options{Getenv: getenv})
	res := run(t, toolByName(t, m, "mcp__srv__spawn"), `{}`)
	pid, err := strconv.Atoi(res.Content)
	if err != nil {
		t.Fatalf("pid %q", res.Content)
	}
	t0 := time.Now()
	m.Kill()
	if el := time.Since(t0); el > 500*time.Millisecond {
		t.Fatalf("Kill trop long : %v", el)
	}
	deadline := time.Now().Add(3 * time.Second)
	for !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("processus enfant %d toujours vivant", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Après Kill, un appel n'est pas signalé comme panne du serveur.
	if res := run(t, toolByName(t, m, "mcp__srv__echo"), `{"text":"x"}`); res.ServerDown != "" {
		t.Fatalf("ServerDown après Kill : %+v", res)
	}
}
