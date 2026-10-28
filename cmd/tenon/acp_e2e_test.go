package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/mcp/mcptest"
	"github.com/tenon-code-eu/tenon/internal/sandbox"
	"github.com/tenon-code-eu/tenon/internal/session"
)

// acpMsg est un message JSON-RPC lu sur la sortie de tenon acp.
type acpMsg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// acpClient est un éditeur factice : il pilote « tenon acp » (run) par des
// tubes, consigne chaque ligne de sortie et répond aux requêtes de l'agent
// par onRequest (nil : aucune réponse).
type acpClient struct {
	t    *testing.T
	in   *io.PipeWriter
	done chan int

	onRequest func(c *acpClient, m acpMsg) any

	mu      sync.Mutex
	raw     []string
	updates []map[string]any
	resps   map[string]acpMsg
	wake    chan struct{}
	next    int
	closeIn sync.Once
}

// startACP lance « tenon acp » dans l'environnement e.
func startACP(t *testing.T, e *env, onRequest func(c *acpClient, m acpMsg) any) *acpClient {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	c := &acpClient{t: t, in: inW, done: make(chan int, 1), onRequest: onRequest, resps: map[string]acpMsg{}, wake: make(chan struct{}, 1)}
	getenv := func(k string) string { return e.vars[k] }
	wd := e.wd
	go func() {
		var errb strings.Builder
		code := run(context.Background(), []string{"acp"}, inR, outW, &lockedWriter{w: &errb}, getenv, wd, func() bool { return false })
		_ = outW.Close()
		c.done <- code
	}()
	go c.read(outR)
	t.Cleanup(func() {
		c.closeStdin()
		select {
		case <-c.done:
		case <-time.After(30 * time.Second):
			t.Error("tenon acp ne s'est pas arrêté")
		}
	})
	return c
}

// lockedWriter sérialise les écritures sur stderr (plusieurs goroutines).
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func (c *acpClient) read(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 16<<20)
	for sc.Scan() {
		line := sc.Text()
		var m acpMsg
		err := json.Unmarshal([]byte(line), &m)
		c.mu.Lock()
		c.raw = append(c.raw, line)
		c.mu.Unlock()
		if err != nil {
			continue
		}
		switch {
		case m.Method != "" && len(m.ID) > 0:
			var res any
			if c.onRequest != nil {
				res = c.onRequest(c, m)
			}
			if res != nil {
				c.send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": res})
			}
		case m.Method == "session/update":
			var p struct {
				Update map[string]any `json:"update"`
			}
			_ = json.Unmarshal(m.Params, &p)
			c.mu.Lock()
			c.updates = append(c.updates, p.Update)
			c.mu.Unlock()
		case len(m.ID) > 0:
			c.mu.Lock()
			c.resps[string(m.ID)] = m
			c.mu.Unlock()
			select {
			case c.wake <- struct{}{}:
			default:
			}
		}
	}
	_, _ = io.Copy(io.Discard, r)
}

func (c *acpClient) send(v any) {
	b, _ := json.Marshal(v)
	_, _ = c.in.Write(append(b, '\n'))
}

func (c *acpClient) closeStdin() { c.closeIn.Do(func() { _ = c.in.Close() }) }

// request envoie une requête et attend sa réponse.
func (c *acpClient) request(method string, params any) acpMsg {
	c.t.Helper()
	c.mu.Lock()
	c.next++
	n := c.next
	c.mu.Unlock()
	id := fmt.Sprint(n)
	go c.send(map[string]any{"jsonrpc": "2.0", "id": n, "method": method, "params": params})
	deadline := time.After(30 * time.Second)
	for {
		c.mu.Lock()
		m, ok := c.resps[id]
		c.mu.Unlock()
		if ok {
			return m
		}
		select {
		case <-c.wake:
		case <-time.After(10 * time.Millisecond):
		case <-deadline:
			c.t.Fatalf("%s : pas de réponse", method)
		}
	}
}

// call est request, en échec si la réponse est une erreur.
func (c *acpClient) call(method string, params any) map[string]any {
	c.t.Helper()
	m := c.request(method, params)
	if m.Error != nil {
		c.t.Fatalf("%s : erreur %d %q", method, m.Error.Code, m.Error.Message)
	}
	// Tout résultat de méthode ACP est un objet : null ou autre chose échoue.
	var out map[string]any
	if err := json.Unmarshal(m.Result, &out); err != nil || out == nil {
		c.t.Fatalf("%s : résultat %s n'est pas un objet (%v)", method, m.Result, err)
	}
	return out
}

