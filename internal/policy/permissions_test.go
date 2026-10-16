package policy

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tenon-code-eu/tenon/internal/tools"
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
		{{Tool: "shell", Match: "*", Decision: Allow}, {Tool: "shell", Match: "rm -rf *", Decision: Deny}},
		{{Tool: "shell", Match: "rm -rf *", Decision: Deny}, {Tool: "shell", Match: "*", Decision: Allow}},
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
	p, _ := newPerms(t, []Rule{{Tool: "shell", Match: "go test *", Decision: Allow}})
	if got := p.Check(shellCall("go test ./...")); got != Allow {
		t.Errorf("got %v", got)
	}
	if got := p.Check(shellCall("go build")); got != Ask {
		t.Errorf("got %v", got)
	}
}

func TestDotDotEscapeIsAsk(t *testing.T) {
	p, root := newPerms(t, []Rule{{Tool: "read_file", Match: "*", Decision: Allow}})
	if got := p.Check(readCall(filepath.Join(root, "..", "secret"))); got != Ask {
		t.Errorf("got %v", got)
	}
}

func TestSymlinkEscapeIsAsk(t *testing.T) {
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "hosts"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	p, root := newPerms(t, []Rule{{Tool: "*", Match: "*", Decision: Allow}})
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
	p, _ := newPerms(t, []Rule{{Tool: "read_file", Match: "*", Decision: Allow}})
	outside := filepath.Join(t.TempDir(), "x")
	if got := p.Check(readCall(outside)); got != Ask {
		t.Errorf("got %v", got)
	}
	if got := p.Check(readCall("/etc/hosts")); got != Ask {
		t.Errorf("got %v", got)
	}
}

func TestSensitiveFilesAsk(t *testing.T) {
	p, root := newPerms(t, []Rule{{Tool: "*", Match: "*", Decision: Allow}})
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
	p, root := newPerms(t, []Rule{{Tool: "*", Match: "secrets/*", Decision: Deny}})
	c := Call{Tool: "write_file", Risk: tools.Write, Target: tools.Target{Paths: []string{filepath.Join(root, "secrets", "a")}}}
	if got := p.Check(c); got != Deny {
		t.Errorf("got %v", got)
	}
}

func TestDenyMatchesThroughSymlink(t *testing.T) {
	p, root := newPerms(t, []Rule{{Tool: "*", Match: "secrets/*", Decision: Deny}})
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

func TestDenyCaseInsensitive(t *testing.T) {
	p, root := newPerms(t, []Rule{{Tool: "*", Match: "*", Decision: Allow}, {Tool: "*", Match: "secrets/*", Decision: Deny}})
	for _, name := range []string{"Secrets/k", "SECRETS/k", "secrets/k"} {
		c := Call{Tool: "write_file", Risk: tools.Write, Target: tools.Target{Paths: []string{filepath.Join(root, name)}}}
		if got := p.Check(c); got != Deny {
			t.Errorf("%s: got %v", name, got)
		}
	}
}

func TestAllowMustMatchEveryPath(t *testing.T) {
	p, root := newPerms(t, []Rule{{Tool: "write_file", Match: "src/*", Decision: Allow}})
	mk := func(names ...string) Call {
		var ps []string
		for _, n := range names {
			ps = append(ps, filepath.Join(root, n))
		}
		return Call{Tool: "write_file", Risk: tools.Write, Target: tools.Target{Paths: ps}}
	}
	if got := p.Check(mk("src/a", "Makefile")); got != Ask {
		t.Errorf("mixed: got %v", got)
	}
	if got := p.Check(mk("src/a", "src/b")); got != Allow {
		t.Errorf("all match: got %v", got)
	}
}

func TestEscapeRegressions(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "proj")
	other := filepath.Join(base, "project2")
	for _, d := range []string{root, other, filepath.Join(base, "real")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// link1 -> link2 -> real (outside root)
	if err := os.Symlink(filepath.Join(base, "real"), filepath.Join(root, "link2")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "link2"), filepath.Join(root, "link1")); err != nil {
		t.Fatal(err)
	}
	p, err := NewPermissions(root, []Rule{{Tool: "*", Match: "*", Decision: Allow}})
	if err != nil {
		t.Fatal(err)
	}
	// relative path is joined to root: ../proj/a lands back inside root.
	if got := p.Check(readCall("../proj/a")); got != Allow {
		t.Errorf("relative ../proj/a (inside): got %v", got)
	}
	for name, path := range map[string]string{
		"prefix collision":    filepath.Join(other, "x"),
		"relative escape":     "../project2/x",
		"trailing slash link": filepath.Join(root, "link2") + "/",
		"link chain child":    filepath.Join(root, "link1", "nope", "x"),
	} {
		if got := p.Check(readCall(path)); got != Ask {
			t.Errorf("%s: read got %v", name, got)
		}
	}
}

