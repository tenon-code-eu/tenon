package hooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tenon-code-eu/tenon/internal/config"
)

// env construit un HOME temporaire et un getenv associé.
func env(t *testing.T) (home string, getenv func(string) string) {
	t.Helper()
	home = t.TempDir()
	return home, func(k string) string {
		if k == "HOME" {
			return home
		}
		return ""
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func tenonFile(home string) string  { return filepath.Join(home, ".config", "tenon", "hooks.json") }
func claudeFile(home string) string { return filepath.Join(home, ".claude", "settings.json") }

func TestLoadTenonFile(t *testing.T) {
	home, g := env(t)
	write(t, tenonFile(home), `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"echo hi"}]}]}}`)
	s, notices, err := Load(Options{Getenv: g, ProjectRoot: t.TempDir()})
	if err != nil || len(notices) != 0 {
		t.Fatalf("err=%v notices=%v", err, notices)
	}
	l := s.List()
	if len(l) != 1 {
		t.Fatalf("hooks = %v", l)
	}
	h := l[0]
	if h.ID != "tenon:PreToolUse:0.0" || h.Timeout != 60*time.Second || h.Source != SourceTenon ||
		h.Type != Command || h.Command != "echo hi" || h.Matcher != "Bash" {
		t.Fatalf("hook = %+v", h)
	}
}

func TestLoadClaudeOptIn(t *testing.T) {
	home, g := env(t)
	write(t, claudeFile(home), `{"model":"x","hooks":{"Stop":[{"hooks":[{"type":"command","command":"a"}]}]}}`)
	s, _, err := Load(Options{Getenv: g, ProjectRoot: t.TempDir()})
	if err != nil || len(s.List()) != 0 {
		t.Fatalf("err=%v hooks=%v", err, s.List())
	}
	s, _, err = Load(Options{Getenv: g, ProjectRoot: t.TempDir(), Config: config.Hooks{ClaudeSettings: true}})
	if err != nil {
		t.Fatal(err)
	}
	l := s.List()
	if len(l) != 1 || l[0].Source != SourceClaude || l[0].ID != "claude:Stop:0.0" {
		t.Fatalf("hooks = %+v", l)
	}
	if total, cl := s.Count(); total != 1 || cl != 1 {
		t.Fatalf("count = %d, %d", total, cl)
	}
}

func TestLoadDisabled(t *testing.T) {
	home, g := env(t)
	write(t, tenonFile(home), `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"a"}]}]}}`)
	s, _, err := Load(Options{Getenv: g, ProjectRoot: t.TempDir(), Config: config.Hooks{Disabled: true, ClaudeSettings: true}})
	if err != nil || len(s.List()) != 0 {
		t.Fatalf("err=%v hooks=%v", err, s.List())
	}
}

func TestLoadDisableAllHooks(t *testing.T) {
	home, g := env(t)
	write(t, tenonFile(home), `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"a"}]}]}}`)
	write(t, claudeFile(home), `{"disableAllHooks":true,"hooks":{"Stop":[{"hooks":[{"type":"command","command":"b"}]}]}}`)
	s, _, err := Load(Options{Getenv: g, ProjectRoot: t.TempDir(), Config: config.Hooks{ClaudeSettings: true}})
	if err != nil {
		t.Fatal(err)
	}
	l := s.List()
	if len(l) != 1 || l[0].Source != SourceTenon {
		t.Fatalf("hooks = %+v", l)
	}
}

func TestLoadProjectIgnored(t *testing.T) {
	_, g := env(t)
	root := t.TempDir()
	const body = `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"a"}]}]}}`
	write(t, filepath.Join(root, ".claude", "settings.json"), body)
	write(t, filepath.Join(root, ".tenon", "hooks.json"), body)
	s, notices, err := Load(Options{Getenv: g, ProjectRoot: root})
	if err != nil || len(s.List()) != 0 {
		t.Fatalf("err=%v hooks=%v", err, s.List())
	}
	if len(notices) != 2 {
		t.Fatalf("notices = %v", notices)
	}
	for _, n := range notices {
		if !strings.Contains(string(n), "project hooks ignored") {
			t.Fatalf("notice = %q", n)
		}
	}

	root = t.TempDir()
	write(t, filepath.Join(root, ".claude", "settings.json"), `{"model":"x"}`)
	_, notices, err = Load(Options{Getenv: g, ProjectRoot: root})
	if err != nil || len(notices) != 0 {
		t.Fatalf("err=%v notices=%v", err, notices)
	}

	root = t.TempDir()
	write(t, filepath.Join(root, ".claude", "settings.local.json"), `{broken`)
	_, notices, err = Load(Options{Getenv: g, ProjectRoot: root})
	if err != nil || len(notices) != 1 {
		t.Fatalf("err=%v notices=%v", err, notices)
	}
}

func TestLoadFailClosed(t *testing.T) {
	many := strings.Repeat(`{"type":"command","command":"a"},`, MaxHooks) + `{"type":"command","command":"a"}`
	cases := []struct{ name, body, want string }{
		{"invalid json", `{nope`, "hooks.json"},
		{"hooks array", `{"hooks":[]}`, "hooks.json: hooks"},
		{"bad regex", `{"hooks":{"Stop":[{"matcher":"(","hooks":[{"type":"command","command":"a"}]}]}}`, "hooks.Stop[0].matcher"},
		{"empty command", `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":""}]}]}}`, "hooks.Stop[0].hooks[0]"},
		{"timeout zero", `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"a","timeout":0}]}]}}`, "hooks.Stop[0].hooks[0]: timeout must be a positive integer"},
		{"timeout float", `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"a"},{"type":"command","command":"a","timeout":1.5}]}]}}`, "hooks.Stop[0].hooks[1]: timeout must be a positive integer"},
		{"too large", `{"x":"` + strings.Repeat("a", MaxFileBytes) + `"}`, "hooks.json: file too large"},
		{"too many", `{"hooks":{"Stop":[{"hooks":[` + many + `]}]}}`, "hooks.json: too many hooks"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home, g := env(t)
			write(t, tenonFile(home), c.body)
			_, _, err := Load(Options{Getenv: g, ProjectRoot: t.TempDir()})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
		})
	}
}

func TestLoadUnknown(t *testing.T) {
	home, g := env(t)
	write(t, tenonFile(home), `{"hooks":{
		"PostToolUseFailure":[{"hooks":[{"type":"command","command":"x"}]}],
		"Stop":[{"hooks":[{"type":"http","command":"y"},{"type":"command","command":"z"}]}]}}`)
	s, notices, err := Load(Options{Getenv: g, ProjectRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if len(notices) != 2 {
		t.Fatalf("notices = %v", notices)
	}
	l := s.List()
	if len(l) != 1 || l[0].Command != "z" || l[0].ID != "tenon:Stop:0.1" {
		t.Fatalf("hooks = %+v", l)
	}
}

func TestLoadRelativeHome(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "rel", ".config", "tenon", "hooks.json"), `{broken`)
	write(t, filepath.Join(dir, "rel", ".claude", "settings.json"), `{broken`)
	t.Chdir(dir)
	g := func(k string) string {
		if k == "HOME" {
			return "rel"
		}
		return ""
	}
	s, _, err := Load(Options{Getenv: g, ProjectRoot: t.TempDir(), Config: config.Hooks{ClaudeSettings: true}})
	if err != nil || len(s.List()) != 0 {
		t.Fatalf("err=%v hooks=%v", err, s.List())
	}
}

func TestMatch(t *testing.T) {
	home, g := env(t)
	write(t, tenonFile(home), `{"hooks":{
	"PreToolUse":[
		{"matcher":"Edit|Write","hooks":[{"type":"command","command":"ew"}]},
		{"matcher":"edit_file","hooks":[{"type":"command","command":"ef"}]},
		{"matcher":"*","hooks":[{"type":"command","command":"star"}]},
		{"hooks":[{"type":"command","command":"star"},{"type":"command","command":"none"}]}],
	"SessionStart":[{"matcher":"resume","hooks":[{"type":"command","command":"r"}]}],
	"Stop":[{"matcher":"whatever","hooks":[{"type":"command","command":"s"}]}]}}`)
	s, _, err := Load(Options{Getenv: g, ProjectRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	cmds := func(hs []Hook) string {
		var o []string
		for _, h := range hs {
			o = append(o, h.Command)
		}
		return strings.Join(o, ",")
	}
	for _, c := range []struct {
		ev   Event
		subj []string
		want string
	}{
		{PreToolUse, []string{"Edit", "edit_file"}, "ew,ef,star,none"},
		{PreToolUse, []string{"Write", "write_file"}, "ew,star,none"},
		{PreToolUse, []string{"MultiEdit", "x"}, "star,none"},
		{SessionStart, []string{"resume"}, "r"},
		{SessionStart, []string{"startup"}, ""},
		{Stop, nil, "s"},
	} {
		if got := cmds(s.Match(c.ev, c.subj...)); got != c.want {
			t.Errorf("Match(%s,%v) = %q, want %q", c.ev, c.subj, got, c.want)
		}
	}
	var nilSet *Set
	if nilSet.Match(Stop) != nil {
		t.Fatal("nil set must match nothing")
	}
}
