package engine

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/hooks"
	"github.com/tenon-code-eu/tenon/internal/i18n"
	"github.com/tenon-code-eu/tenon/internal/llm"
	"github.com/tenon-code-eu/tenon/internal/policy"
	"github.com/tenon-code-eu/tenon/internal/tools"
)

// hk décrit un hook de test : événement, matcher et définition JSON du hook.
type hk struct {
	ev, matcher string
	h           map[string]any
}

// cmd construit la définition d'un hook command.
func cmd(command string) map[string]any { return map[string]any{"type": "command", "command": command} }

// hookScript écrit un script bash exécutable et renvoie son chemin.
func hookScript(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "hook.sh")
	if err := os.WriteFile(p, []byte("#!/bin/bash\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// jsonHook est un hook command qui écrit out (JSON) sur stdout.
func jsonHook(t *testing.T, out string) map[string]any {
	t.Helper()
	return cmd(hookScript(t, "cat >/dev/null\ncat <<'EOF'\n"+out+"\nEOF"))
}

// withHooks charge les hooks hs depuis un hooks.json utilisateur temporaire.
func withHooks(t *testing.T, hs ...hk) func(*Deps) {
	t.Helper()
	groups := map[string][]any{}
	for _, h := range hs {
		groups[h.ev] = append(groups[h.ev], map[string]any{"matcher": h.matcher, "hooks": []any{h.h}})
	}
	b, err := json.Marshal(map[string]any{"hooks": groups})
	if err != nil {
		t.Fatal(err)
	}
	xdg := t.TempDir()
	if err := os.MkdirAll(filepath.Join(xdg, "tenon"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(xdg, "tenon", "hooks.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	set, _, err := hooks.Load(hooks.Options{Getenv: func(k string) string {
		if k == "XDG_CONFIG_HOME" {
			return xdg
		}
		return ""
	}})
	if err != nil {
		t.Fatal(err)
	}
	return func(d *Deps) { d.Hooks = set; d.Lang = i18n.EN }
}

// withShell ajoute l'outil shell aux outils fichiers.
func withShell(d *Deps) {
	d.Tools = tools.NewRegistry(append(tools.FS(d.ProjectRoot, 30000), tools.Shell(d.ProjectRoot, 5*time.Second, 30000))...)
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func hookCompleted(t *testing.T, evs []event.Event) []event.HookCompletedPayload {
	t.Helper()
	var out []event.HookCompletedPayload
	for _, e := range ofType(evs, event.HookCompleted) {
		out = append(out, decode[event.HookCompletedPayload](t, e))
	}
	return out
}

func hookStarted(t *testing.T, evs []event.Event) []event.HookStartedPayload {
	t.Helper()
	var out []event.HookStartedPayload
	for _, e := range ofType(evs, event.HookStarted) {
		out = append(out, decode[event.HookStartedPayload](t, e))
	}
	return out
}

const (
	allowOut = `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow"}}`
	askOut   = `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"ask","permissionDecisionReason":"check it"}}`
)

func TestPreToolUseDeny(t *testing.T) {
	h := newHarness(t, euInfo, [][]llm.Chunk{
		call("c1", "shell", `{"command":"touch made.txt"}`),
		text("ok"),
	}, withShell, withHooks(t, hk{"PreToolUse", "Bash", cmd(hookScript(t, `echo "no shell" >&2; exit 2`))}))
	evs, err := h.submit(t, "s_1", "go", nil)
	if err != nil {
		t.Fatal(err)
	}
	tr := toolResultOf(t, evs, "shell")
	if tr.Denied != "hook" || tr.Content != "blocked by hook: no shell" || !tr.IsError {
		t.Fatalf("ToolResult %+v", tr)
	}
	if exists(filepath.Join(h.root, "made.txt")) {
		t.Fatal("l'outil a été exécuté")
	}
	ty := types(evs)
	i := 0
	for ty[i] != event.ToolCallRequested {
		i++
	}
	want := []event.Type{event.ToolCallRequested, event.HookStarted, event.HookCompleted, event.ToolResult}
	for k, w := range want {
		if ty[i+k] != w {
			t.Fatalf("ordre %v", ty)
		}
	}
	hc := hookCompleted(t, evs)
	if len(hc) != 1 || hc[0].Status != "blocked" || hc[0].Decision != "deny" || hc[0].ExitCode == nil || *hc[0].ExitCode != 2 ||
		hc[0].CallID != "c1" || hc[0].Event != "PreToolUse" {
		t.Fatalf("HookCompleted %+v", hc)
	}
	hs := hookStarted(t, evs)
	if len(hs) != 1 || hs[0].RunID != hc[0].RunID || hs[0].Type != "command" || hs[0].CallID != "c1" {
		t.Fatalf("HookStarted %+v", hs)
	}
	if hs[0].RunID != ofType(evs, event.HookStarted)[0].ID {
		t.Fatalf("run_id %q, id %q", hs[0].RunID, ofType(evs, event.HookStarted)[0].ID)
	}
}

func TestPreToolUseAllowLiftsDefaultAsk(t *testing.T) {
	for name, opt := range map[string]func(*Deps){
		"default":          func(*Deps) {},
		"global ask rule":  withRules(policy.Rule{Tool: "write_file", Decision: policy.Ask}),
		"global ask match": withRules(policy.Rule{Tool: "write_file", Match: "a.txt", Decision: policy.Ask}),
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, euInfo, [][]llm.Chunk{
				call("c1", "write_file", `{"path":"a.txt","content":"x"}`),
				text("ok"),
			}, opt, withHooks(t, hk{"PreToolUse", "Write", jsonHook(t, allowOut)}))
			evs, err := h.submit(t, "s_1", "go", nil)
			if err != nil {
				t.Fatal(err)
			}
			if n := len(ofType(evs, event.PermissionRequested)); n != 0 {
				t.Fatalf("%d PermissionRequested", n)
			}
			if b, _ := os.ReadFile(filepath.Join(h.root, "a.txt")); string(b) != "x" {
				t.Fatalf("fichier %q, events %v", b, types(evs))
			}
		})
	}
}

