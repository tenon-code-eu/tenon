package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"
)

func writeSkill(t *testing.T, root, name, fm, body string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\n"+fm+"\n---\n"+body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func valid(name string) string { return "name: " + name + "\ndescription: desc " + name }

// userOnly renvoie les skills non intégrés ; stripBuiltin renvoie un
// catalogue sans eux (les anciens tests ne portent que sur les dossiers).
func userOnly(c *Catalog) []Skill {
	var out []Skill
	for _, s := range c.List() {
		if s.Source != Builtin {
			out = append(out, s)
		}
	}
	return out
}

func stripBuiltin(c *Catalog) *Catalog {
	n := &Catalog{byName: map[string]int{}}
	for _, s := range userOnly(c) {
		n.byName[s.Name] = len(n.skills)
		n.skills = append(n.skills, s)
	}
	return n
}

func TestDiscoverPriority(t *testing.T) {
	u, p, ut, uc := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	writeSkill(t, u, "review", valid("review"), "user")
	writeSkill(t, p, "review", valid("review"), "project")
	c, notices := Discover([]Dir{{u, User}, {p, Project}})
	s, ok := c.Get("review")
	if !ok || s.Source != User {
		t.Fatalf("got %+v", s)
	}
	if len(notices) != 1 || !strings.Contains(string(notices[0]), "review") {
		t.Fatalf("notices: %v", notices)
	}
	writeSkill(t, ut, "x", valid("x"), "tenon")
	writeSkill(t, uc, "x", valid("x"), "claude")
	c, _ = Discover([]Dir{{ut, User}, {uc, User}})
	s, _ = c.Get("x")
	if s.Dir != filepath.Join(ut, "x") {
		t.Fatalf("got %s", s.Dir)
	}
}

func TestDiscoverInvalid(t *testing.T) {
	r := t.TempDir()
	writeSkill(t, r, "ok", valid("ok"), "")
	writeSkill(t, r, "folder", valid("other"), "")
	writeSkill(t, r, "Bad_Name", valid("Bad_Name"), "")
	writeSkill(t, r, "long", "name: long\ndescription: "+strings.Repeat("a", 1025), "")
	if err := os.MkdirAll(filepath.Join(r, "broken"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(r, "broken", "SKILL.md"), []byte("no frontmatter"), 0o644)
	c, notices := Discover([]Dir{{r, User}})
	if l := userOnly(c); len(l) != 1 || l[0].Name != "ok" || !l[0].ModelInvocable {
		t.Fatalf("list: %+v", l)
	}
	if len(notices) != 4 {
		t.Fatalf("notices: %v", notices)
	}
}

func TestDiscoverUnsafe(t *testing.T) {
	r, out := t.TempDir(), t.TempDir()
	writeSkill(t, out, "target", valid("target"), "")
	if err := os.Symlink(filepath.Join(out, "target"), filepath.Join(r, "target")); err != nil {
		t.Fatal(err)
	}
	writeSkill(t, out, "lnk", valid("lnk"), "")
	os.MkdirAll(filepath.Join(r, "lnk"), 0o755)
	if err := os.Symlink(filepath.Join(out, "lnk", "SKILL.md"), filepath.Join(r, "lnk", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(r, "fifo"), 0o755)
	if err := syscall.Mkfifo(filepath.Join(r, "fifo", "SKILL.md"), 0o644); err != nil {
		t.Fatal(err)
	}
	done := make(chan *Catalog, 1)
	go func() { c, _ := Discover([]Dir{{r, Project}}); done <- c }()
	select {
	case c := <-done:
		if l := userOnly(c); len(l) != 0 {
			t.Fatalf("list: %+v", l)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Discover blocked on FIFO")
	}
}

func TestDiscoverLimits(t *testing.T) {
	r := t.TempDir()
	for i := 0; i < 201; i++ {
		n := fmt.Sprintf("s%03d", i)
		writeSkill(t, r, n, valid(n), "")
	}
	c, notices := Discover([]Dir{{r, User}})
	if len(c.List()) != 200 || len(notices) != 1 {
		t.Fatalf("%d skills, notices %v", len(c.List()), notices)
	}

	r2 := t.TempDir()
	// "é" fait 2 octets : la coupe tombe au milieu d'une rune
	writeSkill(t, r2, "big", valid("big"), strings.Repeat("é", 20000))
	c, _ = Discover([]Dir{{r2, User}})
	body, _, trunc, err := c.Load("big")
	if err != nil || !trunc || len(body) > MaxSkillBytes || !utf8.ValidString(body) {
		t.Fatalf("err=%v trunc=%v len=%d valid=%v", err, trunc, len(body), utf8.ValidString(body))
	}
}

func TestLoadRereads(t *testing.T) {
	r := t.TempDir()
	dir := writeSkill(t, r, "a", valid("a"), "one")
	c, _ := Discover([]Dir{{r, User}})
	b1, h1, trunc, err := c.Load("a")
	if err != nil || b1 != "one" || trunc {
		t.Fatalf("%q %v %v", b1, trunc, err)
	}
	writeSkill(t, r, "a", valid("a"), "two")
	b2, h2, _, err := c.Load("a")
	if err != nil || b2 != "two" || h1 == h2 {
		t.Fatalf("%q %s %s %v", b2, h1, h2, err)
	}
	os.Remove(filepath.Join(dir, "SKILL.md"))
	if _, _, _, err := c.Load("a"); err == nil {
		t.Fatal("expected error")
	}
	if _, _, _, err := c.Load("nope"); err == nil {
		t.Fatal("expected error")
	}
}

func TestDirs(t *testing.T) {
	env := map[string]string{"XDG_CONFIG_HOME": "/x", "HOME": "/h"}
	d := Dirs(func(k string) string { return env[k] }, "/root")
	want := []string{"/x/tenon/skills", "/h/.claude/skills", "/h/.agents/skills", "/root/.tenon/skills", "/root/.claude/skills", "/root/.agents/skills"}
	if len(d) != 6 {
		t.Fatal(d)
	}
	for i, w := range want {
		if d[i].Path != w {
			t.Errorf("%d: %s != %s", i, d[i].Path, w)
		}
	}
	if d[0].Source != User || d[3].Source != Project {
		t.Error("sources")
	}
	delete(env, "XDG_CONFIG_HOME")
	if p := Dirs(func(k string) string { return env[k] }, "/root")[0].Path; p != "/h/.config/tenon/skills" {
		t.Error(p)
	}
}

func TestDirsUnsafeBases(t *testing.T) {
	get := func(env map[string]string) []Dir {
		return Dirs(func(k string) string { return env[k] }, "/root")
	}
	if d := get(map[string]string{}); len(d) != 3 || d[0].Source != Project {
		t.Errorf("HOME vide: %+v", d)
	}
	d := get(map[string]string{"HOME": "/h", "XDG_CONFIG_HOME": "rel"})
	if len(d) != 6 || d[0].Path != "/h/.config/tenon/skills" {
		t.Errorf("XDG relatif: %+v", d)
	}
	if d := get(map[string]string{"XDG_CONFIG_HOME": "rel"}); len(d) != 3 {
		t.Errorf("XDG relatif sans HOME: %+v", d)
	}
}

func TestLoadRefusesSymlinkedFolder(t *testing.T) {
	r, out := t.TempDir(), t.TempDir()
	dir := writeSkill(t, r, "a", valid("a"), "one")
	c, _ := Discover([]Dir{{r, User}})
	writeSkill(t, out, "a", valid("a"), "evil")
	os.RemoveAll(dir)
	if err := os.Symlink(filepath.Join(out, "a"), dir); err != nil {
		t.Fatal(err)
	}
	if body, _, _, err := c.Load("a"); err == nil {
		t.Fatalf("expected error, got %q", body)
	}
}

func TestDiscoverSkipsSymlinkedBase(t *testing.T) {
	out, proj := t.TempDir(), t.TempDir()
	writeSkill(t, out, "x", valid("x"), "")
	if err := os.Symlink(out, filepath.Join(proj, "skills")); err != nil {
		t.Fatal(err)
	}
	c, notices := Discover([]Dir{{filepath.Join(proj, "skills"), Project}})
	if len(userOnly(c)) != 0 {
		t.Fatalf("list: %+v", userOnly(c))
	}
	if len(notices) != 1 || !strings.Contains(string(notices[0]), "symbolic link") {
		t.Fatalf("notices: %v", notices)
	}
}

// Une base utilisateur reliée (dotfiles) est suivie.
func TestDiscoverFollowsSymlinkedUserBase(t *testing.T) {
	out, home := t.TempDir(), t.TempDir()
	writeSkill(t, out, "x", valid("x"), "")
	if err := os.Symlink(out, filepath.Join(home, "skills")); err != nil {
		t.Fatal(err)
	}
	c, notices := Discover([]Dir{{filepath.Join(home, "skills"), User}})
	if s, ok := c.Get("x"); !ok || s.Source != User || len(notices) != 0 {
		t.Fatalf("skill %+v ok=%v notices %v", s, ok, notices)
	}
}

func TestBuiltinSkillsPresent(t *testing.T) {
	c, notices := Discover(nil)
	if len(notices) != 0 {
		t.Fatalf("notices: %v", notices)
	}
	for _, n := range []string{"cra-sbom", "cra-triage", "cra-report", "cra-docs"} {
		s, ok := c.Get(n)
		if !ok || s.Source != Builtin || s.Description == "" {
			t.Fatalf("%s: %+v ok=%v", n, s, ok)
		}
		body, sum, trunc, err := c.Load(n)
		if err != nil || strings.TrimSpace(body) == "" || len(sum) != 64 || trunc {
			t.Fatalf("%s: err=%v sum=%q trunc=%v", n, err, sum, trunc)
		}
		if !strings.Contains(body, "tenon cra") {
			t.Fatalf("%s: no tenon cra command", n)
		}
		if files, _, err := c.Files(n); err != nil || len(files) != 1 || files[0] != "SKILL.md" {
			t.Fatalf("%s: files %v err %v", n, files, err)
		}
		if _, _, _, err := c.ReadFile(n, "other.md", 0); err == nil {
			t.Fatalf("%s: annex read must fail", n)
		}
	}
}

func TestBuiltinOverridden(t *testing.T) {
	u := t.TempDir()
	writeSkill(t, u, "cra-triage", valid("cra-triage"), "mine")
	c, notices := Discover([]Dir{{u, User}})
	if len(notices) != 0 {
		t.Fatalf("notices: %v", notices)
	}
	s, _ := c.Get("cra-triage")
	if s.Source != User {
		t.Fatalf("got %+v", s)
	}
	n := 0
	for _, s := range c.List() {
		if s.Name == "cra-triage" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("listed %d times", n)
	}
	if body, _, _, err := c.Load("cra-triage"); err != nil || !strings.Contains(body, "mine") {
		t.Fatalf("body %q err %v", body, err)
	}
}
