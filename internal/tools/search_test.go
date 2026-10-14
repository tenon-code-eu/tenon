package tools

import (
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
