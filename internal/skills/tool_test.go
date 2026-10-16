package skills

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tenon-code-eu/tenon/internal/policy"
	"github.com/tenon-code-eu/tenon/internal/tools"
)

func toolFixture(t *testing.T) (tools.Tool, string) {
	t.Helper()
	r := t.TempDir()
	dir := writeSkill(t, r, "review", valid("review"), "Corps du skill\n")
	if err := os.MkdirAll(filepath.Join(dir, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "references", "api.md"), []byte("API doc"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeSkill(t, r, "deploy", valid("deploy"), "x")
	c, _ := Discover([]Dir{{r, User}})
	s, _ := c.Get("review")
	return NewTool(c, 10000), s.Dir
}

func run(t *testing.T, tl tools.Tool, args string) tools.Result {
	t.Helper()
	res, err := tl.Run(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestToolLoad(t *testing.T) {
	tl, dir := toolFixture(t)
	res := run(t, tl, `{"name":"review"}`)
	if res.IsError {
		t.Fatal(res.Content)
	}
	if !strings.HasPrefix(res.Content, "Skill review (user, "+dir+")\n\n") {
		t.Fatalf("content: %q", res.Content)
	}
	if !strings.Contains(res.Content, "Corps du skill") || !strings.Contains(res.Content, "Files:\n- SKILL.md") {
		t.Fatalf("content: %q", res.Content)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	h := sha256.Sum256(data)
	sum := hex.EncodeToString(h[:])
	if res.Path != filepath.Join(dir, "SKILL.md") || res.HashAfter != sum {
		t.Fatalf("path %q hash %q want %q", res.Path, res.HashAfter, sum)
	}
}

func TestToolFile(t *testing.T) {
	tl, dir := toolFixture(t)
	res := run(t, tl, `{"name":"review","file":"references/api.md"}`)
	if res.IsError || res.Content != "API doc" {
		t.Fatalf("%+v", res)
	}
	if res.Path != filepath.Join(dir, "references", "api.md") || res.HashAfter == "" {
		t.Fatalf("%+v", res)
	}
}

func TestToolErrors(t *testing.T) {
	tl, _ := toolFixture(t)
	for _, a := range []string{
		`{"name":"nope"}`,
		`{"name":"review","file":"../../etc/passwd"}`,
		`{"name":"review","file":"/etc/passwd"}`,
		`{"name":""}`,
		`{bad`,
	} {
		if res := run(t, tl, a); !res.IsError {
			t.Errorf("%s: want error, got %+v", a, res)
		}
	}
	if _, err := tl.Target(json.RawMessage(`{"name":""}`)); err == nil {
		t.Error("Target: want error")
	}
}

func TestToolPermissions(t *testing.T) {
	tl, _ := toolFixture(t)
	p, err := policy.NewPermissions(t.TempDir(), []policy.Rule{{Tool: "skill", Match: "deploy*", Decision: policy.Deny, Project: true}})
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]policy.Decision{"deploy": policy.Deny, "review": policy.Allow} {
		tg, err := tl.Target(json.RawMessage(`{"name":"` + name + `"}`))
		if err != nil {
			t.Fatal(err)
		}
		if got := p.Check(policy.Call{Tool: "skill", Risk: tl.Risk(), Target: tg}); got != want {
			t.Errorf("%s: got %v want %v", name, got, want)
		}
	}
}
