//go:build unix

package hooks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var scriptSeq atomic.Int64

// script écrit un script sh dans dir et renvoie un hook command qui l'exécute.
func script(t *testing.T, dir string, ev Event, body string) Hook {
	t.Helper()
	p := filepath.Join(dir, fmt.Sprintf("h%d-%s.sh", scriptSeq.Add(1), ev))
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return Hook{ID: "tenon:" + string(ev) + ":0.0", Source: SourceTenon, Event: ev, Type: Command,
		Command: "sh " + p, Timeout: 10 * time.Second}
}

func runHook(t *testing.T, h Hook, fields map[string]any) Outcome {
	t.Helper()
	return Run(context.Background(), []Hook{h}, h.Event, fields,
		Env{SessionID: "s1", TranscriptPath: "/t.jsonl", ProjectRoot: t.TempDir()}, nil)
}

func TestRunExit0JSONPreToolUse(t *testing.T) {
	h := script(t, t.TempDir(), PreToolUse,
		`echo '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"ask","permissionDecisionReason":"why"}}'`)
	o := runHook(t, h, nil)
	if !o.Ask || o.Block || o.Reason != "why" {
		t.Fatalf("outcome = %+v", o)
	}
}

func TestRunExit2(t *testing.T) {
	d := t.TempDir()
	o := runHook(t, script(t, d, PreToolUse, "echo no >&2; exit 2"), nil)
	if !o.Block || o.Reason != "no" || o.Results[0].Status != StatusBlocked {
		t.Fatalf("pre = %+v", o)
	}
	o = runHook(t, script(t, d, SessionStart, "echo no >&2; exit 2"), nil)
	if o.Block || o.Results[0].Status != StatusError || !strings.Contains(o.Results[0].Err, "no") {
		t.Fatalf("start = %+v", o)
	}
}

func TestRunPlainStdoutContext(t *testing.T) {
	d := t.TempDir()
	o := runHook(t, script(t, d, UserPromptSubmit, "echo ctx"), nil)
	if o.Context != "ctx" {
		t.Fatalf("context = %q", o.Context)
	}
	o = runHook(t, script(t, d, PreToolUse, "echo ctx"), nil)
	if o.Context != "" || o.Results[0].Stdout != "ctx\n" {
		t.Fatalf("pre = %+v", o)
	}
}

func TestRunInput(t *testing.T) {
	d := t.TempDir()
	out := filepath.Join(d, "in.json")
	root := t.TempDir()
	h := script(t, d, PreToolUse, "cat > "+out+"\npwd > "+out+".pwd\necho $CLAUDE_PROJECT_DIR $TENON_PROJECT_DIR > "+out+".env")
	Run(context.Background(), []Hook{h}, PreToolUse, map[string]any{"tool_name": "Bash"},
		Env{SessionID: "s1", TranscriptPath: "/t.jsonl", ProjectRoot: root}, nil)
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m["tool_name"] != "Bash" || m["session_id"] != "s1" || m["transcript_path"] != "/t.jsonl" ||
		m["cwd"] != root || m["hook_event_name"] != "PreToolUse" {
		t.Fatalf("input = %v", m)
	}
	env, _ := os.ReadFile(out + ".env")
	if strings.TrimSpace(string(env)) != root+" "+root {
		t.Fatalf("env = %q", env)
	}
	pwd, _ := os.ReadFile(out + ".pwd")
	real, _ := filepath.EvalSymlinks(root)
	if got := strings.TrimSpace(string(pwd)); got != root && got != real {
		t.Fatalf("pwd = %q", got)
	}
}

func TestRunNonBlockingErrors(t *testing.T) {
	d := t.TempDir()
	for name, h := range map[string]Hook{
		"exit1":   script(t, d, PreToolUse, "exit 1"),
		"missing": {ID: "x", Event: PreToolUse, Type: Command, Command: "/nonexistent/x.sh", Timeout: 5 * time.Second},
		"badjson": script(t, d, PreToolUse, "echo '{bad'"),
	} {
		o := runHook(t, h, nil)
		if o.Block || o.Results[0].Status != StatusError || o.Results[0].Err == "" {
			t.Errorf("%s: %+v", name, o)
		}
	}
}

func TestRunTimeout(t *testing.T) {
	h := script(t, t.TempDir(), PreToolUse, "sleep 5")
	h.Timeout = 200 * time.Millisecond
	start := time.Now()
	o := runHook(t, h, nil)
	if o.Results[0].Status != StatusTimeout || time.Since(start) > 2*time.Second {
		t.Fatalf("%+v after %s", o.Results[0], time.Since(start))
	}
}

func TestRunBackgroundChild(t *testing.T) {
	h := script(t, t.TempDir(), PreToolUse, "sleep 30 &\necho ok")
	start := time.Now()
	o := runHook(t, h, nil)
	if o.Results[0].Status != StatusOK || time.Since(start) > 3*time.Second {
		t.Fatalf("%+v after %s", o.Results[0], time.Since(start))
	}
}

