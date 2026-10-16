package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func filesCatalog(t *testing.T) (*Catalog, string) {
	t.Helper()
	r := t.TempDir()
	dir := writeSkill(t, r, "demo", valid("demo"), "body")
	c, _ := Discover([]Dir{{r, User}})
	return c, dir
}

func put(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadFileOK(t *testing.T) {
	c, dir := filesCatalog(t)
	put(t, filepath.Join(dir, "references", "api.md"), "# API\n")
	got, abs, sum, err := c.ReadFile("demo", "references/api.md", 1000)
	if err != nil || got != "# API\n" {
		t.Fatalf("got %q err %v", got, err)
	}
	if abs != filepath.Join(dir, "references", "api.md") {
		t.Fatalf("abs %s", abs)
	}
	h := sha256.Sum256([]byte("# API\n"))
	if sum != hex.EncodeToString(h[:]) {
		t.Fatalf("sha %s", sum)
	}
}

func TestReadFileRefused(t *testing.T) {
	c, dir := filesCatalog(t)
	out := t.TempDir()
	put(t, filepath.Join(out, "secret.md"), "s")
	if err := os.Symlink(out, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(dir, "real.md"), "r")
	os.Symlink(filepath.Join(dir, "real.md"), filepath.Join(dir, "alias.md"))
	put(t, filepath.Join(dir, ".env"), "K=V")
	put(t, filepath.Join(dir, "keys", "server.pem"), "k")
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ name, rel, want string }{
		{"demo", "", "empty path"},
		{"demo", "/etc/passwd", "escapes"},
		{"demo", "../x", "escapes"},
		{"demo", "references/../../x", "escapes"},
		{"demo", "link/secret.md", "symbolic link"},
		{"demo", "alias.md", "symbolic link"},
		{"demo", ".env", "sensitive"},
		{"demo", "keys/server.pem", "sensitive"},
		{"demo", "pipe", "not a regular file"},
		{"demo", "keys", "not a regular file"},
		{"demo", "SKILL.md/x", "not a directory"},
		{"demo", "missing.md", "not found"},
		{"nope", "SKILL.md", `unknown skill "nope"`},
	}
	for _, tc := range cases {
		_, _, _, err := c.ReadFile(tc.name, tc.rel, 100)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q: err %v, want %q", tc.rel, err, tc.want)
		}
	}
}

func TestReadFileSkillFolderSymlink(t *testing.T) {
	c, dir := filesCatalog(t)
	moved := dir + "-moved"
	os.Rename(dir, moved)
	os.Symlink(moved, dir)
	if _, _, _, err := c.ReadFile("demo", "SKILL.md", 100); err == nil || !strings.Contains(err.Error(), "real directory") {
		t.Fatalf("err %v", err)
	}
	if _, _, err := c.Files("demo"); err == nil {
		t.Fatal("Files must refuse")
	}
}

func TestReadFileLimit(t *testing.T) {
	c, dir := filesCatalog(t)
	put(t, filepath.Join(dir, "big.txt"), strings.Repeat("é", 100*1024/2))
	got, _, sum, err := c.ReadFile("demo", "big.txt", 1000)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(got, "\n[… output truncated to 1000 characters]") || len([]rune(got)) < 1000 {
		t.Fatalf("got %d runes, tail %q", len([]rune(got)), got[len(got)-50:])
	}
	if len(sum) != 64 {
		t.Fatalf("sha %q", sum)
	}
}

func TestFiles(t *testing.T) {
	c, dir := filesCatalog(t)
	put(t, filepath.Join(dir, "a", "b", "c.md"), "")
	put(t, filepath.Join(dir, "a", "b", "d", "e.md"), "")
	put(t, filepath.Join(dir, "a", "b", "d", "f", "g.md"), "")
	os.Symlink(t.TempDir(), filepath.Join(dir, "lnk"))
	os.Symlink(filepath.Join(dir, "SKILL.md"), filepath.Join(dir, "alias.md"))
	got, tr, err := c.Files("demo")
	if err != nil || tr {
		t.Fatalf("err %v tr %v", err, tr)
	}
	want := "SKILL.md,a/b/c.md"
	if strings.Join(got, ",") != want {
		t.Fatalf("got %v want %s", got, want)
	}
}

func TestFilesCap(t *testing.T) {
	c, dir := filesCatalog(t)
	for i := 0; i < 150; i++ {
		put(t, filepath.Join(dir, "refs", fmt.Sprintf("f%03d.md", i)), "")
	}
	got, tr, err := c.Files("demo")
	if err != nil || !tr || len(got) != 100 {
		t.Fatalf("len %d tr %v err %v", len(got), tr, err)
	}
	if _, _, err := c.Files("nope"); err == nil {
		t.Fatal("unknown skill")
	}
}
