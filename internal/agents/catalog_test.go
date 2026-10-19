package agents

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/tenon-code-eu/tenon/internal/i18n"
)

func writeAgent(t *testing.T, dir, name, fm, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name+".md")
	if err := os.WriteFile(p, []byte("---\n"+fm+"\n---\n"+body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func valid(name string) string { return "name: " + name + "\ndescription: desc " + name }

func hasNotice(ns []Notice, sub string) int {
	n := 0
	for _, x := range ns {
		if strings.Contains(string(x), sub) {
			n++
		}
	}
	return n
}

func TestDirs(t *testing.T) {
	env := map[string]string{"HOME": "/h"}
	ds := Dirs(func(k string) string { return env[k] }, "/p")
	want := []Dir{
		{"/h/.config/tenon/agents", User}, {"/h/.claude/agents", User}, {"/h/.agents/agents", User},
		{"/p/.tenon/agents", Project}, {"/p/.claude/agents", Project}, {"/p/.agents/agents", Project},
	}
	if !reflect.DeepEqual(ds, want) {
		t.Fatalf("%+v", ds)
	}
}

func TestDiscoverPriority(t *testing.T) {
	u, p := t.TempDir(), t.TempDir()
	writeAgent(t, u, "review", valid("review"), "user")
	writeAgent(t, p, "review", valid("review"), "project")
	c, ns := Discover(i18n.EN, []Dir{{u, User}, {p, Project}})
	a, ok := c.Get("review")
	if !ok || a.Source != User || a.Body != "user" || hasNotice(ns, "review") != 1 {
		t.Fatalf("%+v %v", a, ns)
	}
}

func TestDiscoverClaudeCodeFile(t *testing.T) {
	r := t.TempDir()
	writeAgent(t, r, "reviewer", "name: reviewer\ndescription: Reviews code\ntools: Read, Grep, Bash(git:*), WebFetch\nmodel: sonnet\ncolor: red", "You review.\n")
	c, ns := Discover(i18n.EN, []Dir{{r, User}})
	a, _ := c.Get("reviewer")
	if !reflect.DeepEqual(a.Tools, []string{"read_file", "grep", "shell"}) || a.Model != "sonnet" || a.Body != "You review.\n" {
		t.Fatalf("%+v", a)
	}
	if len(ns) != 1 || hasNotice(ns, "WebFetch") != 1 {
		t.Fatalf("%v", ns)
	}
}

func TestDiscoverToolsForms(t *testing.T) {
	r := t.TempDir()
	writeAgent(t, r, "a", valid("a")+"\ntools: [Read, mcp__git__log]", "")
	writeAgent(t, r, "b", valid("b"), "")
	writeAgent(t, r, "c", valid("c")+"\ntools: Task", "")
	writeAgent(t, r, "d", valid("d")+"\ntools: Edit, MultiEdit", "")
	c, ns := Discover(i18n.EN, []Dir{{r, User}})
	get := func(n string) Agent { a, _ := c.Get(n); return a }
	if !reflect.DeepEqual(get("a").Tools, []string{"read_file", "mcp__git__log"}) {
		t.Fatalf("a %v", get("a").Tools)
	}
	if get("b").Tools != nil {
		t.Fatalf("b %v", get("b").Tools)
	}
	if tc := get("c").Tools; tc == nil || len(tc) != 0 || hasNotice(ns, "Task") != 1 {
		t.Fatalf("c %#v %v", tc, ns)
	}
	if !reflect.DeepEqual(get("d").Tools, []string{"edit_file"}) {
		t.Fatalf("d %v", get("d").Tools)
	}
}

func TestTranslateTools(t *testing.T) {
	tools, unk := TranslateTools([]string{"Read", "agent", "tenon", "shell", "Skill", "Glob", "Write"})
	if !reflect.DeepEqual(tools, []string{"read_file", "shell", "skill", "glob", "write_file"}) ||
		!reflect.DeepEqual(unk, []string{"agent", "tenon"}) {
		t.Fatalf("%v %v", tools, unk)
	}
}

func TestDiscoverInvalid(t *testing.T) {
	r := t.TempDir()
	writeAgent(t, r, "Bad", valid("Bad"), "")
	writeAgent(t, r, "nodesc", "name: nodesc", "")
	if err := os.WriteFile(filepath.Join(r, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, ns := Discover(i18n.EN, []Dir{{r, User}})
	if len(ns) != 2 {
		t.Fatalf("%v", ns)
	}
	if l := c.List(); len(l) != 1 || l[0].Name != GeneralPurpose {
		t.Fatalf("%+v", l)
	}
}

func TestDiscoverUnsafe(t *testing.T) {
	r := t.TempDir()
	// fichier projet en lien symbolique
	pd := filepath.Join(r, "proj")
	real := writeAgent(t, filepath.Join(r, "real"), "target", valid("target"), "")
	if err := os.MkdirAll(pd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(pd, "target.md")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(pd, "fifo.md"), 0o644); err != nil {
		t.Fatal(err)
	}
	// base projet en lien symbolique, base utilisateur suivie
	if err := os.Symlink(filepath.Join(r, "real"), filepath.Join(r, "plink")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(r, "real"), filepath.Join(r, "ulink")); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var c *Catalog
	var ns []Notice
	go func() {
		c, ns = Discover(i18n.EN, []Dir{{pd, Project}, {filepath.Join(r, "plink"), Project}})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("blocked on FIFO")
	}
	if _, ok := c.Get("target"); ok || len(c.List()) != 1 || hasNotice(ns, "symbolic link") != 1 {
		t.Fatalf("%+v %v", c.List(), ns)
	}
	c, _ = Discover(i18n.EN, []Dir{{filepath.Join(r, "ulink"), User}})
	if _, ok := c.Get("target"); !ok {
		t.Fatal("user symlinked base should be followed")
	}
}

func TestDiscoverLimits(t *testing.T) {
	r := t.TempDir()
	for i := 0; i < 101; i++ {
		n := "a" + string(rune('a'+i/26)) + string(rune('a'+i%26))
		writeAgent(t, r, n, valid(n), "")
	}
	c, ns := Discover(i18n.EN, []Dir{{r, User}})
	files := 0
	for _, a := range c.List() {
		if a.Source == User {
			files++
		}
	}
	if files != MaxAgents || hasNotice(ns, "limit") != 1 {
		t.Fatalf("%d %v", files, ns)
	}

	r2 := t.TempDir()
	writeAgent(t, r2, "big", valid("big"), strings.Repeat("é", 20000))
	c2, _ := Discover(i18n.EN, []Dir{{r2, User}})
	a, _ := c2.Get("big")
	if !a.Truncated || !strings.HasSuffix(a.Body, "é") || strings.ContainsRune(a.Body, '�') {
		t.Fatalf("trunc=%v len=%d", a.Truncated, len(a.Body))
	}
}

func TestBuiltin(t *testing.T) {
	c, ns := Discover(i18n.FR, nil)
	l := c.List()
	if len(ns) != 0 || len(l) != 1 || l[0].Name != GeneralPurpose || l[0].Source != Builtin || l[0].Tools != nil ||
		l[0].Description != i18n.T(i18n.FR, "agents.general_purpose") {
		t.Fatalf("%+v %v", l, ns)
	}
	if tot, pr := c.FileCount(); tot != 0 || pr != 0 {
		t.Fatalf("%d %d", tot, pr)
	}
	r := t.TempDir()
	writeAgent(t, r, GeneralPurpose, valid(GeneralPurpose), "mine")
	c, ns = Discover(i18n.EN, []Dir{{r, User}})
	a, _ := c.Get(GeneralPurpose)
	if len(ns) != 0 || len(c.List()) != 1 || a.Source != User || a.Body != "mine" {
		t.Fatalf("%+v %v", a, ns)
	}
	if tot, _ := c.FileCount(); tot != 1 {
		t.Fatal(tot)
	}
}

func TestListOrder(t *testing.T) {
	r := t.TempDir()
	writeAgent(t, r, "zeta", valid("zeta"), "")
	c, _ := Discover(i18n.EN, []Dir{{r, User}})
	l := c.List()
	if len(l) != 2 || l[0].Name != GeneralPurpose || l[1].Name != "zeta" {
		t.Fatalf("%+v", l)
	}
}

func TestDiscoverClaudeCodeRealistic(t *testing.T) {
	r := t.TempDir()
	desc := "Use this agent when reviewing. " + strings.Repeat("é", 2470) +
		" <example>\\nuser: review\\nassistant: ok</example>"
	fm := "name: code-reviewer\ndescription: \"" + desc + "\"\ntools: Read, Grep\nmodel: sonnet\ncolor: red"
	for i := 0; i < 55; i++ {
		fm += "\n# commentaire " + strings.Repeat("c", 100)
	}
	p := writeAgent(t, r, "code-reviewer", fm, "You review.\n")
	c, ns := Discover(i18n.EN, []Dir{{r, User}})
	a, ok := c.Get("code-reviewer")
	if !ok || len([]rune(a.Description)) != 1024 || a.Body != "You review.\n" {
		t.Fatalf("%v %d %v", ok, len([]rune(a.Description)), ns)
	}
	want := `agent "code-reviewer" in ` + p + `: description truncated to 1024 characters`
	if hasNotice(ns, want) != 1 || len(ns) != 1 {
		t.Fatalf("%v", ns)
	}
}

func TestDiscoverNameIndependentOfFile(t *testing.T) {
	r := t.TempDir()
	writeAgent(t, r, "foo", valid("bar"), "")
	c, ns := Discover(i18n.EN, []Dir{{r, User}})
	if _, ok := c.Get("bar"); !ok || len(ns) != 0 {
		t.Fatalf("%v", ns)
	}
	if _, ok := c.Get("foo"); ok {
		t.Fatal("foo ne doit pas exister")
	}
}

func TestDiscoverFrontmatterTooLarge(t *testing.T) {
	r := t.TempDir()
	writeAgent(t, r, "big", valid("big")+"\nnotes: "+strings.Repeat("x", 20000), "")
	c, ns := Discover(i18n.EN, []Dir{{r, User}})
	if _, ok := c.Get("big"); ok || hasNotice(ns, "frontmatter too large") != 1 {
		t.Fatalf("%v", ns)
	}
}

func TestDiscoverTruncatedNotice(t *testing.T) {
	r := t.TempDir()
	p := writeAgent(t, r, "big", valid("big"), strings.Repeat("a", 40000))
	_, ns := Discover(i18n.EN, []Dir{{r, User}})
	if hasNotice(ns, `agent "big" in `+p+`: file truncated to 32768 bytes`) != 1 {
		t.Fatalf("%v", ns)
	}
}