func TestRunCancel(t *testing.T) {
	h := script(t, t.TempDir(), PreToolUse, "sleep 5")
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	start := time.Now()
	o := Run(ctx, []Hook{h}, PreToolUse, nil, Env{ProjectRoot: t.TempDir()}, nil)
	if o.Results[0].Status != StatusCancelled || time.Since(start) > 2*time.Second {
		t.Fatalf("%+v", o.Results[0])
	}
}

func TestRunIgnoresStdin(t *testing.T) {
	h := script(t, t.TempDir(), PreToolUse, "exit 0")
	o := runHook(t, h, map[string]any{"big": strings.Repeat("x", 1<<20)})
	if o.Results[0].Status != StatusOK {
		t.Fatalf("%+v", o.Results[0])
	}
}

func TestRunCaps(t *testing.T) {
	h := script(t, t.TempDir(), PreToolUse, "head -c 200000 /dev/zero | tr '\\0' a")
	o := runHook(t, h, nil)
	if len(o.Results[0].Stdout) != 65536 {
		t.Fatalf("len = %d", len(o.Results[0].Stdout))
	}
}

func TestRunParallel(t *testing.T) {
	d := t.TempDir()
	var hs []Hook
	for i := 0; i < 3; i++ {
		h := script(t, d, PreToolUse, "sleep 1")
		h.ID = "tenon:PreToolUse:0." + string(rune('0'+i))
		h.Command += " "
		hs = append(hs, h)
	}
	start := time.Now()
	o := Run(context.Background(), hs, PreToolUse, nil, Env{ProjectRoot: t.TempDir()}, nil)
	if time.Since(start) > 2500*time.Millisecond || len(o.Results) != 3 {
		t.Fatalf("took %s", time.Since(start))
	}
}

func TestCombine(t *testing.T) {
	d := t.TempDir()
	pre := func(dec, reason string) Hook {
		return script(t, d, PreToolUse, `echo '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"`+dec+`","permissionDecisionReason":"`+reason+`"}}'`)
	}
	run := func(hs ...Hook) Outcome {
		return Run(context.Background(), hs, PreToolUse, nil, Env{ProjectRoot: t.TempDir()}, nil)
	}
	o := run(pre("deny", "d1"), pre("ask", "a1"), pre("allow", "o1"), pre("deny", "d2"))
	if !o.Block || o.Ask || o.Allow || o.Reason != "d1\nd2" {
		t.Fatalf("deny = %+v", o)
	}
	o = run(pre("ask", "a1"), pre("allow", "o1"))
	if !o.Ask || o.Block || o.Allow || o.Reason != "a1" {
		t.Fatalf("ask = %+v", o)
	}
	o = run(pre("allow", "o1"))
	if !o.Allow || o.Ask || o.Block {
		t.Fatalf("allow = %+v", o)
	}
	upd := func(v string) Hook {
		return script(t, d, PreToolUse, `echo '{"hookSpecificOutput":{"hookEventName":"PreToolUse","updatedInput":`+v+`}}'`)
	}
	o = run(upd(`{"a":1}`), upd(`{ "a": 1 }`))
	if o.Conflict || string(o.UpdatedInput) == "" {
		t.Fatalf("same = %+v", o)
	}
	o = run(upd(`{"a":1}`), upd(`{"a":2}`))
	if !o.Conflict || o.UpdatedInput != nil {
		t.Fatalf("diff = %+v", o)
	}
	stop := script(t, d, PreToolUse, `echo '{"continue":false,"stopReason":"halt"}'`)
	o = run(pre("allow", "x"), stop)
	if !o.Stop || o.StopReason != "halt" {
		t.Fatalf("stop = %+v", o)
	}
}

type fakeEval struct {
	text string
	err  error
}

func (f fakeEval) Evaluate(context.Context, Hook, string) (string, error) { return f.text, f.err }

func TestPromptHook(t *testing.T) {
	h := Hook{ID: "p", Event: PreToolUse, Type: Prompt, Prompt: "check", Timeout: time.Second}
	run := func(text string, err error) Outcome {
		return Run(context.Background(), []Hook{h}, PreToolUse, nil, Env{ProjectRoot: t.TempDir()}, fakeEval{text, err})
	}
	o := run("```json\n{\"ok\":false,\"reason\":\"r\"}\n```", nil)
	if !o.Block || o.Reason != "r" {
		t.Fatalf("%+v", o)
	}
	o = run("nonsense", nil)
	if o.Block || o.Results[0].Status != StatusError {
		t.Fatalf("%+v", o)
	}
	o = run("", errors.New("boom"))
	if o.Results[0].Status != StatusError {
		t.Fatalf("%+v", o)
	}
	o = run(`{"ok":true}`, nil)
	if o.Block || o.Results[0].Status != StatusOK {
		t.Fatalf("%+v", o)
	}
	in := []byte(`{"a":1}`)
	if got := BuildPrompt("x $ARGUMENTS y", in); got != `x {"a":1} y` {
		t.Fatalf("got %q", got)
	}
	if got := BuildPrompt("x", in); got != "x\n\n"+string(in) {
		t.Fatalf("got %q", got)
	}
}
