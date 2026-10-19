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
	t.Run("PostToolUse sees executed input", func(t *testing.T) {
		dump := filepath.Join(t.TempDir(), "in.json")
		h := newHarness(t, euInfo, [][]llm.Chunk{
			call("c1", "write_file", `{"path":"orig.txt","content":"x"}`),
			text("ok"),
		}, withHooks(t,
			hk{"PreToolUse", "Write", jsonHook(t, upd(`{"file_path":"out.txt","content":"y"}`))},
			hk{"PostToolUse", "Write", cmd(hookScript(t, `cat > "`+dump+`"`))}))
		if _, err := h.submit(t, "s_1", "go", nil); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(dump)
		if err != nil {
			t.Fatal(err)
		}
		var in struct {
			ToolInput  map[string]any `json:"tool_input"`
			TenonInput map[string]any `json:"tenon_input"`
		}
		if err := json.Unmarshal(b, &in); err != nil {
			t.Fatal(err)
		}
		if in.ToolInput["file_path"] != "out.txt" || in.TenonInput["path"] != "out.txt" {
			t.Fatalf("entrée PostToolUse %s", b)
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
		if err := json.Unmarshal(in.Response, &resp); err != nil {
			t.Fatalf("tool_response %s : %v", in.Response, err)
		}
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

// recHook est un hook command qui ajoute son entrée JSON (une ligne) à rec,
// puis exécute tail.
func recHook(t *testing.T, rec, tail string) map[string]any {
	t.Helper()
	return cmd(hookScript(t, "in=$(cat)\nprintf '%s\\n' \"$in\" >> '"+rec+"'\n"+tail))
}

// onceFlag renvoie un fragment bash qui exécute body à la première exécution
// seulement (fichier témoin flag).
func onceFlag(flag, body string) string {
	return "if [ ! -e '" + flag + "' ]; then touch '" + flag + "'; " + body + "; fi"
}

// inputs relit les entrées enregistrées par recHook ; nil si rec n'existe pas.
func inputs(t *testing.T, rec string) []map[string]any {
	t.Helper()
	b, err := os.ReadFile(rec)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("entrée %q : %v", l, err)
		}
		out = append(out, m)
	}
	return out
}

func userMessages(t *testing.T, evs []event.Event) []event.UserMessagePayload {
	t.Helper()
	var out []event.UserMessagePayload
	for _, e := range ofType(evs, event.UserMessage) {
		out = append(out, decode[event.UserMessagePayload](t, e))
	}
	return out
}

// lastContent renvoie le contenu du dernier message de la requête.
func lastContent(r llm.Request) string { return r.Messages[len(r.Messages)-1].Content }

func ctxOut(ev, ctx string) string {
	return `{"hookSpecificOutput":{"hookEventName":"` + ev + `","additionalContext":"` + ctx + `"}}`
}

func TestWithHookContext(t *testing.T) {
	if got := withHookContext("hi", ""); got != "hi" {
		t.Fatalf("sans contexte %q", got)
	}
	if got := withHookContext("hi", "CTX"); got != "hi\n\n[hook context]\nCTX" {
		t.Fatalf("avec contexte %q", got)
	}
}

func TestUserPromptSubmitBlock(t *testing.T) {
	block := cmd(hookScript(t, `in=$(cat); if [[ "$in" == *secret* ]]; then echo "not that" >&2; exit 2; fi`))
	h := newHarness(t, euInfo, [][]llm.Chunk{text("ok")}, withHooks(t, hk{"UserPromptSubmit", "", block}))
	evs, err := h.submit(t, "s_1", "secret plan", nil)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(h.prov.reqs()); n != 0 {
		t.Fatalf("%d requêtes", n)
	}
	um := userMessages(t, evs)
	if len(um) != 1 || !um[0].Blocked || um[0].Text != "secret plan" || um[0].HookContext != "" {
		t.Fatalf("UserMessage %+v", um)
	}
	if tc := lastTurn(t, evs); tc.Reason != "blocked_by_hook" || tc.Iterations != 0 {
		t.Fatalf("TurnCompleted %+v", tc)
	}
	if hc := hookCompleted(t, evs); len(hc) != 1 || hc[0].Reason != "not that" || hc[0].Status != "blocked" {
		t.Fatalf("HookCompleted %+v", hc)
	}
	if _, err := h.submit(t, "s_1", "hello", nil); err != nil {
		t.Fatal(err)
	}
	reqs := h.prov.reqs()
	if len(reqs) != 1 || strings.Contains(mustJSON(reqs[0]), "secret") {
		t.Fatalf("requêtes %s", mustJSON(reqs))
	}
	if n := len(reqs[0].Messages); n != 2 {
		t.Fatalf("%d messages", n)
	}
}

func TestUserPromptSubmitContext(t *testing.T) {
	rec := filepath.Join(t.TempDir(), "rec")
	h := newHarness(t, euInfo, [][]llm.Chunk{text("ok")},
		withHooks(t, hk{"UserPromptSubmit", "", recHook(t, rec, "echo CTX")}))
	evs, err := h.submit(t, "s_1", "hi", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := lastContent(h.prov.reqs()[0]); got != withHookContext("hi", "CTX") {
		t.Fatalf("message %q", got)
	}
	if um := userMessages(t, evs); len(um) != 1 || um[0].Text != "hi" || um[0].HookContext != "CTX" {
		t.Fatalf("UserMessage %+v", um)
	}
	if in := inputs(t, rec); len(in) != 1 || in[0]["prompt"] != "hi" || in[0]["hook_event_name"] != "UserPromptSubmit" {
		t.Fatalf("entrées %v", in)
	}
	if tc := lastTurn(t, evs); tc.Reason != "done" {
		t.Fatalf("TurnCompleted %+v", tc)
	}
}

func TestSessionStartContext(t *testing.T) {
	rec := filepath.Join(t.TempDir(), "rec")
	never := filepath.Join(t.TempDir(), "never")
	hs := withHooks(t,
		hk{"SessionStart", "startup|resume", recHook(t, rec, "cat <<'EOF'\n"+ctxOut("SessionStart", "START")+"\nEOF")},
		hk{"SessionStart", "compact", recHook(t, never, "")},
		hk{"UserPromptSubmit", "", cmd(hookScript(t, "cat >/dev/null; echo CTX"))})
	h := newHarness(t, euInfo, [][]llm.Chunk{text("ok")}, hs)
	evs, err := h.submit(t, "s_1", "hi", nil)
	if err != nil {
		t.Fatal(err)
	}
	want := withHookContext("hi", "START\n\nCTX")
	if got := lastContent(h.prov.reqs()[0]); got != want {
		t.Fatalf("message %q", got)
	}
	if um := userMessages(t, evs); len(um) != 1 || um[0].HookContext != "START\n\nCTX" {
		t.Fatalf("UserMessage %+v", um)
	}
	// SessionStart vient après SessionStarted et avant UserMessage.
	ty := types(evs)
	if ty[0] != event.SessionStarted || ty[1] != event.HookStarted {
		t.Fatalf("ordre %v", ty)
	}
	if _, err := h.submit(t, "s_1", "again", nil); err != nil {
		t.Fatal(err)
	}
	reqs := h.prov.reqs()
	if got := lastContent(reqs[1]); got != withHookContext("again", "CTX") {
		t.Fatalf("second tour %q", got)
	}
	if in := inputs(t, rec); len(in) != 1 || in[0]["source"] != "startup" {
		t.Fatalf("entrées %v", in)
	}
	if exists(never) {
		t.Fatal("matcher compact au démarrage")
	}

	// Reprise : source resume.
	l := &evLog{t: t, sid: "s_2"}
	l.add(event.SessionStarted, event.SessionStartedPayload{Provider: euInfo}).
		add(event.UserMessage, event.UserMessagePayload{Text: "a"})
	h2 := newHarness(t, euInfo, [][]llm.Chunk{text("ok")}, hs)
	if err := h2.eng.Restore("s_2", l.evs); err != nil {
		t.Fatal(err)
	}
	if _, err := h2.submit(t, "s_2", "b", nil); err != nil {
		t.Fatal(err)
	}
	if in := inputs(t, rec); len(in) != 2 || in[1]["source"] != "resume" {
		t.Fatalf("entrées %v", in)
	}
}

func TestPromptHookStop(t *testing.T) {
	stop := `{"continue":false,"stopReason":"enough"}`
	for _, ev := range []string{"SessionStart", "UserPromptSubmit"} {
		t.Run(ev, func(t *testing.T) {
			h := newHarness(t, euInfo, [][]llm.Chunk{text("ok")}, withHooks(t, hk{ev, "", jsonHook(t, stop)}))
			evs, err := h.submit(t, "s_1", "hi", nil)
			if err != nil {
				t.Fatal(err)
			}
			if n := len(h.prov.reqs()); n != 0 {
				t.Fatalf("%d requêtes", n)
			}
			if tc := lastTurn(t, evs); tc.Reason != "stopped_by_hook" || tc.Iterations != 0 {
				t.Fatalf("TurnCompleted %+v", tc)
			}
			if um := userMessages(t, evs); len(um) != 1 || um[0].Blocked {
				t.Fatalf("UserMessage %+v", um)
			}
			// Le message est dans l'historique.
			msgs := h.eng.sessions["s_1"].history
			if last := msgs[len(msgs)-1]; last.Role != llm.User || last.Content != "hi" {
				t.Fatalf("historique %s", mustJSON(msgs))
			}
		})
	}
}

func TestStopHookContinues(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "rec")
	stop := recHook(t, rec, onceFlag(filepath.Join(dir, "flag"), `echo '{"decision":"block","reason":"more"}'`))
	h := newHarness(t, euInfo, [][]llm.Chunk{text("first"), text("second")}, withHooks(t, hk{"Stop", "", stop}))
	evs, err := h.submit(t, "s_1", "go", nil)
	if err != nil {
		t.Fatal(err)
	}
	reqs := h.prov.reqs()
	if len(reqs) != 2 {
		t.Fatalf("%d requêtes", len(reqs))
	}
	m := reqs[1].Messages
	if last := m[len(m)-1]; last.Role != llm.User || last.Content != "Stop hook feedback: more" {
		t.Fatalf("messages %s", mustJSON(m))
	}
	if prev := m[len(m)-2]; prev.Role != llm.Assistant || prev.Content != "first" {
		t.Fatalf("messages %s", mustJSON(m))
	}
	in := inputs(t, rec)
	if len(in) != 2 || in[0]["stop_hook_active"] != false || in[1]["stop_hook_active"] != true {
		t.Fatalf("entrées %v", in)
	}
	um := userMessages(t, evs)
	if len(um) != 2 || um[1].Text != "Stop hook feedback: more" || um[1].Hook != "Stop" || um[1].ParentCallID != "" {
		t.Fatalf("UserMessage %+v", um)
	}
	if tc := lastTurn(t, evs); tc.Reason != "done" || tc.Iterations != 2 {
		t.Fatalf("TurnCompleted %+v", tc)
	}
}

func TestStopHookAlwaysBlocks(t *testing.T) {
	rec := filepath.Join(t.TempDir(), "rec")
	h := newHarness(t, euInfo, [][]llm.Chunk{text("x")},
		func(d *Deps) { d.MaxIterations = 3 },
		withHooks(t, hk{"Stop", "", recHook(t, rec, `echo again >&2; exit 2`)}))
	evs, err := h.submit(t, "s_1", "go", nil)
	if err != nil {
		t.Fatal(err)
	}
	if tc := lastTurn(t, evs); tc.Reason != "max_iterations" || tc.Iterations != 3 {
		t.Fatalf("TurnCompleted %+v", tc)
	}
	if n := len(h.prov.reqs()); n != 3 {
		t.Fatalf("%d requêtes", n)
	}
	in := inputs(t, rec)
	if len(in) != 3 || in[0]["stop_hook_active"] != false || in[1]["stop_hook_active"] != true || in[2]["stop_hook_active"] != true {
		t.Fatalf("entrées %v", in)
	}
}

func TestStopHookContinueFalse(t *testing.T) {
	h := newHarness(t, euInfo, [][]llm.Chunk{text("x")},
		withHooks(t, hk{"Stop", "", jsonHook(t, `{"continue":false,"decision":"block","reason":"r"}`)}))
	evs, err := h.submit(t, "s_1", "go", nil)
	if err != nil {
		t.Fatal(err)
	}
	if tc := lastTurn(t, evs); tc.Reason != "stopped_by_hook" || tc.Iterations != 1 {
		t.Fatalf("TurnCompleted %+v", tc)
	}
	if n := len(h.prov.reqs()); n != 1 {
		t.Fatalf("%d requêtes", n)
	}
}

func TestStopNotOnCancelOrError(t *testing.T) {
	for name, script := range map[string][][]llm.Chunk{
		"provider error": {{{Done: true, Err: errors.New("boom")}}},
		"incomplete":     {{{Text: "par"}}},
	} {
		t.Run(name, func(t *testing.T) {
			rec := filepath.Join(t.TempDir(), "rec")
			h := newHarness(t, euInfo, script, withHooks(t, hk{"Stop", "", recHook(t, rec, "")}))
			evs, _ := h.submit(t, "s_1", "go", nil)
			if n := len(ofType(evs, event.HookStarted)); n != 0 || exists(rec) {
				t.Fatalf("%d HookStarted", n)
			}
		})
	}
	// Annulation reçue avec la fin du flux (réponse complète, sans appel
	// d'outil) : la boucle verrait une fin done, mais aucun Stop ne part.
	t.Run("cancelled at end of stream", func(t *testing.T) {
		rec := filepath.Join(t.TempDir(), "rec")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		h := newHarness(t, euInfo, nil, withHooks(t, hk{"Stop", "", recHook(t, rec, "")}), func(d *Deps) {
			d.Provider = &cancelProvider{info: euInfo, cancel: cancel}
		})
		if err := h.eng.Submit(ctx, "s_1", "go"); !errors.Is(err, context.Canceled) {
			t.Fatalf("err %v", err)
		}
		if n := len(ofType(h.sink.events, event.HookStarted)); n != 0 || exists(rec) {
			t.Fatalf("%d HookStarted", n)
		}
		if tc := lastTurn(t, h.sink.events); tc.Reason != "cancelled" {
			t.Fatalf("TurnCompleted %+v", tc)
		}
	})
}

// cancelProvider renvoie une réponse complète et annule le contexte du tour
// juste avant le dernier fragment (Done).
type cancelProvider struct {
	info   llm.ProviderInfo
	cancel context.CancelFunc
}

func (p *cancelProvider) Info() llm.ProviderInfo { return p.info }

func (p *cancelProvider) Stream(ctx context.Context, req llm.Request) (<-chan llm.Chunk, error) {
	ch := make(chan llm.Chunk, 2)
	ch <- llm.Chunk{Text: "x"}
	p.cancel()
	ch <- llm.Chunk{Done: true}
	close(ch)
	return ch, nil
}

func TestSubagentStop(t *testing.T) {
	dir := t.TempDir()
	subRec, stopRec := filepath.Join(dir, "sub"), filepath.Join(dir, "stop")
	h := newAgentEngine(t, [][]llm.Chunk{
		call("m1", "agent", agentCall),
		text("sub first"),
		text("sub second"),
		text("main done"),
	}, nil, withHooks(t,
		hk{"SubagentStop", "", recHook(t, subRec, onceFlag(filepath.Join(dir, "flag"), `echo once >&2; exit 2`))},
		hk{"Stop", "", recHook(t, stopRec, "")}))
	evs, err := h.submit(t, "s_1", "go", nil)
	if err != nil {
		t.Fatal(err)
	}
	reqs := h.prov.reqs()
	if len(reqs) != 4 {
		t.Fatalf("%d requêtes", len(reqs))
	}
	if got := lastContent(reqs[2]); got != "Stop hook feedback: once" {
		t.Fatalf("requête du sous-agent %q", got)
	}
	in := inputs(t, subRec)
	if len(in) != 2 || in[0]["agent"] != "general-purpose" || in[0]["tool_use_id"] != "m1" ||
		in[0]["stop_hook_active"] != false || in[1]["stop_hook_active"] != true {
		t.Fatalf("entrées SubagentStop %v", in)
	}
	if in := inputs(t, stopRec); len(in) != 1 {
		t.Fatalf("entrées Stop %v", in)
	}
	if tr := toolResultOf(t, evs, "agent"); tr.IsError || tr.Content != "sub second" {
		t.Fatalf("ToolResult %+v", tr)
	}
	um := userMessages(t, evs)
	if len(um) != 2 || um[1].Hook != "SubagentStop" || um[1].ParentCallID != "m1" || um[1].Text != "Stop hook feedback: once" {
		t.Fatalf("UserMessage %+v", um)
	}
	// Le retour au sous-agent n'entre pas dans l'historique principal.
	if strings.Contains(mustJSON(reqs[3]), "Stop hook feedback") {
		t.Fatal("retour du sous-agent dans l'historique principal")
	}
}

func TestPreCompactAndSessionStartCompact(t *testing.T) {
	dir := t.TempDir()
	pre, post := filepath.Join(dir, "pre"), filepath.Join(dir, "post")
	hs := withHooks(t,
		hk{"PreCompact", "auto", recHook(t, pre, "")},
		hk{"SessionStart", "compact", recHook(t, post, "cat <<'EOF'\n"+ctxOut("SessionStart", "AFTER")+"\nEOF")})
	h := newHarness(t, euInfo, [][]llm.Chunk{text("LE RÉSUMÉ"), text("ok")}, window(1000), hs)
	l := &evLog{t: t, sid: "s_1"}
	for _, m := range longHistory() {
		if m.Role == llm.User {
			l.add(event.UserMessage, event.UserMessagePayload{Text: m.Content})
		} else {
			l.add(event.AssistantMessage, event.AssistantMessagePayload{Text: m.Content})
		}
	}
	if err := h.eng.Restore("s_1", l.evs); err != nil {
		t.Fatal(err)
	}
	evs, err := h.submit(t, "s_1", "suite", nil)
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, p := range hookStarted(t, evs) {
		order = append(order, p.Event)
	}
	if strings.Join(order, ",") != "PreCompact,SessionStart" {
		t.Fatalf("hooks %v", order)
	}
	if in := inputs(t, pre); len(in) != 1 || in[0]["trigger"] != "auto" {
		t.Fatalf("entrées PreCompact %v", in)
	}
	if in := inputs(t, post); len(in) != 1 || in[0]["source"] != "compact" {
		t.Fatalf("entrées SessionStart %v", in)
	}
	cc := ofType(evs, event.ContextCompacted)
	if len(cc) != 1 {
		t.Fatalf("events %v", types(evs))
	}
	if p := decode[event.ContextCompactedPayload](t, cc[0]); p.HookContext != "AFTER" || p.Summary != "LE RÉSUMÉ" {
		t.Fatalf("ContextCompacted %+v", p)
	}
	sum := h.prov.reqs()[1].Messages[1].Content
	if sum != withHookContext(summaryPrefix+"LE RÉSUMÉ", "AFTER") || !strings.Contains(sum, "[hook context]") {
		t.Fatalf("message de résumé %q", sum)
	}

	// La reprise reconstruit le même résumé.
	live := h.eng.sessions["s_1"].history
	h2 := newHarness(t, euInfo, [][]llm.Chunk{text("ok")})
	if err := h2.eng.Restore("s_1", append(l.evs, h.sink.events...)); err != nil {
		t.Fatal(err)
	}
	if got := h2.eng.sessions["s_1"].history; mustJSON(got) != mustJSON(live) {
		t.Fatalf("historique restauré\n%s\nattendu\n%s", mustJSON(got), mustJSON(live))
	}
}

func TestNotification(t *testing.T) {
	notify := func(d *Deps) { d.Notify = true }
	t.Run("tool", func(t *testing.T) {
		rec := filepath.Join(t.TempDir(), "rec")
		h := newHarness(t, euInfo, [][]llm.Chunk{
			call("c1", "write_file", `{"path":"a.txt","content":"x"}`),
			text("ok"),
		}, notify, withHooks(t, hk{"Notification", "permission_prompt", recHook(t, rec, "")}))
		if _, err := h.submit(t, "s_1", "go", func(event.PermissionRequestedPayload) Reply { return Reply{Allow: true} }); err != nil {
			t.Fatal(err)
		}
		in := inputs(t, rec)
		if len(in) != 1 || in[0]["message"] != "tenon needs your permission to use write_file" ||
			in[0]["notification_type"] != "permission_prompt" {
			t.Fatalf("entrées %v", in)
		}
	})
	t.Run("jurisdiction", func(t *testing.T) {
		rec := filepath.Join(t.TempDir(), "rec")
		h := newHarness(t, nonEUInfo, [][]llm.Chunk{text("ok")},
			notify, withHooks(t, hk{"Notification", "", recHook(t, rec, "")}))
		if _, err := h.submit(t, "s_1", "go", func(event.PermissionRequestedPayload) Reply { return Reply{Allow: true} }); err != nil {
			t.Fatal(err)
		}
		if in := inputs(t, rec); len(in) != 1 || in[0]["message"] != "tenon needs your permission to send data to openai" {
			t.Fatalf("entrées %v", in)
		}
	})
	t.Run("disabled", func(t *testing.T) {
		rec := filepath.Join(t.TempDir(), "rec")
		h := newHarness(t, euInfo, [][]llm.Chunk{
			call("c1", "write_file", `{"path":"a.txt","content":"x"}`),
			text("ok"),
		}, withHooks(t, hk{"Notification", "", recHook(t, rec, "")}))
		evs, err := h.submit(t, "s_1", "go", func(event.PermissionRequestedPayload) Reply { return Reply{Allow: true} })
		if err != nil {
			t.Fatal(err)
		}
		if n := len(ofType(evs, event.HookStarted)); n != 0 || exists(rec) {
			t.Fatalf("%d HookStarted", n)
		}
	})
}

func TestEndSession(t *testing.T) {
	rec := filepath.Join(t.TempDir(), "rec")
	hs := withHooks(t,
		hk{"SessionEnd", "", recHook(t, rec, "")},
		hk{"SessionEnd", "", cmd("sleep 10")})
	h := newHarness(t, euInfo, [][]llm.Chunk{text("ok")}, hs)
	if _, err := h.submit(t, "s_1", "hi", nil); err != nil {
		t.Fatal(err)
	}
	n0 := len(h.sink.events)
	start := time.Now()
	if err := h.eng.EndSession(context.Background(), "s_1", "prompt_input_exit"); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 6*time.Second {
		t.Fatalf("durée %v", d)
	}
	evs := h.sink.events[n0:]
	if n := len(ofType(evs, event.TurnCompleted)); n != 0 {
		t.Fatalf("%d TurnCompleted", n)
	}
	hc := hookCompleted(t, evs)
	if len(hc) != 2 || hc[0].Status != "ok" || hc[1].Status != "timeout" || hc[0].Event != "SessionEnd" {
		t.Fatalf("HookCompleted %+v", hc)
	}
	if in := inputs(t, rec); len(in) != 1 || in[0]["reason"] != "prompt_input_exit" {
		t.Fatalf("entrées %v", in)
	}

	// Session jamais démarrée (inconnue, ou restaurée sans tour) : aucun hook.
	rec2 := filepath.Join(t.TempDir(), "rec")
	h2 := newHarness(t, euInfo, [][]llm.Chunk{text("ok")}, withHooks(t, hk{"SessionEnd", "", recHook(t, rec2, "")}))
	if err := h2.eng.EndSession(context.Background(), "s_x", "other"); err != nil {
		t.Fatal(err)
	}
	if err := h2.eng.Restore("s_r", nil); err != nil {
		t.Fatal(err)
	}
	if err := h2.eng.EndSession(context.Background(), "s_r", "other"); err != nil {
		t.Fatal(err)
	}
	if exists(rec2) || len(h2.sink.events) != 0 {
		t.Fatalf("events %v", types(h2.sink.events))
	}
	if _, ok := h2.eng.sessions["s_x"]; ok {
		t.Fatal("session créée par EndSession")
	}
}

func TestEndSessionIgnoresContinueFalse(t *testing.T) {
	h := newHarness(t, euInfo, [][]llm.Chunk{text("ok")},
		withHooks(t, hk{"SessionEnd", "", jsonHook(t, `{"continue":false,"stopReason":"x"}`)}))
	if _, err := h.submit(t, "s_1", "hi", nil); err != nil {
		t.Fatal(err)
	}
	if err := h.eng.EndSession(context.Background(), "s_1", "other"); err != nil {
		t.Fatal(err)
	}
	// La session reste utilisable normalement.
	evs, err := h.submit(t, "s_1", "again", nil)
	if err != nil {
		t.Fatal(err)
	}
	if tc := lastTurn(t, evs); tc.Reason != "done" {
		t.Fatalf("TurnCompleted %+v", tc)
	}
}

func TestSessionStartContextBlockedFirstPrompt(t *testing.T) {
	hs := withHooks(t,
		hk{"SessionStart", "", jsonHook(t, ctxOut("SessionStart", "START"))},
		hk{"UserPromptSubmit", "", cmd(hookScript(t, `in=$(cat); if [[ "$in" == *secret* ]]; then echo no >&2; exit 2; fi; echo CTX`))})
	h := newHarness(t, euInfo, [][]llm.Chunk{text("ok")}, hs)
	evs, err := h.submit(t, "s_1", "secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	if um := userMessages(t, evs); len(um) != 1 || !um[0].Blocked || um[0].HookContext != "START" {
		t.Fatalf("UserMessage %+v", um)
	}
	blocked := append([]event.Event(nil), h.sink.events...)
	if _, err := h.submit(t, "s_1", "hi", nil); err != nil {
		t.Fatal(err)
	}
	if got := lastContent(h.prov.reqs()[0]); got != withHookContext("hi", "START\n\nCTX") {
		t.Fatalf("message %q", got)
	}
	live := mustJSON(h.eng.sessions["s_1"].history)
	h2 := newHarness(t, euInfo, [][]llm.Chunk{text("ok")})
	if err := h2.eng.Restore("s_1", h.sink.events); err != nil {
		t.Fatal(err)
	}
	if got := mustJSON(h2.eng.sessions["s_1"].history); got != live {
		t.Fatalf("historique restauré\n%s\nattendu\n%s", got, live)
	}

	// Reprise juste après le prompt bloqué : le contexte reste en attente.
	h3 := newHarness(t, euInfo, [][]llm.Chunk{text("ok")})
	if err := h3.eng.Restore("s_1", blocked); err != nil {
		t.Fatal(err)
	}
	if _, err := h3.submit(t, "s_1", "hi", nil); err != nil {
		t.Fatal(err)
	}
	if got := lastContent(h3.prov.reqs()[0]); got != withHookContext("hi", "START") {
		t.Fatalf("message après reprise %q", got)
	}
}

func TestEndSessionBoundedWait(t *testing.T) {
	rec := filepath.Join(t.TempDir(), "rec")
	h := newHarness(t, euInfo, [][]llm.Chunk{text("ok")}, withHooks(t, hk{"SessionEnd", "", recHook(t, rec, "")}))
	if _, err := h.submit(t, "s_1", "hi", nil); err != nil {
		t.Fatal(err)
	}
	s := h.eng.sessions["s_1"]
	s.turn.Lock() // un tour encore en cours
	defer s.turn.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := h.eng.EndSession(ctx, "s_1", "other"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err %v", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("durée %v", d)
	}
	if exists(rec) {
		t.Fatal("hook exécuté")
	}
}

func TestPreCompactContinueFalse(t *testing.T) {
	h := newHarness(t, euInfo, [][]llm.Chunk{text("LE RÉSUMÉ"), text("ok")}, window(1000),
		withHooks(t, hk{"PreCompact", "", jsonHook(t, `{"continue":false,"stopReason":"no"}`)}))
	seed(t, h, "s_1", longHistory()...)
	evs, err := h.submit(t, "s_1", "suite", nil)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(h.prov.reqs()); n != 0 {
		t.Fatalf("%d requêtes", n)
	}
	if n := len(ofType(evs, event.ContextCompacted)); n != 0 {
		t.Fatalf("%d ContextCompacted", n)
	}
	if tc := lastTurn(t, evs); tc.Reason != "stopped_by_hook" || tc.Iterations != 0 {
		t.Fatalf("TurnCompleted %+v", tc)
	}
}

// TestPermissionArgsOnlyWhenUpdated : la demande porte les arguments exécutés
// seulement quand un updatedInput les a changés.
func TestPermissionArgsOnlyWhenUpdated(t *testing.T) {
	upd := func(input string) string {
		return `{"hookSpecificOutput":{"hookEventName":"PreToolUse","updatedInput":` + input + `}}`
	}
	for _, c := range []struct {
		name, out, want string
	}{
		{"changed", upd(`{"file_path":"a.txt","content":"changed"}`), "changed"},
		{"same", upd(`{"content":"x","file_path":"a.txt"}`), ""},
		{"no updatedInput", `{}`, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t, euInfo, [][]llm.Chunk{
				call("c1", "write_file", `{"path":"a.txt","content":"x"}`),
				text("ok"),
			}, withHooks(t, hk{"PreToolUse", "Write", jsonHook(t, c.out)}))
			evs, err := h.submit(t, "s_1", "go", nil)
			if err != nil {
				t.Fatal(err)
			}
			pr := ofType(evs, event.PermissionRequested)
			if len(pr) != 1 {
				t.Fatalf("events %v", types(evs))
			}
			p := decode[event.PermissionRequestedPayload](t, pr[0])
			if c.want == "" {
				if p.Args != nil || strings.Contains(string(pr[0].Payload), `"args"`) {
					t.Fatalf("Args %s (payload %s)", p.Args, pr[0].Payload)
				}
				return
			}
			var a struct{ Path, Content string }
			if err := json.Unmarshal(p.Args, &a); err != nil || a.Path != "a.txt" || a.Content != c.want {
				t.Fatalf("Args %s : %v", p.Args, err)
			}
		})
	}
}