// forceAskTool imite un outil MCP hors UE : ForceAsk.
type forceAskTool struct{ ran bool }

func (f *forceAskTool) Name() string            { return "mcp__srv__do" }
func (f *forceAskTool) Description() string     { return "outil MCP factice" }
func (f *forceAskTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (f *forceAskTool) Risk() tools.Risk        { return tools.Read }
func (f *forceAskTool) Target(json.RawMessage) (tools.Target, error) {
	return tools.Target{Command: "do", ForceAsk: true}, nil
}
func (f *forceAskTool) Run(context.Context, json.RawMessage) (tools.Result, error) {
	f.ran = true
	return tools.Result{Content: "done"}, nil
}

func TestPreToolUseAllowNeverLifts(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "outside.txt")
	outArgs, _ := json.Marshal(map[string]string{"path": outside, "content": "x"})
	cases := []struct {
		name, tool, args string
		opt              func(*Deps)
		written          func(h *harness) bool
	}{
		{"deny rule", "write_file", `{"path":"a.txt","content":"x"}`,
			withRules(policy.Rule{Tool: "write_file", Decision: policy.Deny}), nil},
		{"force ask", "mcp__srv__do", `{}`, nil, nil},
		{"outside project", "write_file", string(outArgs), nil,
			func(*harness) bool { return exists(outside) }},
		{"sensitive", "write_file", `{"path":".env","content":"x"}`, nil,
			func(h *harness) bool { return exists(filepath.Join(h.root, ".env")) }},
		{"project ask rule", "write_file", `{"path":"a.txt","content":"x"}`,
			withRules(policy.Rule{Tool: "write_file", Decision: policy.Ask, Project: true}), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ft := &forceAskTool{}
			opts := []func(*Deps){func(d *Deps) { d.Tools = d.Tools.With(ft) }}
			if tc.opt != nil {
				opts = append(opts, tc.opt)
			}
			opts = append(opts, withHooks(t, hk{"PreToolUse", "", jsonHook(t, allowOut)}))
			h := newHarness(t, euInfo, [][]llm.Chunk{call("c1", tc.tool, tc.args), text("ok")}, opts...)
			evs, err := h.submit(t, "s_1", "go", func(event.PermissionRequestedPayload) Reply { return Reply{} })
			if err != nil {
				t.Fatal(err)
			}
			tr := toolResultOf(t, evs, tc.tool)
			asked := len(ofType(evs, event.PermissionRequested)) == 1
			if !(tr.Denied == "policy" || (tr.Denied == "user" && asked)) {
				t.Fatalf("ToolResult %+v, events %v", tr, types(evs))
			}
			written := exists(filepath.Join(h.root, "a.txt"))
			if tc.written != nil {
				written = tc.written(h)
			}
			if written || ft.ran {
				t.Fatal("exécution silencieuse")
			}
		})
	}
}