func (c *acpClient) initialize() {
	c.t.Helper()
	c.call("initialize", map[string]any{"protocolVersion": 1, "clientInfo": map[string]any{"name": "zed", "version": "0.201.0"}})
}

func (c *acpClient) newSession(cwd string, mcp []map[string]any) string {
	c.t.Helper()
	if mcp == nil {
		mcp = []map[string]any{}
	}
	r := c.call("session/new", map[string]any{"cwd": cwd, "mcpServers": mcp})
	sid, _ := r["sessionId"].(string)
	if sid == "" {
		c.t.Fatalf("session/new sans sessionId : %v", r)
	}
	return sid
}

func (c *acpClient) prompt(sid, text string) string {
	c.t.Helper()
	r := c.call("session/prompt", map[string]any{"sessionId": sid, "prompt": []map[string]any{{"type": "text", "text": text}}})
	s, _ := r["stopReason"].(string)
	return s
}

// wait ferme stdin et renvoie le code de sortie.
func (c *acpClient) wait() int {
	c.t.Helper()
	c.closeStdin()
	select {
	case code := <-c.done:
		c.done <- code
		return code
	case <-time.After(30 * time.Second):
		c.t.Fatal("tenon acp ne s'est pas arrêté")
	}
	return -1
}

func (c *acpClient) allUpdates() []map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]map[string]any(nil), c.updates...)
}

func (c *acpClient) lines() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.raw...)
}

// updateTexts renvoie les textes des mises à jour de type kind.
func updateTexts(us []map[string]any, kind string) []string {
	var out []string
	for _, u := range us {
		if u["sessionUpdate"] != kind {
			continue
		}
		if c, ok := u["content"].(map[string]any); ok {
			s, _ := c["text"].(string)
			out = append(out, s)
		}
	}
	return out
}

// allowPerm répond « allow » à chaque demande de permission.
func allowPerm(_ *acpClient, m acpMsg) any {
	if m.Method != "session/request_permission" {
		return nil
	}
	return map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": "allow"}}
}

// checkpointHas indique qu'une copie d'avant écriture contient content.
func checkpointHas(e *env, content string) bool {
	found := false
	_ = filepath.WalkDir(filepath.Join(e.dataDir, "checkpoints"), func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if b, _ := os.ReadFile(p); string(b) == content {
				found = true
			}
		}
		return nil
	})
	return found
}

func TestACPInitializeAndText(t *testing.T) {
	f := newFakeLLM(t, sseText("bonjour de tenon"))
	e := newEnv(t, f, "eu")
	c := startACP(t, e, nil)
	c.initialize()
	sid := c.newSession(e.wd, nil)
	if r := c.prompt(sid, "salut"); r != "end_turn" {
		t.Fatalf("stopReason=%q", r)
	}
	if got := strings.Join(updateTexts(c.allUpdates(), "agent_message_chunk"), ""); got != "bonjour de tenon" {
		t.Fatalf("agent_message_chunk=%q", got)
	}
	if code := c.wait(); code != 0 {
		t.Fatalf("code=%d", code)
	}
	ss := sessionStarted(t, e.auditContents())
	if len(ss) != 1 || ss[0].Client == nil || ss[0].Client.Name != "zed" || ss[0].Client.Version != "0.201.0" || ss[0].Transport != "acp" {
		t.Fatalf("SessionStarted=%+v", ss)
	}
}