func TestAskFiresOnAnyPath(t *testing.T) {
	p, root := newPerms(t, []Rule{{Tool: "*", Match: "secret-dir/*", Decision: Ask}})
	c := readCall(filepath.Join(root, "a.txt"), filepath.Join(root, "secret-dir", "b"))
	if got := p.Check(c); got != Ask {
		t.Errorf("got %v", got)
	}
}

func TestAllowShellRejectsChaining(t *testing.T) {
	p, _ := newPerms(t, []Rule{{Tool: "shell", Match: "go test *", Decision: Allow}})
	for _, cmd := range []string{
		"go test ./... ; touch pwned",
		"go test ./... && rm x",
		"go test ./... || rm x",
		"go test ./... | sh",
		"go test $(evil)",
		"go test `evil`",
		"go test > /etc/x",
		"go test < /etc/x",
		"go test ./...\ntouch pwned",
	} {
		if d := p.Check(shellCall(cmd)); d != Ask {
			t.Errorf("%q : %v, want ask", cmd, d)
		}
	}
	if d := p.Check(shellCall("go test ./...")); d != Allow {
		t.Fatalf("go test ./... : %v", d)
	}
	// Un opérateur présent littéralement dans le motif reste autorisé.
	p, _ = newPerms(t, []Rule{{Tool: "shell", Match: "go test * | tee *", Decision: Allow}})
	if d := p.Check(shellCall("go test ./... | tee out.txt")); d != Allow {
		t.Fatalf("pipe littéral : %v", d)
	}
	// Deny inchangé.
	p, _ = newPerms(t, []Rule{{Tool: "shell", Match: "rm *", Decision: Deny}})
	if d := p.Check(shellCall("rm -rf x; ls")); d != Deny {
		t.Fatalf("deny : %v", d)
	}
}

// ForceAsk impose Ask quelle que soit la règle allow ; seule une règle deny
// l'emporte.
func TestForceAskOverridesAllow(t *testing.T) {
	forced := Call{Tool: "mcp__gh__issue", Risk: tools.Read, Target: tools.Target{Command: "mcp gh.issue {}", ForceAsk: true}}
	p, _ := newPerms(t, []Rule{{Tool: "*", Match: "", Decision: Allow}})
	if got := p.Check(forced); got != Ask {
		t.Errorf("allow * + ForceAsk : got %v want Ask", got)
	}
	p, _ = newPerms(t, nil)
	if got := p.Check(forced); got != Ask {
		t.Errorf("lecture par défaut + ForceAsk : got %v want Ask", got)
	}
	p, _ = newPerms(t, []Rule{{Tool: "*", Match: "", Decision: Allow}, {Tool: "mcp__gh__issue", Match: "", Decision: Deny}})
	if got := p.Check(forced); got != Deny {
		t.Errorf("deny + ForceAsk : got %v want Deny", got)
	}
}

// Une règle ask du projet l'emporte sur une règle allow globale : le projet ne
// peut que restreindre. Une ask globale reste, elle, primée par une allow.
func TestProjectAskOverridesGlobalAllow(t *testing.T) {
	p, root := newPerms(t, []Rule{
		{Tool: "shell", Match: "*", Decision: Allow},
		{Tool: "write_file", Match: "*", Decision: Allow},
		{Tool: "shell", Match: "git push*", Decision: Ask, Project: true},
		{Tool: "*", Match: "secret-dir/*", Decision: Ask, Project: true},
	})
	if got := p.Check(shellCall("git push origin main")); got != Ask {
		t.Errorf("git push: got %v want Ask", got)
	}
	if got := p.Check(shellCall("ls")); got != Allow {
		t.Errorf("ls: got %v want Allow", got)
	}
	w := func(rel string) Call {
		return Call{Tool: "write_file", Risk: tools.Write, Target: tools.Target{Paths: []string{filepath.Join(root, rel)}}}
	}
	if got := p.Check(w("secret-dir/a.txt")); got != Ask {
		t.Errorf("write secret-dir: got %v want Ask", got)
	}
	if got := p.Check(w("src/a.txt")); got != Allow {
		t.Errorf("write src: got %v want Allow", got)
	}

	// Ask globale : l'allow globale l'emporte (comportement inchangé).
	g, _ := newPerms(t, []Rule{
		{Tool: "shell", Match: "*", Decision: Allow},
		{Tool: "shell", Match: "git push*", Decision: Ask},
	})
	if got := g.Check(shellCall("git push")); got != Allow {
		t.Errorf("global ask: got %v want Allow", got)
	}

	// Deny reste prioritaire sur une ask projet.
	d, _ := newPerms(t, []Rule{
		{Tool: "shell", Match: "git push*", Decision: Ask, Project: true},
		{Tool: "shell", Match: "git push*", Decision: Deny},
	})
	if got := d.Check(shellCall("git push")); got != Deny {
		t.Errorf("deny: got %v want Deny", got)
	}
}