// TestHookAskEmptyReason : un ask de hook sans raison porte une raison fixe.
func TestHookAskEmptyReason(t *testing.T) {
	h := newHarness(t, euInfo, [][]llm.Chunk{
		call("c1", "read_file", `{"path":"a.txt"}`),
		text("ok"),
	}, withHooks(t, hk{"PreToolUse", "Read", jsonHook(t, `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"ask"}}`)}))
	if err := os.WriteFile(filepath.Join(h.root, "a.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	evs, err := h.submit(t, "s_1", "go", nil)
	if err != nil {
		t.Fatal(err)
	}
	pr := ofType(evs, event.PermissionRequested)
	if len(pr) != 1 {
		t.Fatalf("events %v", types(evs))
	}
	if p := decode[event.PermissionRequestedPayload](t, pr[0]); p.HookReason != "(no reason given)" {
		t.Fatalf("HookReason %q", p.HookReason)
	}
}

// TestPostToolUseNotAfterToolRefusal : PostToolUse ne suit jamais un refus
// décidé par l'outil lui-même.
func TestPostToolUseNotAfterToolRefusal(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "post")
	ft := &fakeTool{res: []tools.Result{{Content: "refusé", IsError: true, Denied: "policy"}}}
	h := newHarness(t, euInfo, [][]llm.Chunk{call("c1", "fake_read", `{}`), text("fin")},
		withFake(ft), withHooks(t, hk{"PostToolUse", "", cmd(hookScript(t, `touch "`+marker+`"`))}))
	evs, err := h.submit(t, "s_1", "go", nil)
	if err != nil {
		t.Fatal(err)
	}
	if hs := hookStarted(t, evs); len(hs) != 0 {
		t.Fatalf("HookStarted %+v", hs)
	}
	if exists(marker) {
		t.Fatal("hook PostToolUse exécuté")
	}
	if tr := toolResultOf(t, evs, "fake_read"); tr.Denied != "policy" {
		t.Fatalf("ToolResult %+v", tr)
	}
}

// driveHookAsk lance un tour où un hook agent demande une permission (lecture
// hors du projet) : onHook reçoit cette demande (sans y répondre), les
// demandes de l'agent principal sont acceptées. Renvoie les événements.
func driveHookAsk(t *testing.T, timeout int, onHook func(cancel context.CancelFunc)) (*harness, []event.Event, string, error) {
	t.Helper()
	outside := filepath.Join(t.TempDir(), "o.txt")
	if err := os.WriteFile(outside, []byte("o\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hook := map[string]any{"type": "agent", "prompt": "verify"}
	if timeout > 0 {
		hook["timeout"] = timeout
	}
	h := newHarness(t, euInfo, [][]llm.Chunk{
		call("c1", "write_file", `{"path":"a.txt","content":"x"}`),
		call("h1", "read_file", mustJSON(map[string]string{"path": outside})),
		text("ok"),
	}, withHooks(t, hk{"PreToolUse", "Write", hook}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- h.eng.Submit(ctx, "s_1", "go") }()
	var (
		evs      []event.Event
		hookPerm string
	)
	handle := func(ev event.Event) {
		evs = append(evs, ev)
		if ev.Type != event.PermissionRequested {
			return
		}
		p := decode[event.PermissionRequestedPayload](t, ev)
		if p.ParentCallID != "" {
			hookPerm = p.PermissionID
			onHook(cancel)
			return
		}
		if err := h.eng.Respond(p.PermissionID, Reply{Allow: true}); err != nil {
			t.Errorf("Respond: %v", err)
		}
	}
	for {
		select {
		case ev := <-h.sub:
			handle(ev)
		case err := <-done:
			for {
				select {
				case ev := <-h.sub:
					handle(ev)
				default:
					return h, evs, hookPerm, err
				}
			}
		}
	}
}

// resolvedFor renvoie les PermissionResolved de la demande id.
func resolvedFor(t *testing.T, evs []event.Event, id string) []event.PermissionResolvedPayload {
	t.Helper()
	var out []event.PermissionResolvedPayload
	for _, e := range ofType(evs, event.PermissionResolved) {
		if p := decode[event.PermissionResolvedPayload](t, e); p.PermissionID == id {
			out = append(out, p)
		}
	}
	return out
}

// TestHookAskTimeoutResolves : une demande faite dans un hook dont le délai
// expire est résolue (refus), et la demande suivante reste traitée.
func TestHookAskTimeoutResolves(t *testing.T) {
	h, evs, hookPerm, err := driveHookAsk(t, 1, func(context.CancelFunc) {})
	if err != nil {
		t.Fatal(err)
	}
	if hookPerm == "" {
		t.Fatalf("aucune demande du hook : %v", types(evs))
	}
	if r := resolvedFor(t, evs, hookPerm); len(r) != 1 || r[0].Allowed || r[0].ParentCallID == "" {
		t.Fatalf("PermissionResolved %+v", r)
	}
	if err := h.eng.Respond(hookPerm, Reply{Allow: true}); !errors.Is(err, ErrUnknownPermission) {
		t.Fatalf("demande du hook encore en attente : %v", err)
	}
	if hc := hookCompleted(t, evs); len(hc) != 1 || hc[0].Status != "timeout" {
		t.Fatalf("HookCompleted %+v", hc)
	}
	if b, _ := os.ReadFile(filepath.Join(h.root, "a.txt")); string(b) != "x" {
		t.Fatalf("a.txt %q, events %v", b, types(evs))
	}
}

// TestHookAskTurnCancelled : l'annulation du tour n'émet pas de
// PermissionResolved pour la demande du hook.
func TestHookAskTurnCancelled(t *testing.T) {
	_, evs, hookPerm, _ := driveHookAsk(t, 0, func(cancel context.CancelFunc) { cancel() })
	if hookPerm == "" {
		t.Fatalf("aucune demande du hook : %v", types(evs))
	}
	if r := resolvedFor(t, evs, hookPerm); len(r) != 0 {
		t.Fatalf("PermissionResolved %+v", r)
	}
}