func TestACPWritePermissionDiff(t *testing.T) {
	f := newFakeLLM(t, sseCall("write_file", `{"path":"x.txt","content":"nouveau"}`), sseText("écrit"))
	e := newEnv(t, f, "eu")
	e.writeFile(filepath.Join(e.wd, "x.txt"), "ancien")
	var asked int
	var mu sync.Mutex
	c := startACP(t, e, func(c *acpClient, m acpMsg) any {
		mu.Lock()
		asked++
		mu.Unlock()
		return allowPerm(c, m)
	})
	c.initialize()
	sid := c.newSession(e.wd, nil)
	if r := c.prompt(sid, "écris"); r != "end_turn" {
		t.Fatalf("stopReason=%q", r)
	}
	mu.Lock()
	n := asked
	mu.Unlock()
	if n != 1 {
		t.Fatalf("%d demande(s) de permission", n)
	}
	var diff map[string]any
	for _, u := range c.allUpdates() {
		if u["sessionUpdate"] != "tool_call_update" {
			continue
		}
		if cs, ok := u["content"].([]any); ok && len(cs) == 1 {
			if d, _ := cs[0].(map[string]any); d["type"] == "diff" {
				diff = d
			}
		}
	}
	if diff == nil || diff["oldText"] != "ancien" || diff["newText"] != "nouveau" || diff["path"] != filepath.Join(e.wd, "x.txt") {
		t.Fatalf("diff=%v updates=%v", diff, c.allUpdates())
	}
	if b, _ := os.ReadFile(filepath.Join(e.wd, "x.txt")); string(b) != "nouveau" {
		t.Fatalf("fichier=%q", b)
	}
	if !checkpointHas(e, "ancien") {
		t.Fatal("copie d'avant écriture absente")
	}
}

func TestACPCancel(t *testing.T) {
	f := newFakeLLM(t, sseCall("write_file", `{"path":"x.txt","content":"nouveau"}`), sseText("écrit"))
	e := newEnv(t, f, "eu")
	e.writeFile(filepath.Join(e.wd, "x.txt"), "ancien")
	var sid string
	c := startACP(t, e, func(c *acpClient, m acpMsg) any {
		if m.Method == "session/request_permission" {
			go c.send(map[string]any{"jsonrpc": "2.0", "method": "session/cancel", "params": map[string]any{"sessionId": sid}})
		}
		return nil
	})
	c.initialize()
	sid = c.newSession(e.wd, nil)
	if r := c.prompt(sid, "écris"); r != "cancelled" {
		t.Fatalf("stopReason=%q", r)
	}
	if b, _ := os.ReadFile(filepath.Join(e.wd, "x.txt")); string(b) != "ancien" {
		t.Fatalf("fichier=%q", b)
	}
}

func TestACPLoad(t *testing.T) {
	f := newFakeLLM(t, sseText("réponse un"), sseText("réponse deux"))
	e := newEnv(t, f, "eu")
	c := startACP(t, e, nil)
	c.initialize()
	sid := c.newSession(e.wd, nil)
	if r := c.prompt(sid, "premier message"); r != "end_turn" {
		t.Fatalf("stopReason=%q", r)
	}
	if code := c.wait(); code != 0 {
		t.Fatalf("code=%d", code)
	}

	c2 := startACP(t, e, nil)
	c2.initialize()
	// Session inconnue, puis session d'un autre dossier : -32002.
	other := t.TempDir()
	for _, p := range []map[string]any{
		{"sessionId": "s_000000000000", "cwd": e.wd, "mcpServers": []any{}},
		{"sessionId": sid, "cwd": other, "mcpServers": []any{}},
	} {
		if m := c2.request("session/load", p); m.Error == nil || m.Error.Code != -32002 {
			t.Fatalf("load %v : %+v", p, m.Error)
		}
	}
	c2.call("session/load", map[string]any{"sessionId": sid, "cwd": e.wd, "mcpServers": []any{}})
	if r := c2.prompt(sid, "suite"); r != "end_turn" {
		t.Fatalf("stopReason=%q", r)
	}
	us := c2.allUpdates()
	if got := updateTexts(us, "user_message_chunk"); len(got) != 1 || got[0] != "premier message" {
		t.Fatalf("user_message_chunk=%q", got)
	}
	if got := updateTexts(us, "agent_message_chunk"); len(got) < 2 || got[0] != "réponse un" || strings.Join(got[1:], "") != "réponse deux" {
		t.Fatalf("agent_message_chunk=%q", got)
	}
	reqs := f.requests()
	if len(reqs) != 2 || !strings.Contains(reqs[1], "premier message") || !strings.Contains(reqs[1], "réponse un") {
		t.Fatalf("la conversation ne continue pas : %d requête(s)", len(reqs))
	}
}