func TestPreToolUseAskForces(t *testing.T) {
	h := newHarness(t, euInfo, [][]llm.Chunk{
		call("c1", "read_file", `{"path":"a.txt"}`),
		text("ok"),
		call("c2", "read_file", `{"path":"a.txt"}`),
		text("ok"),
	}, withHooks(t, hk{"PreToolUse", "Read", jsonHook(t, askOut)}))
	if err := os.WriteFile(filepath.Join(h.root, "a.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	yes := func(event.PermissionRequestedPayload) Reply { return Reply{Allow: true, Remember: true} }
	for i := range 2 {
		evs, err := h.submit(t, "s_1", "go", yes)
		if err != nil {
			t.Fatal(err)
		}
		pr := ofType(evs, event.PermissionRequested)
		if len(pr) != 1 {
			t.Fatalf("tour %d : %d PermissionRequested", i, len(pr))
		}
		if p := decode[event.PermissionRequestedPayload](t, pr[0]); p.HookReason != "check it" || p.Tool != "read_file" {
			t.Fatalf("payload %+v", p)
		}
		if tr := toolResultOf(t, evs, "read_file"); tr.IsError || !strings.Contains(tr.Content, "hi") {
			t.Fatalf("ToolResult %+v", tr)
		}
	}
}

// TestRememberedApprovalDoesNotBypassHookAsk : une approbation mémorisée pour
// la règle ne dispense pas d'une demande issue d'un hook.
func TestRememberedApprovalDoesNotBypassHookAsk(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ask")
	script := hookScript(t, `cat >/dev/null
if [ -e "`+marker+`" ]; then echo '`+askOut+`'; fi`)
	h := newHarness(t, euInfo, [][]llm.Chunk{
		call("c1", "write_file", `{"path":"a.txt","content":"x"}`),
		text("ok"),
		call("c2", "write_file", `{"path":"a.txt","content":"x"}`),
		text("ok"),
	}, withHooks(t, hk{"PreToolUse", "", cmd(script)}))
	yes := func(event.PermissionRequestedPayload) Reply { return Reply{Allow: true, Remember: true} }
	evs, err := h.submit(t, "s_1", "go", yes)
	if err != nil || len(ofType(evs, event.PermissionRequested)) != 1 {
		t.Fatalf("err %v, events %v", err, types(evs))
	}
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	evs, err = h.submit(t, "s_1", "go", yes)
	if err != nil {
		t.Fatal(err)
	}
	pr := ofType(evs, event.PermissionRequested)
	if len(pr) != 1 || decode[event.PermissionRequestedPayload](t, pr[0]).HookReason != "check it" {
		t.Fatalf("events %v", types(evs))
	}
}

func TestPreToolUseUpdatedInput(t *testing.T) {
	upd := func(input string) string {
		return `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","updatedInput":` + input + `}}`
	}
	t.Run("rewrite", func(t *testing.T) {
		h := newHarness(t, euInfo, [][]llm.Chunk{
			call("c1", "write_file", `{"path":"orig.txt","content":"x"}`),
			text("ok"),
		}, withHooks(t, hk{"PreToolUse", "Write", jsonHook(t, upd(`{"file_path":"out.txt","content":"y"}`))}))
		evs, err := h.submit(t, "s_1", "go", nil)
		if err != nil {
			t.Fatal(err)
		}
		if b, _ := os.ReadFile(filepath.Join(h.root, "out.txt")); string(b) != "y" {
			t.Fatalf("out.txt %q, events %v", b, types(evs))
		}
		if exists(filepath.Join(h.root, "orig.txt")) {
			t.Fatal("orig.txt écrit")
		}
		hc := hookCompleted(t, evs)
		if len(hc) != 1 || !strings.Contains(string(hc[0].UpdatedInput), "out.txt") {
			t.Fatalf("HookCompleted %+v", hc)
		}
		tcr := decode[event.ToolCallRequestedPayload](t, ofType(evs, event.ToolCallRequested)[0])
		if !strings.Contains(string(tcr.Args), "orig.txt") {
			t.Fatalf("ToolCallRequested %s", tcr.Args)
		}
	})
	t.Run("outside project asks", func(t *testing.T) {
		outside := filepath.Join(t.TempDir(), "out.txt")
		in, _ := json.Marshal(map[string]string{"file_path": outside, "content": "y"})
		h := newHarness(t, euInfo, [][]llm.Chunk{
			call("c1", "write_file", `{"path":"orig.txt","content":"x"}`),
			text("ok"),
		}, withHooks(t, hk{"PreToolUse", "Write", jsonHook(t, upd(string(in)))}))
		evs, err := h.submit(t, "s_1", "go", nil)
		if err != nil {
			t.Fatal(err)
		}
		pr := ofType(evs, event.PermissionRequested)
		if len(pr) != 1 {
			t.Fatalf("events %v", types(evs))
		}
		if p := decode[event.PermissionRequestedPayload](t, pr[0]); len(p.Paths) != 1 || !strings.Contains(p.Paths[0], "out.txt") {
			t.Fatalf("payload %+v", p)
		}
		if exists(outside) || exists(filepath.Join(h.root, "orig.txt")) {
			t.Fatal("écriture sans validation")
		}
	})
	t.Run("not an object", func(t *testing.T) {
		h := newHarness(t, euInfo, [][]llm.Chunk{
			call("c1", "write_file", `{"path":"orig.txt","content":"x"}`),
			text("ok"),
		}, withHooks(t, hk{"PreToolUse", "Write", jsonHook(t, upd(`[]`))}))
		evs, err := h.submit(t, "s_1", "go", nil)
		if err != nil {
			t.Fatal(err)
		}
		tr := toolResultOf(t, evs, "write_file")
		if tr.Denied != "hook" || !strings.HasPrefix(tr.Content, "invalid updatedInput from hook: ") {
			t.Fatalf("ToolResult %+v", tr)
		}
	})
	t.Run("invalid arguments", func(t *testing.T) {
		h := newHarness(t, euInfo, [][]llm.Chunk{
			call("c1", "write_file", `{"path":"orig.txt","content":"x"}`),
			text("ok"),
		}, withHooks(t, hk{"PreToolUse", "Write", jsonHook(t, upd(`{"content":"y"}`))}))
		evs, err := h.submit(t, "s_1", "go", nil)
		if err != nil {
			t.Fatal(err)
		}
		tr := toolResultOf(t, evs, "write_file")
		if tr.Denied != "hook" || !strings.HasPrefix(tr.Content, "invalid updatedInput from hook: ") {
			t.Fatalf("ToolResult %+v", tr)
		}
	})
	t.Run("redaction marker", func(t *testing.T) {
		h := newHarness(t, euInfo, [][]llm.Chunk{
			call("c1", "write_file", `{"path":"orig.txt","content":"x"}`),
			text("ok"),
		}, withHooks(t, hk{"PreToolUse", "Write", jsonHook(t, upd(`{"file_path":"o.txt","content":"[SECRET_1]"}`))}))
		evs, err := h.submit(t, "s_1", "go", nil)
		if err != nil {
			t.Fatal(err)
		}
		tr := toolResultOf(t, evs, "write_file")
		if tr.Denied != "hook" || !strings.Contains(tr.Content, "redaction marker") || exists(filepath.Join(h.root, "o.txt")) {
			t.Fatalf("ToolResult %+v", tr)
		}
	})
	t.Run("conflict", func(t *testing.T) {
		h := newHarness(t, euInfo, [][]llm.Chunk{
			call("c1", "write_file", `{"path":"orig.txt","content":"x"}`),
			text("ok"),
		}, withHooks(t,
			hk{"PreToolUse", "Write", jsonHook(t, upd(`{"file_path":"a.txt","content":"y"}`))},
			hk{"PreToolUse", "Write", jsonHook(t, upd(`{"file_path":"b.txt","content":"y"}`))}))
		evs, err := h.submit(t, "s_1", "go", nil)
		if err != nil {
			t.Fatal(err)
		}
		tr := toolResultOf(t, evs, "write_file")
		if tr.Denied != "hook" || tr.Content != "conflicting updatedInput from hooks" {
			t.Fatalf("ToolResult %+v", tr)
		}
	})
}

func TestHookErrorDoesNotBlock(t *testing.T) {
	h := newHarness(t, euInfo, [][]llm.Chunk{
		call("c1", "read_file", `{"path":"a.txt"}`),
		text("ok"),
	}, withHooks(t, hk{"PreToolUse", "", cmd("/nonexistent/hook.sh")}))
	if err := os.WriteFile(filepath.Join(h.root, "a.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	evs, err := h.submit(t, "s_1", "go", nil)
	if err != nil {
		t.Fatal(err)
	}
	hc := hookCompleted(t, evs)
	if len(hc) != 1 || hc[0].Status != "error" || hc[0].ExitCode == nil || *hc[0].ExitCode != 127 || hc[0].Error == "" {
		t.Fatalf("HookCompleted %+v", hc)
	}
	if tr := toolResultOf(t, evs, "read_file"); tr.IsError || !strings.Contains(tr.Content, "hi") {
		t.Fatalf("ToolResult %+v", tr)
	}
}

func TestPostToolUseFeedback(t *testing.T) {
	t.Run("feedback", func(t *testing.T) {
		h := newHarness(t, euInfo, [][]llm.Chunk{
			call("c1", "read_file", `{"path":"a.txt"}`),
			text("ok"),
		}, withHooks(t,
			hk{"PostToolUse", "Read", jsonHook(t, `{"decision":"block","reason":"fix it"}`)},
			hk{"PostToolUse", "Read", jsonHook(t, `{"hookSpecificOutput":{"hookEventName":"PostToolUse","additionalContext":"more"}}`)}))
		if err := os.WriteFile(filepath.Join(h.root, "a.txt"), []byte("hi\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		evs, err := h.submit(t, "s_1", "go", nil)
		if err != nil {
			t.Fatal(err)
		}
		tr := toolResultOf(t, evs, "read_file")
		if tr.IsError || !strings.HasSuffix(tr.Content, "\n\nhook feedback: fix it\n\nmore") {
			t.Fatalf("ToolResult %+v", tr)
		}
		reqs := h.prov.reqs()
		last := reqs[len(reqs)-1].Messages
		if !strings.Contains(last[len(last)-1].Content, "hook feedback: fix it") {
			t.Fatal("le modèle ne reçoit pas le retour du hook")
		}
	})
	t.Run("tool_response", func(t *testing.T) {
		dump := filepath.Join(t.TempDir(), "in.json")
		h := newHarness(t, euInfo, [][]llm.Chunk{
			call("c1", "read_file", `{"path":"a.txt"}`),
			text("ok"),
		}, withHooks(t, hk{"PostToolUse", "", cmd(hookScript(t, `cat > "`+dump+`"`))}))
		if err := os.WriteFile(filepath.Join(h.root, "a.txt"), []byte("hi\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := h.submit(t, "s_1", "go", nil); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(dump)
		if err != nil {
			t.Fatal(err)
		}
		var in struct {
			ToolName   string          `json:"tool_name"`
			ToolInput  map[string]any  `json:"tool_input"`
			TenonTool  string          `json:"tenon_tool"`
			TenonInput map[string]any  `json:"tenon_input"`
			ToolUseID  string          `json:"tool_use_id"`
			Event      string          `json:"hook_event_name"`
			Response   json.RawMessage `json:"tool_response"`
		}
		if err := json.Unmarshal(b, &in); err != nil {
			t.Fatal(err)
		}
		var resp struct {
			Content string `json:"content"`
			IsError *bool  `json:"is_error"`
		}
		_ = json.Unmarshal(in.Response, &resp)
		if in.ToolName != "Read" || in.ToolInput["file_path"] != "a.txt" || in.TenonTool != "read_file" ||
			in.TenonInput["path"] != "a.txt" || in.ToolUseID != "c1" || in.Event != "PostToolUse" ||
			!strings.Contains(resp.Content, "hi") || resp.IsError == nil || *resp.IsError {
			t.Fatalf("entrée %s", b)
		}
	})
	t.Run("not after a denial", func(t *testing.T) {
		marker := filepath.Join(t.TempDir(), "post")
		h := newHarness(t, euInfo, [][]llm.Chunk{
			call("c1", "read_file", `{"path":"a.txt"}`),
			text("ok"),
		}, withHooks(t,
			hk{"PreToolUse", "", cmd(hookScript(t, `echo no >&2; exit 2`))},
			hk{"PostToolUse", "", cmd(hookScript(t, `touch "`+marker+`"`))}))
		evs, err := h.submit(t, "s_1", "go", nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range hookStarted(t, evs) {
			if p.Event == "PostToolUse" {
				t.Fatal("PostToolUse après un refus")
			}
		}
		if exists(marker) {
			t.Fatal("hook PostToolUse exécuté")
		}
	})
}

func TestContinueFalseStopsTurn(t *testing.T) {
	stop := `{"continue":false,"stopReason":"enough"}`
	t.Run("PostToolUse", func(t *testing.T) {
		h := newHarness(t, euInfo, [][]llm.Chunk{
			calls(llm.ToolCall{ID: "c1", Name: "read_file", Args: json.RawMessage(`{"path":"a.txt"}`)},
				llm.ToolCall{ID: "c2", Name: "read_file", Args: json.RawMessage(`{"path":"a.txt"}`)}),
			text("next"),
		}, withHooks(t, hk{"PostToolUse", "", jsonHook(t, stop)}))
		if err := os.WriteFile(filepath.Join(h.root, "a.txt"), []byte("hi\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		evs, err := h.submit(t, "s_1", "go", nil)
		if err != nil {
			t.Fatal(err)
		}
		if tc := lastTurn(t, evs); tc.Reason != "stopped_by_hook" || tc.Iterations != 1 {
			t.Fatalf("TurnCompleted %+v", tc)
		}
		hc := hookCompleted(t, evs)
		if len(hc) != 1 || hc[0].StopReason != "enough" || !hc[0].Stop {
			t.Fatalf("HookCompleted %+v", hc)
		}
		if n := len(ofType(evs, event.ToolResult)); n != 1 {
			t.Fatalf("%d ToolResult", n)
		}
		if n := len(h.prov.reqs()); n != 1 {
			t.Fatalf("%d requêtes", n)
		}
		// L'historique reste valide : c2 a une réponse synthétique.
		h.prov.script = append(h.prov.script, text("ok"))
		if _, err := h.submit(t, "s_1", "again", nil); err != nil {
			t.Fatal(err)
		}
		reqs := h.prov.reqs()
		ids := map[string]bool{}
		for _, m := range reqs[len(reqs)-1].Messages {
			if m.Role == llm.Tool {
				ids[m.ToolCallID] = true
			}
		}
		if !ids["c1"] || !ids["c2"] {
			t.Fatalf("réponses d'outils %v", ids)
		}
	})
	t.Run("PreToolUse", func(t *testing.T) {
		h := newHarness(t, euInfo, [][]llm.Chunk{
			call("c1", "write_file", `{"path":"a.txt","content":"x"}`),
			text("next"),
		}, withHooks(t, hk{"PreToolUse", "", jsonHook(t, stop)}))
		evs, err := h.submit(t, "s_1", "go", nil)
		if err != nil {
			t.Fatal(err)
		}
		tr := toolResultOf(t, evs, "write_file")
		if tr.Denied != "hook" || tr.Content != "stopped by hook: enough" || !tr.IsError {
			t.Fatalf("ToolResult %+v", tr)
		}
		if exists(filepath.Join(h.root, "a.txt")) {
			t.Fatal("outil exécuté")
		}
		if tc := lastTurn(t, evs); tc.Reason != "stopped_by_hook" {
			t.Fatalf("TurnCompleted %+v", tc)
		}
	})
}

func TestPromptHookUsesOutbound(t *testing.T) {
	h := newHarness(t, euInfo, [][]llm.Chunk{
		call("c1", "write_file", `{"path":"a.txt","content":"MISTRAL_API_KEY=Zx8kq2LmNp4Rt6Vw9YbC1dEf"}`),
		text(`{"ok":false,"reason":"r"}`),
		text("ok"),
	}, withHooks(t, hk{"PreToolUse", "Write", map[string]any{"type": "prompt", "prompt": "Judge: $ARGUMENTS"}}))
	evs, err := h.submit(t, "s_1", "go", nil)
	if err != nil {
		t.Fatal(err)
	}
	reqs := h.prov.reqs()
	if len(reqs) != 3 {
		t.Fatalf("%d requêtes", len(reqs))
	}
	hr := reqs[1]
	if len(hr.Tools) != 0 || len(hr.Messages) != 2 || hr.Messages[0].Role != llm.System ||
		hr.Messages[0].Content != i18n.T(i18n.EN, "hooks.prompt_system") || !strings.HasPrefix(hr.Messages[1].Content, "Judge: {") {
		t.Fatalf("requête du hook %+v", hr)
	}
	if hr.Model != euInfo.Model {
		t.Fatalf("modèle %q", hr.Model)
	}
	if strings.Contains(mustJSON(hr), "Zx8kq") {
		t.Fatal("le secret n'est pas masqué dans la requête du hook")
	}
	if strings.Contains(mustJSON(reqs[2]), "Judge:") {
		t.Fatal("la requête du hook est entrée dans l'historique")
	}
	tr := toolResultOf(t, evs, "write_file")
	if tr.Denied != "hook" || tr.Content != "blocked by hook: r" {
		t.Fatalf("ToolResult %+v", tr)
	}
	// La réponse du hook n'est jamais diffusée en delta.
	for _, e := range ofType(evs, event.AssistantDelta) {
		if strings.Contains(string(e.Payload), "reason") {
			t.Fatal("delta émis pour la réponse du hook")
		}
	}
	hs := hookStarted(t, evs)
	if len(hs) != 1 || hs[0].Type != "prompt" || hs[0].Prompt != "Judge: $ARGUMENTS" || hs[0].Command != "" {
		t.Fatalf("HookStarted %+v", hs)
	}
	if hc := hookCompleted(t, evs); len(hc) != 1 || hc[0].ExitCode != nil || hc[0].Status != "blocked" {
		t.Fatalf("HookCompleted %+v", hc)
	}
}

func TestAgentHookNoRecursion(t *testing.T) {
	h := newHarness(t, euInfo, [][]llm.Chunk{
		call("c1", "read_file", `{"path":"a.txt"}`),
		call("h1", "shell", `{"command":"touch made.txt"}`),
		text(`{"ok":true}`),
		text("done"),
	}, withShell, withHooks(t, hk{"PreToolUse", "", map[string]any{"type": "agent", "prompt": "verify"}}))
	if err := os.WriteFile(filepath.Join(h.root, "a.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	evs, err := h.submit(t, "s_1", "go", nil)
	if err != nil {
		t.Fatal(err)
	}
	hs := hookStarted(t, evs)
	if len(hs) != 1 {
		t.Fatalf("%d HookStarted : récursion", len(hs))
	}
	reqs := h.prov.reqs()
	if len(reqs) != 4 {
		t.Fatalf("%d requêtes", len(reqs))
	}
	child := reqs[1]
	if names := defNames(child); strings.Join(names, ",") != "glob,grep,read_file" {
		t.Fatalf("outils du hook agent %v", names)
	}
	if !strings.Contains(child.Messages[0].Content, i18n.T(i18n.EN, "hooks.agent_preamble")) ||
		!strings.HasPrefix(child.Messages[1].Content, "verify\n\n{") {
		t.Fatalf("requête du hook agent %+v", child.Messages)
	}
	sh := toolResultOf(t, evs, "shell")
	if sh.Content != "unknown tool: shell" || sh.ParentCallID != hs[0].RunID {
		t.Fatalf("ToolResult shell %+v (run %s)", sh, hs[0].RunID)
	}
	for _, e := range ofType(evs, event.ToolCallRequested) {
		if p := decode[event.ToolCallRequestedPayload](t, e); p.CallID == "h1" && p.ParentCallID != hs[0].RunID {
			t.Fatalf("parent %q", p.ParentCallID)
		}
	}
	if exists(filepath.Join(h.root, "made.txt")) {
		t.Fatal("shell exécuté")
	}
	if hc := hookCompleted(t, evs); len(hc) != 1 || hc[0].Status != "ok" {
		t.Fatalf("HookCompleted %+v", hc)
	}
	if tr := toolResultOf(t, evs, "read_file"); tr.IsError {
		t.Fatalf("ToolResult %+v", tr)
	}
}

func TestHooksInSubagent(t *testing.T) {
	h := newAgentEngine(t, [][]llm.Chunk{
		call("m1", "agent", agentCall),
		call("s1", "read_file", `{"path":"a.txt"}`),
		text("a.txt says hi"),
		text("done"),
	}, nil, withHooks(t, hk{"PreToolUse", "Read", cmd(hookScript(t, "cat >/dev/null"))}))
	evs, err := h.submit(t, "s_1", "go", nil)
	if err != nil {
		t.Fatal(err)
	}
	hs := hookStarted(t, evs)
	hc := hookCompleted(t, evs)
	if len(hs) != 1 || hs[0].ParentCallID != "m1" || hs[0].CallID != "s1" || len(hc) != 1 || hc[0].ParentCallID != "m1" {
		t.Fatalf("HookStarted %+v HookCompleted %+v", hs, hc)
	}
}

func TestHookAuditFailure(t *testing.T) {
	h := newHarness(t, euInfo, [][]llm.Chunk{
		call("c1", "shell", `{"command":"touch made.txt"}`),
		text("ok"),
	}, withShell, withHooks(t, hk{"PreToolUse", "", jsonHook(t, `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow"}}`)}))
	h.sink.failOn = event.HookStarted
	_, err := h.submit(t, "s_1", "go", nil)
	if !errors.Is(err, ErrAuditFailure) {
		t.Fatalf("err %v", err)
	}
	if exists(filepath.Join(h.root, "made.txt")) {
		t.Fatal("outil exécuté malgré l'échec d'audit")
	}
}

func TestHookAuditTruncation(t *testing.T) {
	long := strings.Repeat("é", 3000) // 6000 octets
	script := hookScript(t, "cat >/dev/null\nprintf '%s' '"+long+"'\nprintf '%s' '"+long+"' >&2")
	h := newHarness(t, euInfo, [][]llm.Chunk{
		call("c1", "read_file", `{"path":"a.txt"}`),
		text("ok"),
	}, withHooks(t, hk{"PreToolUse", "", cmd(script + " # " + long)}))
	evs, err := h.submit(t, "s_1", "go", nil)
	if err != nil {
		t.Fatal(err)
	}
	hs := hookStarted(t, evs)
	hc := hookCompleted(t, evs)
	if len(hs) != 1 || len(hc) != 1 {
		t.Fatalf("events %v", types(evs))
	}
	for name, s := range map[string]string{"command": hs[0].Command, "stdout": hc[0].Stdout, "stderr": hc[0].Stderr} {
		if len(s) > 4096 || len(s) < 4094 || !utf8.ValidString(s) {
			t.Fatalf("%s : %d octets, valide %v", name, len(s), utf8.ValidString(s))
		}
	}
}

func TestNoHooksNoEvents(t *testing.T) {
	h := newHarness(t, euInfo, [][]llm.Chunk{
		call("c1", "read_file", `{"path":"a.txt"}`),
		text("ok"),
	}, withHooks(t, hk{"PreToolUse", "Bash", cmd("exit 2")}))
	evs, err := h.submit(t, "s_1", "go", nil)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(ofType(evs, event.HookStarted)); n != 0 {
		t.Fatalf("%d HookStarted", n)
	}
}

func TestCutBytes(t *testing.T) {
	for _, tc := range []struct {
		in   string
		n    int
		want string
	}{
		{"abc", 4, "abc"},
		{"abcd", 4, "abcd"},
		{"abcde", 4, "abcd"},
		{"aé", 2, "a"},
		{"aéb", 3, "aé"},
	} {
		if got := cutBytes(tc.in, tc.n); got != tc.want {
			t.Errorf("cutBytes(%q, %d) = %q, attendu %q", tc.in, tc.n, got, tc.want)
		}
	}
}

func TestContinueFalseInSubagent(t *testing.T) {
	h := newAgentEngine(t, [][]llm.Chunk{
		call("m1", "agent", agentCall),
		call("s1", "read_file", `{"path":"a.txt"}`),
		text("sub done"),
		text("main done"),
	}, nil, withHooks(t, hk{"PostToolUse", "Read", jsonHook(t, `{"continue":false,"stopReason":"enough"}`)}))
	evs, err := h.submit(t, "s_1", "go", nil)
	if err != nil {
		t.Fatal(err)
	}
	if tr := toolResultOf(t, evs, "agent"); !tr.IsError || !strings.HasPrefix(tr.Content, "sub-agent stopped: stopped_by_hook") {
		t.Fatalf("ToolResult %+v", tr)
	}
	if tc := lastTurn(t, evs); tc.Reason != "done" {
		t.Fatalf("TurnCompleted %+v", tc)
	}
}
