package diff

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func mkArgs(t *testing.T, v map[string]any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestLinesSimple(t *testing.T) {
	got := Lines("a\nb\nc", "a\nX\nc")
	want := []Line{{Same, "a"}, {Del, "b"}, {Add, "X"}, {Same, "c"}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func numbered(n int) []string {
	var s []string
	for i := 1; i <= n; i++ {
		s = append(s, fmt.Sprintf("l%d", i))
	}
	return s
}

func TestUnifiedContext(t *testing.T) {
	a := numbered(20)
	b := numbered(20)
	b[9] = "CHANGED"
	got := Unified(strings.Join(a, "\n"), strings.Join(b, "\n"), 3)
	want := []Line{
		{Same, "…"}, {Same, "l7"}, {Same, "l8"}, {Same, "l9"},
		{Del, "l10"}, {Add, "CHANGED"},
		{Same, "l11"}, {Same, "l12"}, {Same, "l13"}, {Same, "…"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestUnifiedSeparatesBlocks(t *testing.T) {
	a := numbered(30)
	b := numbered(30)
	b[1] = "X"
	b[27] = "Y"
	got := Unified(strings.Join(a, "\n"), strings.Join(b, "\n"), 2)
	n := 0
	for _, l := range got {
		if l.Kind == Same && l.Text == "…" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("want 1 ellipsis between blocks, got %d: %v", n, got)
	}
}

func TestPreviewWriteNewFile(t *testing.T) {
	root := t.TempDir()
	args := mkArgs(t, map[string]any{"path": "new.txt", "content": "a\nb\n"})
	lines, more, err := Preview("write_file", args, root)
	if err != nil || more != 0 {
		t.Fatalf("err=%v more=%d", err, more)
	}
	want := []Line{{Add, "a"}, {Add, "b"}}
	if fmt.Sprint(lines) != fmt.Sprint(want) {
		t.Fatalf("got %v", lines)
	}
}

func TestPreviewWriteExisting(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte("a\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args := mkArgs(t, map[string]any{"path": "f.txt", "content": "a\nc\n"})
	lines, _, err := Preview("write_file", args, root)
	if err != nil {
		t.Fatal(err)
	}
	want := []Line{{Same, "a"}, {Del, "b"}, {Add, "c"}}
	if fmt.Sprint(lines) != fmt.Sprint(want) {
		t.Fatalf("got %v", lines)
	}
}

func TestPreviewEdit(t *testing.T) {
	args := mkArgs(t, map[string]any{"path": "x", "old_string": "foo\nbar", "new_string": "foo\nbaz", "replace_all": true})
	lines, _, err := Preview("edit_file", args, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	want := []Line{{Same, "foo"}, {Del, "bar"}, {Add, "baz"}}
	if fmt.Sprint(lines) != fmt.Sprint(want) {
		t.Fatalf("got %v", lines)
	}
}

func TestBinaryAndHuge(t *testing.T) {
	root := t.TempDir()
	args := mkArgs(t, map[string]any{"path": "b", "content": "a\x00b"})
	lines, _, err := Preview("write_file", args, root)
	if !errors.Is(err, ErrBinary) || lines != nil {
		t.Fatalf("lines=%v err=%v", lines, err)
	}
	// fichier existant binaire
	if err := os.WriteFile(filepath.Join(root, "bin"), []byte("x\x00y"), 0o644); err != nil {
		t.Fatal(err)
	}
	args = mkArgs(t, map[string]any{"path": "bin", "content": "texte"})
	if _, _, err := Preview("write_file", args, root); !errors.Is(err, ErrBinary) {
		t.Fatalf("existing binary: %v", err)
	}
	args = mkArgs(t, map[string]any{"path": "x", "old_string": "a\x00", "new_string": "b"})
	if _, _, err := Preview("edit_file", args, root); !errors.Is(err, ErrBinary) {
		t.Fatalf("edit binary: %v", err)
	}

	var a, b []string
	for i := 0; i < 5000; i++ {
		a = append(a, fmt.Sprintf("old%d", i))
		b = append(b, fmt.Sprintf("new%d", i))
	}
	if err := os.WriteFile(filepath.Join(root, "big"), []byte(strings.Join(a, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	args = mkArgs(t, map[string]any{"path": "big", "content": strings.Join(b, "\n")})
	start := time.Now()
	lines, more, err := Preview("write_file", args, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != MaxLines || more <= 0 {
		t.Fatalf("len=%d more=%d", len(lines), more)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("trop lent: %v", time.Since(start))
	}
}

func TestPreviewOtherTool(t *testing.T) {
	lines, more, err := Preview("shell", json.RawMessage(`{"command":"ls"}`), t.TempDir())
	if lines != nil || more != 0 || err != nil {
		t.Fatalf("got %v %d %v", lines, more, err)
	}
}

func TestPreviewInvalidArgs(t *testing.T) {
	if _, _, err := Preview("write_file", json.RawMessage(`{`), t.TempDir()); err == nil {
		t.Fatal("want error")
	}
}

func TestPreviewNonRegularIgnored(t *testing.T) {
	root := t.TempDir()
	// un répertoire n'est pas un fichier régulier : "avant" vide
	if err := os.Mkdir(filepath.Join(root, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	args := mkArgs(t, map[string]any{"path": "d", "content": "a"})
	lines, _, err := Preview("write_file", args, root)
	if err != nil || len(lines) != 1 || lines[0].Kind != Add {
		t.Fatalf("got %v %v", lines, err)
	}
}

func TestPreviewHugeFile(t *testing.T) {
	root := t.TempDir()
	// > 1 MiB
	if err := os.WriteFile(filepath.Join(root, "h"), []byte(strings.Repeat("x\n", 600*1024)), 0o644); err != nil {
		t.Fatal(err)
	}
	args := mkArgs(t, map[string]any{"path": "h", "content": "y\n"})
	lines, more, err := Preview("write_file", args, root)
	if err != nil || len(lines) != MaxLines || more <= 0 || lines[0].Kind != Del {
		t.Fatalf("len=%d more=%d err=%v", len(lines), more, err)
	}
}

func TestPreviewSymlinkFollowed(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "real.txt"), []byte("a\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real.txt", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	args := mkArgs(t, map[string]any{"path": "link", "content": "a\nX\n"})
	lines, _, err := Preview("write_file", args, root)
	if err != nil {
		t.Fatal(err)
	}
	want := []Line{{Same, "a"}, {Del, "b"}, {Add, "X"}}
	if fmt.Sprint(lines) != fmt.Sprint(want) {
		t.Fatalf("got %v", lines)
	}
}