// Recharger dans la même connexion une session ouverte et inactive : elle est
// fermée d'abord (verrou relâché), puis reprise et rejouée.
func TestACPLoadSameConnection(t *testing.T) {
	f := newFakeLLM(t, sseText("réponse un"), sseText("réponse deux"))
	e := newEnv(t, f, "eu")
	c := startACP(t, e, nil)
	c.initialize()
	sid := c.newSession(e.wd, nil)
	if r := c.prompt(sid, "premier message"); r != "end_turn" {
		t.Fatalf("stopReason=%q", r)
	}
	c.call("session/load", map[string]any{"sessionId": sid, "cwd": e.wd, "mcpServers": []any{}})
	if r := c.prompt(sid, "suite"); r != "end_turn" {
		t.Fatalf("stopReason=%q", r)
	}
	if got := updateTexts(c.allUpdates(), "user_message_chunk"); len(got) != 1 || got[0] != "premier message" {
		t.Fatalf("user_message_chunk=%q", got)
	}
	reqs := f.requests()
	if len(reqs) != 2 || !strings.Contains(reqs[1], "premier message") || !strings.Contains(reqs[1], "réponse un") {
		t.Fatalf("la conversation ne continue pas : %d requête(s)", len(reqs))
	}
	if code := c.wait(); code != 0 {
		t.Fatalf("code=%d", code)
	}
}

// editorServer est le serveur MCP de test fourni par l'éditeur : l'éditeur ne
// peut transmettre de variables d'environnement (ignorées), d'où env. Ses
// démarrages sont consignés dans log.
func editorServer(t *testing.T, log string) []map[string]any {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return []map[string]any{{"name": "ed", "command": "/usr/bin/env", "args": []string{mcptest.EnvServer + "=1", mcptest.EnvLog + "=" + log, exe}, "env": []any{}}}
}

func TestACPEditorMCP(t *testing.T) {
	const accept = "\n[acp]\naccept_editor_mcp = true\n"
	for _, tc := range []struct {
		name, extra, policy, notice string
		started                     bool
	}{
		{"ignorés", "", "", "serveurs MCP de l'éditeur ignorés ([acp] accept_editor_mcp)", false},
		{"acceptés", accept, "", "", true},
		// Accepté, le serveur suit le chemin de la configuration : la
		// politique l'exclut et il n'est pas lancé.
		{"exclus par la politique", accept, "[mcp]\nmode = \"off\"\n", "politique acme-dev : serveur MCP « ed » ignoré (non autorisé)", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeLLM(t, sseText("ok"))
			e := newEnvExtra(t, f, "eu", tc.extra)
			e.vars["PATH"] = os.Getenv("PATH")
			if tc.policy != "" {
				e.withPolicy(policyHead + tc.policy)
			}
			log := filepath.Join(t.TempDir(), "mcp.log")
			c := startACP(t, e, nil)
			c.initialize()
			sid := c.newSession(e.wd, editorServer(t, log))
			if r := c.prompt(sid, "salut"); r != "end_turn" {
				t.Fatalf("stopReason=%q", r)
			}
			notices := strings.Join(updateTexts(c.allUpdates(), "agent_thought_chunk"), "\n")
			if tc.notice != "" && !strings.Contains(notices, "tenon: "+tc.notice) {
				t.Fatalf("notices=%q", notices)
			}
			if tc.started && strings.Contains(notices, "ignorés ([acp] accept_editor_mcp)") {
				t.Fatalf("notices=%q", notices)
			}
			if code := c.wait(); code != 0 {
				t.Fatalf("code=%d", code)
			}
			if _, pids := mcpLog(t, log); (len(pids) > 0) != tc.started {
				t.Fatalf("serveur lancé : pids %v", pids)
			}
			ss := sessionStarted(t, e.auditContents())
			if len(ss) != 1 {
				t.Fatalf("SessionStarted=%+v", ss)
			}
			listed := len(ss[0].MCP) == 1 && ss[0].MCP[0].ID == "ed" && ss[0].MCP[0].Jurisdiction == "local"
			if listed != tc.started {
				t.Fatalf("mcp=%+v", ss[0].MCP)
			}
		})
	}
}

