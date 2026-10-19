package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tenon-code-eu/tenon/internal/engine"
	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/hooks"
	"github.com/tenon-code-eu/tenon/internal/i18n"
)

func startHook(t *testing.T, tr *Transcript, run string) {
	tr.Apply(ev(t, event.HookStarted, event.HookStartedPayload{RunID: run, HookID: "h1", Event: "PreToolUse", Type: "command", Command: "/opt/hooks/check.sh --x"}))
}

func TestHookSilentOK(t *testing.T) {
	tr := NewTranscript(i18n.EN)
	startHook(t, tr, "r1")
	tr.Apply(ev(t, event.HookCompleted, event.HookCompletedPayload{RunID: "r1", Event: "PreToolUse", Status: "ok"}))
	if n := len(tr.Items()); n != 0 {
		t.Fatalf("items = %+v", tr.Items())
	}
}

func TestHookBlockedLine(t *testing.T) {
	tr := NewTranscript(i18n.EN)
	startHook(t, tr, "r1")
	tr.Apply(ev(t, event.HookCompleted, event.HookCompletedPayload{RunID: "r1", Event: "PreToolUse", Status: "blocked", Reason: "no"}))
	it := tr.Items()
	if len(it) != 1 || it[0].Kind != ItemNotice || !strings.Contains(it[0].Text, "hook PreToolUse · check.sh [blocked] no") {
		t.Fatalf("items = %+v", it)
	}
}

func TestHookStatuses(t *testing.T) {
	tr := NewTranscript(i18n.FR)
	startHook(t, tr, "r1")
	tr.Apply(ev(t, event.HookCompleted, event.HookCompletedPayload{RunID: "r1", Event: "PreToolUse", Status: "ok", Decision: "ask", Reason: "sure ?"}))
	tr.Apply(ev(t, event.HookCompleted, event.HookCompletedPayload{RunID: "r2", Event: "Stop", Status: "timeout", Error: "slow"}))
	it := tr.Items()
	if len(it) != 2 || !strings.Contains(it[0].Text, "[demandé] sure ?") || !strings.Contains(it[1].Text, "[délai dépassé] slow") {
		t.Fatalf("items = %+v", it)
	}
}

func TestHookReasonCleanedAndRedacted(t *testing.T) {
	tr := NewTranscript(i18n.EN)
	tr.SetRedact(func(s string) string { return strings.ReplaceAll(s, "SECRET", "***") })
	startHook(t, tr, "r1")
	tr.Apply(ev(t, event.HookCompleted, event.HookCompletedPayload{RunID: "r1", Event: "PreToolUse", Status: "error", Error: "a\x1b[31mb SECRET"}))
	it := tr.Items()
	if len(it) != 1 || strings.ContainsRune(it[0].Text, 0x1b) || !strings.Contains(it[0].Text, "[error] a [31mb ***") {
		t.Fatalf("items = %+v", it)
	}
}

func TestHookSystemMessageAndStopReason(t *testing.T) {
	tr := NewTranscript(i18n.EN)
	tr.Apply(ev(t, event.HookCompleted, event.HookCompletedPayload{RunID: "r1", Event: "PostToolUse", Status: "ok", SystemMessage: "hello\nworld", Stop: true, StopReason: "enough"}))
	it := tr.Items()
	if len(it) != 2 || it[0].Text != "hello world" || it[1].Text != "enough" || it[0].Kind != ItemNotice {
		t.Fatalf("items = %+v", it)
	}
}

func TestUserMessageHookIsNotice(t *testing.T) {
	tr := NewTranscript(i18n.EN)
	tr.Apply(ev(t, event.UserMessage, event.UserMessagePayload{Text: "keep\x1bgoing", Hook: "Stop"}))
	it := tr.Items()
	if len(it) != 1 || it[0].Kind != ItemNotice || it[0].Text != "keep going" || !tr.Busy() {
		t.Fatalf("items = %+v", it)
	}
}

func TestUserMessageBlockedIsUser(t *testing.T) {
	tr := NewTranscript(i18n.EN)
	tr.Apply(ev(t, event.UserMessage, event.UserMessagePayload{Text: "hi", Blocked: true}))
	if it := tr.Items(); len(it) != 1 || it[0].Kind != ItemUser {
		t.Fatalf("items = %+v", it)
	}
}

func TestUserMessageSubagentFeedbackSkipped(t *testing.T) {
	tr := NewTranscript(i18n.EN)
	tr.Apply(ev(t, event.UserMessage, event.UserMessagePayload{Text: "more", Hook: "SubagentStop", ParentCallID: "c1"}))
	if it := tr.Items(); len(it) != 0 {
		t.Fatalf("items = %+v", it)
	}
}

func hookPerm() event.PermissionRequestedPayload {
	p := shellPerm("p1")
	p.HookReason = "needs a human"
	return p
}

func TestPermissionHookReasonNoSession(t *testing.T) {
	f := newFake()
	h := newHarness(t, f, Options{Lang: i18n.EN})
	askPerm(t, h, f, hookPerm())
	v := h.m.View()
	if !strings.Contains(v, "hook: needs a human") {
		t.Fatalf("vue sans la raison : %q", v)
	}
	if strings.Contains(v, i18n.T(i18n.EN, "tui.perm.keys_tool")) || !strings.Contains(v, i18n.T(i18n.EN, "tui.perm.keys_hook")) {
		t.Fatalf("touches : %q", v)
	}
	h.send(runeKey('a'))
	if _, ok := f.reply("p1"); ok {
		t.Fatal("« pour la session » accepté malgré hook_reason")
	}
	quiet(h)
	h.send(runeKey('y'))
	wantReply(t, f, "p1", engine.Reply{Allow: true})
}

func TestHooksCommandEmpty(t *testing.T) {
	for lang, want := range map[i18n.Lang]string{i18n.EN: "no hooks", i18n.FR: "aucun hook"} {
		h := newHarness(t, newFake(), Options{Lang: lang})
		typeText(h, "/hooks")
		enter(h)
		if n := notices(h); n != want {
			t.Fatalf("avis %q", n)
		}
	}
}

func TestHooksCommandList(t *testing.T) {
	home := t.TempDir()
	file := filepath.Join(home, ".config", "tenon", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(`{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"echo  a\nb"}]}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	getenv := func(k string) string {
		if k == "HOME" {
			return home
		}
		return ""
	}
	set, _, err := hooks.Load(hooks.Options{Getenv: getenv, ProjectRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, newFake(), Options{Lang: i18n.EN, Hooks: set})
	typeText(h, "/hooks")
	enter(h)
	n := notices(h)
	if !strings.Contains(n, "  Bash  command  echo a b") {
		t.Fatalf("avis %q", n)
	}
}

func TestHooksCompletion(t *testing.T) {
	h := newHarness(t, newFake(), Options{Lang: i18n.EN})
	typeText(h, "/ho")
	h.m.complete()
	if v := h.m.input.Value(); v != "/hooks " {
		t.Fatalf("complétion %q", v)
	}
}
