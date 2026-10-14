package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGlobSkipsGit(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a/b.go"), "x")
	write(t, filepath.Join(root, "c.go"), "x")
	write(t, filepath.Join(root, ".git/z.go"), "x")
	res := run(t, find(t, root, "glob"), `{"pattern":"**/*.go"}`)
	if res.IsError || res.Content != "a/b.go\nc.go" {
		t.Fatalf("got %+v", res)
	}
}

func TestGrepSkipsBinary(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a.txt"), "x\nhello\n")
	write(t, filepath.Join(root, "b.bin"), "hello\x00")
	write(t, filepath.Join(root, ".git/c"), "hello")
	res := run(t, find(t, root, "grep"), `{"pattern":"hel+o"}`)
	if res.IsError || strings.TrimSpace(res.Content) != "a.txt:2:hello" {
		t.Fatalf("got %+v", res)
	}
	res = run(t, find(t, root, "grep"), `{"pattern":"("}`)
	if !res.IsError {
		t.Fatal("regexp invalide attendue en erreur")
	}
}

func TestGlobDoesNotFollowSymlinks(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	write(t, filepath.Join(outside, "secret.txt"), "s")
	write(t, filepath.Join(root, "ok.txt"), "x")
	if err := os.Symlink(outside, filepath.Join(root, "out")); err != nil {
		t.Fatal(err)
	}
	for _, pat := range []string{"**", "out/**", "out/*"} {
		res := run(t, find(t, root, "glob"), `{"pattern":"`+pat+`"}`)
		if strings.Contains(res.Content, "secret.txt") {
			t.Fatalf("%s: fuite %q", pat, res.Content)
		}
	}
}

func TestGrepSymlinkedRoot(t *testing.T) {
	real := t.TempDir()
	write(t, filepath.Join(real, "a.txt"), "hello\n")
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	res := run(t, find(t, link, "grep"), `{"pattern":"hello"}`)
	if strings.TrimSpace(res.Content) != "a.txt:1:hello" {
		t.Fatalf("got %+v", res)
	}
}