func TestACPStartupRefusal(t *testing.T) {
	withProbe(t, sandbox.Status{Available: false, Reason: "x"})
	f := newFakeLLM(t, sseText("salut"))
	e := newEnvExtra(t, f, "eu", sandboxTOML("required"))
	_, _, cli := e.run("-p", "salut")
	c := startACP(t, e, nil)
	c.initialize()
	m := c.request("session/new", map[string]any{"cwd": e.wd, "mcpServers": []any{}})
	if m.Error == nil || m.Error.Code != -32603 || !strings.Contains(m.Error.Message, "sandbox") || !strings.Contains(cli, m.Error.Message) {
		t.Fatalf("erreur=%+v cli=%q", m.Error, cli)
	}
	if n := len(f.requests()); n != 0 {
		t.Fatalf("%d requête(s) au fournisseur", n)
	}
}

// Review Focus 2 : l'éditeur ferme stdin en plein tour.
func TestACPStdinClosed(t *testing.T) {
	f := newFakeLLM(t, sseCall("write_file", `{"path":"x.txt","content":"nouveau"}`), sseText("écrit"))
	e := newEnv(t, f, "eu")
	e.writeFile(filepath.Join(e.wd, "x.txt"), "ancien")
	asked := make(chan struct{}, 1)
	c := startACP(t, e, func(c *acpClient, m acpMsg) any {
		if m.Method == "session/request_permission" {
			asked <- struct{}{}
		}
		return nil
	})
	c.initialize()
	sid := c.newSession(e.wd, nil)
	go c.send(map[string]any{"jsonrpc": "2.0", "id": 99, "method": "session/prompt", "params": map[string]any{"sessionId": sid, "prompt": []map[string]any{{"type": "text", "text": "écris"}}}})
	select {
	case <-asked:
	case <-time.After(30 * time.Second):
		t.Fatal("aucune demande de permission")
	}
	if code := c.wait(); code != 0 {
		t.Fatalf("code=%d", code)
	}
	if b, _ := os.ReadFile(filepath.Join(e.wd, "x.txt")); string(b) != "ancien" {
		t.Fatalf("fichier=%q", b)
	}
	if code, out, errb := e.run("audit", "verify"); code != 0 {
		t.Fatalf("audit verify : code=%d stdout=%q stderr=%q", code, out, errb)
	}
	// Verrou de session relâché : la session se reprend.
	if code, _, errb := e.run("--resume", sid, "-p", "suite"); code != 0 {
		t.Fatalf("reprise : code=%d stderr=%q", code, errb)
	}
}

// Review Focus 5 : cwd lien symbolique vers le projet.
func TestACPSymlinkCwd(t *testing.T) {
	f := newFakeLLM(t, sseText("ok"))
	e := newEnv(t, f, "eu")
	link := filepath.Join(filepath.Dir(e.wd), "lien")
	if err := os.Symlink(e.wd, link); err != nil {
		t.Fatal(err)
	}
	e.wd = link
	if code, _, errb := e.run("-p", "salut"); code != 0 {
		t.Fatalf("cli : code=%d stderr=%q", code, errb)
	}
	c := startACP(t, e, nil)
	c.initialize()
	sid := c.newSession(link, nil)
	if r := c.prompt(sid, "salut"); r != "end_turn" {
		t.Fatalf("stopReason=%q", r)
	}
	if code := c.wait(); code != 0 {
		t.Fatalf("code=%d", code)
	}
	ss := sessionStarted(t, e.auditContents())
	if len(ss) != 2 || ss[0].ProjectRoot != ss[1].ProjectRoot {
		t.Fatalf("project_root : %+v", ss)
	}
}

func TestACPStdoutOnlyJSON(t *testing.T) {
	// Avis de démarrage (isolation indisponible) : jamais sur stdout.
	withProbe(t, sandbox.Status{Available: false, Mechanism: "none", Network: "none", Reason: "x"})
	f := newFakeLLM(t, sseCall("write_file", `{"path":"x.txt","content":"a b\nc"}`), sseText("fin \n"))
	e := newEnvExtra(t, f, "eu", sandboxTOML("auto"))
	c := startACP(t, e, allowPerm)
	c.initialize()
	sid := c.newSession(e.wd, nil)
	if r := c.prompt(sid, "écris"); r != "end_turn" {
		t.Fatalf("stopReason=%q", r)
	}
	if code := c.wait(); code != 0 {
		t.Fatalf("code=%d", code)
	}
	lines := c.lines()
	if len(lines) == 0 {
		t.Fatal("aucune sortie")
	}
	for _, l := range lines {
		var m acpMsg
		if err := json.Unmarshal([]byte(l), &m); err != nil || m.JSONRPC != "2.0" {
			t.Fatalf("ligne hors JSON-RPC : %q", l)
		}
	}
	if len(updateTexts(c.allUpdates(), "agent_thought_chunk")) == 0 {
		t.Fatal("avis de démarrage absents des notices")
	}
}

