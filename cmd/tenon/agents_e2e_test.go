package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tenon-code-eu/tenon/internal/event"
)

const reviewerAgent = "---\nname: reviewer\ndescription: Relit le code.\ntools: Read, Grep\nmodel: sonnet\n---\nTu es un relecteur severe.\n"

func subagentEnv(t *testing.T, f *fakeLLM) *env {
	t.Helper()
	e := newEnv(t, f, "eu")
	e.writeFile(filepath.Join(e.wd, ".claude", "agents", "reviewer.md"), reviewerAgent)
	return e
}

func TestAgentsListCommand(t *testing.T) {
	e := subagentEnv(t, nil)
	code, out, errb := e.run("agents")
	if code != 0 || errb != "" {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	for _, want := range []string{"general-purpose  builtin", "reviewer  project  ", "tools: read_file, grep", "model: sonnet"} {
		if !strings.Contains(out, want) {
			t.Fatalf("%q absent : %q", want, out)
		}
	}
	if code, _, _ := e.run("agents", "--lang", "fr"); code != 0 {
		t.Fatalf("--lang fr : code=%d", code)
	}
	if code, _, _ := e.run("--resume", "last", "agents"); code != 0 {
		t.Fatalf("--resume : code=%d", code)
	}
	if code, _, errb := e.run("agents", "extra"); code != 1 || !strings.Contains(errb, "extra") {
		t.Fatalf("extra : code=%d stderr=%q", code, errb)
	}
}

func TestAgentsListSanitised(t *testing.T) {
	e := newEnv(t, nil, "eu")
	e.writeFile(filepath.Join(e.wd, ".claude", "agents", "evil.md"),
		"---\nname: evil\ndescription: joli \x1b[31mrouge \u202efin\n---\ncorps\n")
	code, out, _ := e.run("agents")
	if code != 0 || !strings.Contains(out, "evil  project") || strings.ContainsAny(out, "\x1b\u202e") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestSubagentEndToEnd(t *testing.T) {
	f := newFakeLLM(t,
		sseCall("agent", `{"agent":"reviewer","description":"review","prompt":"read go.mod"}`),
		sseCall("read_file", `{"path":"a.txt"}`),
		sseText("sub answer"),
		sseText("final answer"),
	)
	e := subagentEnv(t, f)
	e.writeFile(filepath.Join(e.wd, "a.txt"), "contenu\n")
	code, out, errb := e.run("-p", "lance le relecteur")
	if code != 0 || out != "final answer\n" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errb)
	}
	reqs := f.requests()
	if len(reqs) != 4 {
		t.Fatalf("requêtes : %d", len(reqs))
	}
	if !strings.Contains(reqs[1], "Tu es un relecteur severe.") || strings.Contains(reqs[1], "lance le relecteur") {
		t.Fatalf("requête du sous-agent : %q", reqs[1])
	}
	if code, out, errb := e.run("audit", "verify"); code != 0 {
		t.Fatalf("audit verify : code=%d stdout=%q stderr=%q", code, out, errb)
	}

	f2 := newFakeLLM(t,
		sseCall("agent", `{"agent":"reviewer","description":"review","prompt":"read go.mod"}`),
		sseCall("read_file", `{"path":"a.txt"}`),
		sseText("sub answer"),
		sseText("final answer"),
	)
	e2 := subagentEnv(t, f2)
	e2.writeFile(filepath.Join(e2.wd, "a.txt"), "contenu\n")
	code, out, errb = e2.run("-p", "lance le relecteur", "--output", "json")
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	var started, nested, completed bool
	for _, ev := range decodeEvents(t, out) {
		switch ev.Type {
		case event.SubagentStarted:
			started = true
		case event.ToolResult:
			var p event.ToolResultPayload
			if json.Unmarshal(ev.Payload, &p) == nil && p.ParentCallID != "" {
				nested = true
			}
		case event.SubagentCompleted:
			var p event.SubagentCompletedPayload
			if json.Unmarshal(ev.Payload, &p) == nil && p.Status == "ok" {
				completed = true
			}
		}
	}
	if !started || !nested || !completed {
		t.Fatalf("événements : started=%v nested=%v completed=%v", started, nested, completed)
	}
}

func TestSubagentBanner(t *testing.T) {
	f := newFakeLLM(t, sseText("ok"))
	e := subagentEnv(t, f)
	if code, _, errb := e.run("-p", "salut", "--lang", "en"); code != 0 || !strings.Contains(errb, "agents: 1 (project: 1)") {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	f2 := newFakeLLM(t, sseText("ok"))
	e2 := newEnv(t, f2, "eu")
	if code, _, errb := e2.run("-p", "salut", "--lang", "en"); code != 0 || strings.Contains(errb, "agents:") {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
}

func TestAgentsListToolsNoneAndTruncated(t *testing.T) {
	e := newEnv(t, nil, "eu")
	e.writeFile(filepath.Join(e.wd, ".claude", "agents", "empty.md"),
		"---\nname: empty\ndescription: Sans outils.\ntools:\n---\ncorps\n")
	e.writeFile(filepath.Join(e.wd, ".claude", "agents", "big.md"),
		"---\nname: big\ndescription: Gros.\n---\n"+strings.Repeat("a", 40000))
	code, out, _ := e.run("agents")
	if code != 0 {
		t.Fatalf("code=%d", code)
	}
	var emptyLine, bigLine string
	for _, l := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(l, "empty  "):
			emptyLine = l
		case strings.HasPrefix(l, "big  "):
			bigLine = l
		}
	}
	if !strings.Contains(emptyLine, "tools: none") || strings.Contains(emptyLine, "tools: all") {
		t.Fatalf("empty : %q", emptyLine)
	}
	if !strings.Contains(bigLine, "big.md (truncated)  ") || !strings.Contains(out, "file truncated to 32768 bytes") {
		t.Fatalf("big : %q\n%s", bigLine, out)
	}
}
