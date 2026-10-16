package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func find(t *testing.T, root, name string) Tool {
	t.Helper()
	r := NewRegistry(FS(root, 30000)...)
	tl, ok := r.Get(name)
	if !ok {
		t.Fatalf("outil %s absent", name)
	}
	return tl
}

func run(t *testing.T, tl Tool, args string) Result {
	t.Helper()
	res, err := tl.Run(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadFileNumbersLines(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "f.txt"), "a\nb\n")
	res := run(t, find(t, root, "read_file"), `{"path":"f.txt"}`)
	if res.IsError || res.Content != "1\ta\n2\tb\n" {
		t.Fatalf("got %+v", res)
	}
}

func TestReadFileBinaryRejected(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "b.bin"), "ab\x00cd")
	res := run(t, find(t, root, "read_file"), `{"path":"b.bin"}`)
	if !res.IsError || !strings.Contains(res.Content, "binary file") {
		t.Fatalf("got %+v", res)
	}
}

func TestWriteFileHashes(t *testing.T) {
	root := t.TempDir()
	w := find(t, root, "write_file")
	r1 := run(t, w, `{"path":"d/x.txt","content":"un"}`)
	if r1.IsError || r1.HashBefore != "" || !strings.HasPrefix(r1.HashAfter, "sha256:") {
		t.Fatalf("got %+v", r1)
	}
	r2 := run(t, w, `{"path":"d/x.txt","content":"deux"}`)
	if r2.HashBefore != r1.HashAfter || r2.HashAfter == r1.HashAfter {
		t.Fatalf("got %+v / %+v", r1, r2)
	}
}

func TestEditFileUniqueMatch(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "e.txt")
	write(t, p, "foo bar")
	res := run(t, find(t, root, "edit_file"), `{"path":"e.txt","old_string":"bar","new_string":"baz"}`)
	b, _ := os.ReadFile(p)
	if res.IsError || string(b) != "foo baz" || res.HashBefore == "" || res.HashAfter == res.HashBefore {
		t.Fatalf("got %+v %q", res, b)
	}
}

func TestEditFileAmbiguous(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "e.txt")
	write(t, p, "x x")
	e := find(t, root, "edit_file")
	res := run(t, e, `{"path":"e.txt","old_string":"x","new_string":"y"}`)
	if !res.IsError || !strings.Contains(res.Content, "found 2 times") {
		t.Fatalf("got %+v", res)
	}
	res = run(t, e, `{"path":"e.txt","old_string":"x","new_string":"y","replace_all":true}`)
	b, _ := os.ReadFile(p)
	if res.IsError || string(b) != "y y" {
		t.Fatalf("got %+v %q", res, b)
	}
}

func TestEditFileNotFound(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "e.txt"), "abc")
	res := run(t, find(t, root, "edit_file"), `{"path":"e.txt","old_string":"zzz","new_string":"y"}`)
	if !res.IsError || !strings.Contains(res.Content, "text not found") {
		t.Fatalf("got %+v", res)
	}
}

func TestTargetResolvesRelativePath(t *testing.T) {
	root := t.TempDir()
	tg, err := find(t, root, "read_file").Target(json.RawMessage(`{"path":"x/y.go"}`))
	if err != nil || len(tg.Paths) != 1 || tg.Paths[0] != filepath.Join(root, "x/y.go") {
		t.Fatalf("got %+v %v", tg, err)
	}
}

func TestTargetInvalidArgs(t *testing.T) {
	root := t.TempDir()
	if _, err := find(t, root, "read_file").Target(json.RawMessage(`{"path":`)); err == nil {
		t.Fatal("erreur attendue")
	}
}

func TestTruncate(t *testing.T) {
	if Truncate("abc", 5) != "abc" {
		t.Fatal("court")
	}
	got := Truncate("abcdef", 3)
	if got != "abc\n[… output truncated to 3 characters]" {
		t.Fatalf("got %q", got)
	}
}

func TestRegistryDefsSorted(t *testing.T) {
	defs := NewRegistry(FS(t.TempDir(), 100)...).Defs()
	want := []string{"edit_file", "glob", "grep", "read_file", "write_file"}
	if len(defs) != len(want) {
		t.Fatalf("got %d", len(defs))
	}
	for i, d := range defs {
		if d.Name != want[i] || !json.Valid(d.Schema) {
			t.Fatalf("def %d: %+v", i, d)
		}
	}
}