// Une commande /skill de l'éditeur est développée comme en ligne de commande
// et le skill est annoncé dans available_commands_update.
func TestACPSkillCommand(t *testing.T) {
	f := newFakeLLM(t, sseText("fait"))
	e := skillEnv(t, f)
	c := startACP(t, e, nil)
	c.initialize()
	sid := c.newSession(e.wd, nil)
	if r := c.prompt(sid, "/review le dernier commit"); r != "end_turn" {
		t.Fatalf("stopReason=%q", r)
	}
	announced := false
	for _, u := range c.allUpdates() {
		if u["sessionUpdate"] != "available_commands_update" {
			continue
		}
		cmds, _ := u["availableCommands"].([]any)
		for _, x := range cmds {
			if m, _ := x.(map[string]any); m["name"] == "review" && m["description"] == "Relit un diff avec rigueur." {
				announced = true
			}
		}
	}
	if !announced {
		t.Fatalf("skill review non annoncé : %v", c.allUpdates())
	}
	reqs := f.requests()
	if len(reqs) != 1 || !strings.Contains(reqs[0], "Regardez le diff.") || !strings.Contains(reqs[0], "le dernier commit") {
		t.Fatalf("requêtes : %q", reqs)
	}
}

// La clé du fournisseur n'atteint jamais l'éditeur : contenu lu, commande
// shell (titre, rawInput, demande de permission) et sortie sont masqués.
func TestACPKeyNeverLeaks(t *testing.T) {
	withProbe(t, sandbox.Status{Available: false, Mechanism: "none", Network: "none", Reason: "x"})
	for _, tc := range []struct{ name, tool, args string }{
		{"read_file", "read_file", `{"path":"notes.txt"}`},
		{"shell", "shell", `{"command":"echo cle-de-test"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeLLM(t, sseCall(tc.tool, tc.args), sseText("fini"))
			e := newEnvExtra(t, f, "eu", sandboxTOML("auto"))
			key := e.vars["TEST_KEY"]
			e.writeFile(filepath.Join(e.wd, "notes.txt"), "la clé : "+key+"\n")
			var asked []string
			var mu sync.Mutex
			c := startACP(t, e, func(c *acpClient, m acpMsg) any {
				if m.Method == "session/request_permission" {
					mu.Lock()
					asked = append(asked, string(m.Params))
					mu.Unlock()
				}
				return allowPerm(c, m)
			})
			c.initialize()
			sid := c.newSession(e.wd, nil)
			if r := c.prompt(sid, "vas-y"); r != "end_turn" {
				t.Fatalf("stopReason=%q", r)
			}
			if code := c.wait(); code != 0 {
				t.Fatalf("code=%d", code)
			}
			mu.Lock()
			n := len(asked)
			mu.Unlock()
			if tc.tool == "shell" && n != 1 {
				t.Fatalf("%d demande(s) de permission", n)
			}
			results := 0
			for _, u := range c.allUpdates() {
				if u["sessionUpdate"] == "tool_call_update" && u["status"] == "completed" {
					results++
				}
			}
			if results != 1 {
				t.Fatalf("résultat d'outil absent : %v", c.allUpdates())
			}
			for _, l := range c.lines() {
				if strings.Contains(l, key) {
					t.Fatalf("clé envoyée à l'éditeur : %s", l)
				}
			}
		})
	}
}

// L'éditeur qui ferme stdout ne tue pas tenon par SIGPIPE avant endSession.
func TestACPIgnoresSIGPIPE(t *testing.T) {
	e := newEnv(t, newFakeLLM(t, sseText("ok")), "eu")
	c := startACP(t, e, nil)
	c.initialize()
	if !signal.Ignored(syscall.SIGPIPE) {
		t.Fatal("SIGPIPE non ignoré")
	}
	if code := c.wait(); code != 0 {
		t.Fatalf("code=%d", code)
	}
}

// session/list : sessions du projet seulement, la plus récente d'abord,
// titre masqué et borné à 80 caractères.
func TestACPListSessions(t *testing.T) {
	f := newFakeLLM(t, sseText("un"), sseText("deux"), sseText("trois"))
	e := newEnv(t, f, "eu")
	key := e.vars["TEST_KEY"]
	other := t.TempDir()
	c := startACP(t, e, nil)
	c.initialize()
	s1 := c.newSession(e.wd, nil)
	c.prompt(s1, "premier "+key+" "+strings.Repeat("é", 120))
	time.Sleep(20 * time.Millisecond)
	s2 := c.newSession(e.wd, nil)
	c.prompt(s2, "second message")
	so := c.newSession(other, nil)
	c.prompt(so, "ailleurs")
	r := c.call("session/list", map[string]any{"cwd": e.wd})
	list, _ := r["sessions"].([]any)
	if len(list) != 2 {
		t.Fatalf("sessions=%v", r)
	}
	var ids []string
	for _, it := range list {
		m := it.(map[string]any)
		ids = append(ids, m["sessionId"].(string))
		if m["cwd"] != e.wd {
			t.Fatalf("cwd=%v", m["cwd"])
		}
		title := m["title"].(string)
		if strings.Contains(title, key) || len([]rune(title)) > 80 {
			t.Fatalf("titre non masqué ou trop long : %q", title)
		}
		if _, err := time.Parse(time.RFC3339, m["updatedAt"].(string)); err != nil || !strings.HasSuffix(m["updatedAt"].(string), "Z") {
			t.Fatalf("updatedAt=%v", m["updatedAt"])
		}
	}
	if ids[0] != s2 || ids[1] != s1 {
		t.Fatalf("ordre %v, attendu [%s %s]", ids, s2, s1)
	}
	if m := c.request("session/list", map[string]any{"cwd": "rel"}); m.Error == nil || m.Error.Code != -32602 {
		t.Fatalf("cwd relatif : %+v", m.Error)
	}
	for _, l := range c.lines() {
		if strings.Contains(l, key) {
			t.Fatalf("clé envoyée à l'éditeur : %s", l)
		}
	}
}

// _meta.tenon des réponses session/new et session/load.
func TestACPSessionMeta(t *testing.T) {
	f := newFakeLLM(t, sseText("ok"))
	e := newEnv(t, f, "eu")
	c := startACP(t, e, nil)
	c.initialize()
	r := c.call("session/new", map[string]any{"cwd": e.wd, "mcpServers": []any{}})
	sid := r["sessionId"].(string)
	check := func(r map[string]any) {
		t.Helper()
		m, _ := r["_meta"].(map[string]any)
		tn, _ := m["tenon"].(map[string]any)
		if tn["provider"] != "test" || tn["providerName"] != "Test SAS" || tn["model"] != "test-model" || tn["jurisdiction"] != "eu" {
			t.Fatalf("_meta=%v", r["_meta"])
		}
		sb, _ := tn["sandbox"].(map[string]any)
		if _, ok := sb["mode"].(string); !ok {
			t.Fatalf("sandbox=%v", tn["sandbox"])
		}
	}
	check(r)
	c.prompt(sid, "salut")
	check(c.call("session/load", map[string]any{"sessionId": sid, "cwd": e.wd, "mcpServers": []any{}}))
}

// Un journal écrit sans masquage (avant la configuration d'un secret, ou par
// une autre version) : le titre est masqué à l'envoi, comme le rejeu.
func TestACPListSessionsMasksOldJournal(t *testing.T) {
	e := newEnv(t, newFakeLLM(t, sseText("ok")), "eu")
	key := e.vars["TEST_KEY"]
	const id = "s_aaaaaaaaaaaa"
	w, err := session.Open(filepath.Join(e.dataDir, "sessions"), id, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i, ev := range []struct {
		typ event.Type
		p   any
	}{
		{event.SessionStarted, event.SessionStartedPayload{ProjectRoot: e.wd}},
		{event.UserMessage, event.UserMessagePayload{Text: "ma clé est " + key}},
	} {
		x, err := event.New(id, uint64(i+1), time.Now(), ev.typ, ev.p)
		if err != nil {
			t.Fatal(err)
		}
		if err := w.Write(x); err != nil {
			t.Fatal(err)
		}
	}
	w.Close()
	c := startACP(t, e, nil)
	c.initialize()
	r := c.call("session/list", map[string]any{"cwd": e.wd})
	list, _ := r["sessions"].([]any)
	if len(list) != 1 {
		t.Fatalf("sessions=%v", r)
	}
	if title := list[0].(map[string]any)["title"].(string); strings.Contains(title, key) || !strings.Contains(title, "ma clé est") {
		t.Fatalf("titre %q", title)
	}
	for _, l := range c.lines() {
		if strings.Contains(l, key) {
			t.Fatalf("clé envoyée à l'éditeur : %s", l)
		}
	}
}

// toolCallIDs renvoie les toolCallId des tool_call de us, dans l'ordre.
func toolCallIDs(us []map[string]any) []string {
	var ids []string
	for _, u := range us {
		if u["sessionUpdate"] == "tool_call" {
			id, _ := u["toolCallId"].(string)
			ids = append(ids, id)
		}
	}
	return ids
}

// Le fournisseur renvoie call_1 à chaque tour : deux identifiants ACP
// distincts, et le rejeu d'une session chargée redonne ceux du direct.
func TestACPToolCallIDsUniqueAndReplayed(t *testing.T) {
	f := newFakeLLM(t,
		sseCall("read_file", `{"path":"x.txt"}`), sseText("un"),
		sseCall("read_file", `{"path":"x.txt"}`), sseText("deux"))
	e := newEnv(t, f, "eu")
	e.writeFile(filepath.Join(e.wd, "x.txt"), "contenu")
	c := startACP(t, e, allowPerm)
	c.initialize()
	sid := c.newSession(e.wd, nil)
	for _, p := range []string{"premier", "second"} {
		if r := c.prompt(sid, p); r != "end_turn" {
			t.Fatalf("stopReason=%q", r)
		}
	}
	live := toolCallIDs(c.allUpdates())
	if len(live) != 2 || live[0] == live[1] {
		t.Fatalf("identifiants en direct %q", live)
	}
	if code := c.wait(); code != 0 {
		t.Fatalf("code=%d", code)
	}

	c2 := startACP(t, e, nil)
	c2.initialize()
	c2.call("session/load", map[string]any{"sessionId": sid, "cwd": e.wd, "mcpServers": []any{}})
	// Le rejeu suit la réponse de session/load.
	var replayed []string
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if replayed = toolCallIDs(c2.allUpdates()); len(replayed) >= len(live) {
			break
		}
	}
	if !reflect.DeepEqual(replayed, live) {
		t.Fatalf("rejeu %q, direct %q", replayed, live)
	}
}

// usage_update a la forme ACP { sessionUpdate, used, size } (sans size, le
// SDK ACP rejette la notification) : size = fenêtre de contexte du
// fournisseur de l'appel (défaut compris), used = jetons d'entrée.
func TestACPUsageUpdateShape(t *testing.T) {
	for _, tc := range []struct {
		extra string
		size  float64
	}{{"", 32000}, {"context_window = 64000\n", 64000}} {
		f := newFakeLLM(t, sseText("ok"))
		e := newEnvExtra(t, f, "eu", tc.extra)
		c := startACP(t, e, nil)
		c.initialize()
		sid := c.newSession(e.wd, nil)
		if r := c.prompt(sid, "salut"); r != "end_turn" {
			t.Fatalf("stopReason=%q", r)
		}
		var got []map[string]any
		for _, u := range c.allUpdates() {
			if u["sessionUpdate"] == "usage_update" {
				got = append(got, u)
			}
		}
		if len(got) != 1 {
			t.Fatalf("usage_update : %v", got)
		}
		u := got[0]
		used, okUsed := u["used"].(float64)
		size, okSize := u["size"].(float64)
		if !okUsed || !okSize || size != tc.size || used <= 0 {
			t.Fatalf("usage_update=%v", u)
		}
		m, _ := u["_meta"].(map[string]any)
		tn, _ := m["tenon"].(map[string]any)
		if tn["provider"] != "test" || tn["providerName"] != "Test SAS" || tn["jurisdiction"] != "eu" || tn["input"] != used {
			t.Fatalf("_meta=%v", u["_meta"])
		}
	}
}
