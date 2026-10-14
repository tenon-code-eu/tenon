package policy

import (
	"os"
	"path/filepath"
	"testing"

	"codeberg.org/tenon/tenon/internal/tools"
)

func newPerms(t *testing.T, rules []Rule) (*Permissions, string) {
	t.Helper()
	root := t.TempDir()
	p, err := NewPermissions(root, rules)
	if err != nil {
		t.Fatal(err)
	}
	return p, root
}

func readCall(paths ...string) Call {
	return Call{Tool: "read_file", Risk: tools.Read, Target: tools.Target{Paths: paths}}
}

func shellCall(cmd string) Call {
	return Call{Tool: "shell", Risk: tools.Exec, Target: tools.Target{Command: cmd}}
}

func TestDefaultsByRisk(t *testing.T) {
	p, root := newPerms(t, nil)
	tests := []struct {
		name string
		c    Call
		want Decision
	}{
		{"read in project", readCall(filepath.Join(root, "a.txt")), Allow},
		{"write", Call{Tool: "write_file", Risk: tools.Write, Target: tools.Target{Paths: []string{filepath.Join(root, "a.txt")}}}, Ask},
		{"shell", shellCall("ls"), Ask},
		{"network", Call{Tool: "fetch", Risk: tools.Network}, Ask},
	}
	for _, tt := range tests {
		if got := p.Check(tt.c); got != tt.want {
			t.Errorf("%s: got %v want %v", tt.name, got, tt.want)
		}
	}
}

func TestDenyWinsOverAllow(t *testing.T) {
	for _, rules := range [][]Rule{
		{{"shell", "*", Allow}, {"shell", "rm -rf *", Deny}},
		{{"shell", "rm -rf *", Deny}, {"shell", "*", Allow}},
	} {
		p, _ := newPerms(t, rules)
		if got := p.Check(shellCall("rm -rf /")); got != Deny {
			t.Errorf("got %v want Deny", got)
		}
		if got := p.Check(shellCall("ls")); got != Allow {
			t.Errorf("got %v want Allow", got)
		}
	}
}

func TestAllowShellPattern(t *testing.T) {
	p, _ := newPerms(t, []Rule{{"shell", "go test *", Allow}})
	if got := p.Check(shellCall("go test ./...")); got != Allow {
		t.Errorf("got %v", got)
	}
	if got := p.Check(shellCall("go build")); got != Ask {
		t.Errorf("got %v", got)
	}
}

func TestDotDotEscapeIsAsk(t *testing.T) {
	p, root := newPerms(t, []Rule{{"read_file", "*", Allow}})
	if got := p.Check(readCall(filepath.Join(root, "..", "secret"))); got != Ask {
		t.Errorf("got %v", got)
	}
}

func TestSymlinkEscapeIsAsk(t *testing.T) {
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "hosts"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	p, root := newPerms(t, []Rule{{"*", "*", Allow}})
	if err := os.Symlink(outside, filepath.Join(root, "etc")); err != nil {
		t.Fatal(err)
	}
	// dangling link to a not-yet-existing outside file
	if err := os.Symlink(filepath.Join(outside, "new"), filepath.Join(root, "dangling")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		filepath.Join(root, "etc", "hosts"),
		filepath.Join(root, "etc", "not-yet-created"),
		filepath.Join(root, "etc"),
		filepath.Join(root, "dangling"),
	} {
		if got := p.Check(readCall(path)); got != Ask {
			t.Errorf("read %s: got %v", path, got)
		}
		w := Call{Tool: "write_file", Risk: tools.Write, Target: tools.Target{Paths: []string{path}}}
		if got := p.Check(w); got != Ask {
			t.Errorf("write %s: got %v", path, got)
		}
	}
}

func TestAllowRuleCannotEscapeRoot(t *testing.T) {
	p, _ := newPerms(t, []Rule{{"read_file", "*", Allow}})
	outside := filepath.Join(t.TempDir(), "x")
	if got := p.Check(readCall(outside)); got != Ask {
		t.Errorf("got %v", got)
	}
	if got := p.Check(readCall("/etc/hosts")); got != Ask {
		t.Errorf("got %v", got)
	}
}

func TestSensitiveFilesAsk(t *testing.T) {
	p, root := newPerms(t, []Rule{{"*", "*", Allow}})
	for _, name := range []string{".env", ".env.local", "cert.pem", "id_rsa", "id_rsa.pub", "sub/.ENV"} {
		if got := p.Check(readCall(filepath.Join(root, name))); got != Ask {
			t.Errorf("%s: got %v", name, got)
		}
	}
	// sensitive via symlink target basename
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("k"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, ".env"), filepath.Join(root, "innocent.txt")); err != nil {
		t.Fatal(err)
	}
	if got := p.Check(readCall(filepath.Join(root, "innocent.txt"))); got != Ask {
		t.Errorf("symlink to .env: got %v", got)
	}
}

func TestWildcardToolDeny(t *testing.T) {
	p, root := newPerms(t, []Rule{{"*", "secrets/*", Deny}})
	c := Call{Tool: "write_file", Risk: tools.Write, Target: tools.Target{Paths: []string{filepath.Join(root, "secrets", "a")}}}
	if got := p.Check(c); got != Deny {
		t.Errorf("got %v", got)
	}
}

func TestDenyMatchesThroughSymlink(t *testing.T) {
	p, root := newPerms(t, []Rule{{"*", "secrets/*", Deny}})
	if err := os.Mkdir(filepath.Join(root, "secrets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "secrets"), filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	if got := p.Check(readCall(filepath.Join(root, "alias", "a"))); got != Deny {
		t.Errorf("got %v", got)
	}
}

func TestParseDecision(t *testing.T) {
	for s, want := range map[string]Decision{"allow": Allow, "ask": Ask, "deny": Deny} {
		got, err := ParseDecision(s)
		if err != nil || got != want {
			t.Errorf("%q: %v %v", s, got, err)
		}
	}
	for _, s := range []string{"", "Allow", "ALLOW", " ask", "nope"} {
		if _, err := ParseDecision(s); err == nil {
			t.Errorf("%q: want error", s)
		}
	}
}

func TestMatchWildcard(t *testing.T) {
	tests := []struct {
		pat, s string
		want   bool
	}{
		{"*", "", true},
		{"*", "anything at/all", true},
		{"go test *", "go test ./...", true},
		{"go test *", "go build", false},
		{"rm -rf *", "rm -rf /", true},
		{"a*b*c", "aXXbYYc", true},
		{"a*b*c", "acb", false},
		{"a*b", "ab", true},
		{"a*a", "a", false},
		{"abc", "abc", true},
		{"abc", "abcd", false},
		{"secrets/*", "secrets/a/b", true},
		{"", "", true},
		{"", "x", false},
		{"a.c", "abc", false},
	}
	for _, tt := range tests {
		if got := matchWildcard(tt.pat, tt.s); got != tt.want {
			t.Errorf("matchWildcard(%q,%q)=%v want %v", tt.pat, tt.s, got, tt.want)
		}
	}
}
