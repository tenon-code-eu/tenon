package tools

import (
	"context"
	"encoding/json"
	"errors"
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
	if got != "ab\n[… 3 characters omitted …]\nf" {
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

// findWith renvoie l'outil name construit avec opts.
func findWith(t *testing.T, root, name string, opts ...Option) Tool {
	t.Helper()
	r := NewRegistry(FS(root, 30000, opts...)...)
	tl, ok := r.Get(name)
	if !ok {
		t.Fatalf("outil %s absent", name)
	}
	return tl
}

// snapRecorder enregistre les appels de save et l'état du fichier au moment de l'appel.
type snapRecorder struct {
	calls   int
	path    string
	old     []byte
	seenNow []byte
	err     error
}

func (s *snapRecorder) save(p string, old []byte) error {
	s.calls++
	s.path = p
	s.old = append([]byte(nil), old...)
	if data, err := os.ReadFile(p); err == nil {
		s.seenNow = data
	}
	return s.err
}

func TestSnapshotWrite(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "w.txt")
	write(t, p, "ancien")
	rec := &snapRecorder{}
	res := run(t, findWith(t, root, "write_file", WithSnapshot(rec.save)), `{"path":"w.txt","content":"nouveau"}`)
	if res.IsError || res.NoCheckpoint {
		t.Fatalf("got %+v", res)
	}
	if rec.calls != 1 || rec.path != p || string(rec.old) != "ancien" {
		t.Fatalf("save: calls=%d path=%q old=%q", rec.calls, rec.path, rec.old)
	}
	if string(rec.seenNow) != "ancien" {
		t.Fatalf("save appelé après l'écriture : fichier vu %q", rec.seenNow)
	}
	if data, _ := os.ReadFile(p); string(data) != "nouveau" {
		t.Fatalf("fichier = %q", data)
	}
}

func TestSnapshotEdit(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "e.txt")
	write(t, p, "foo bar")
	rec := &snapRecorder{}
	res := run(t, findWith(t, root, "edit_file", WithSnapshot(rec.save)), `{"path":"e.txt","old_string":"foo","new_string":"baz"}`)
	if res.IsError || res.NoCheckpoint {
		t.Fatalf("got %+v", res)
	}
	if rec.calls != 1 || rec.path != p || string(rec.old) != "foo bar" {
		t.Fatalf("save: calls=%d path=%q old=%q", rec.calls, rec.path, rec.old)
	}
	if string(rec.seenNow) != "foo bar" {
		t.Fatalf("save appelé après l'écriture : fichier vu %q", rec.seenNow)
	}
	if data, _ := os.ReadFile(p); string(data) != "baz bar" {
		t.Fatalf("fichier = %q", data)
	}
}

func TestSnapshotNewFile(t *testing.T) {
	root := t.TempDir()
	rec := &snapRecorder{}
	res := run(t, findWith(t, root, "write_file", WithSnapshot(rec.save)), `{"path":"neuf/n.txt","content":"x"}`)
	if res.IsError || res.NoCheckpoint || rec.calls != 0 {
		t.Fatalf("got %+v, appels save=%d", res, rec.calls)
	}
}

func TestSnapshotError(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "s.txt")
	write(t, p, "intact")
	rec := &snapRecorder{err: errors.New("boom")}
	w := findWith(t, root, "write_file", WithSnapshot(rec.save))
	res := run(t, w, `{"path":"s.txt","content":"perdu"}`)
	if !res.IsError || res.Content != "cannot save checkpoint: boom" {
		t.Fatalf("got %+v", res)
	}
	if data, _ := os.ReadFile(p); string(data) != "intact" {
		t.Fatalf("fichier modifié : %q", data)
	}
	e := findWith(t, root, "edit_file", WithSnapshot(rec.save))
	res = run(t, e, `{"path":"s.txt","old_string":"intact","new_string":"perdu"}`)
	if !res.IsError || res.Content != "cannot save checkpoint: boom" {
		t.Fatalf("edit: got %+v", res)
	}
	if data, _ := os.ReadFile(p); string(data) != "intact" {
		t.Fatalf("edit: fichier modifié : %q", data)
	}
}

func TestSnapshotTooLarge(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "gros.bin")
	big := strings.Repeat("a", SnapshotMaxBytes+1)
	write(t, p, big)
	rec := &snapRecorder{}
	res := run(t, findWith(t, root, "write_file", WithSnapshot(rec.save)), `{"path":"gros.bin","content":"petit"}`)
	if res.IsError || rec.calls != 0 || !res.NoCheckpoint {
		t.Fatalf("got %+v, appels save=%d", res, rec.calls)
	}
	if !strings.Contains(res.Content, "\ncheckpoint skipped (file too large)") {
		t.Fatalf("contenu = %q", res.Content)
	}
	if data, _ := os.ReadFile(p); string(data) != "petit" {
		t.Fatalf("fichier = %q", data)
	}
}

func TestSnapshotLimitExact(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "limite.txt")
	write(t, p, strings.Repeat("a", SnapshotMaxBytes))
	rec := &snapRecorder{}
	res := run(t, findWith(t, root, "write_file", WithSnapshot(rec.save)), `{"path":"limite.txt","content":"petit"}`)
	if res.IsError || res.NoCheckpoint || rec.calls != 1 {
		t.Fatalf("got %+v, appels save=%d", res, rec.calls)
	}
}

func TestNoSnapshotOption(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "o.txt"), "avant")
	res := run(t, find(t, root, "write_file"), `{"path":"o.txt","content":"après"}`)
	if res.IsError || res.NoCheckpoint || res.Content != "file written: o.txt" {
		t.Fatalf("got %+v", res)
	}
	res = run(t, find(t, root, "edit_file"), `{"path":"o.txt","old_string":"après","new_string":"fin"}`)
	if res.IsError || res.NoCheckpoint {
		t.Fatalf("edit got %+v", res)
	}
}

// Un fichier existant illisible n'est jamais écrasé : sans copie possible
// ni hash_before, l'écriture serait irréversible.
func TestUnreadableExistingRefused(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root lit les fichiers en mode 0200")
	}
	cases := []struct {
		tool, args string
	}{
		{"write_file", `{"path":"u.txt","content":"perdu"}`},
		{"edit_file", `{"path":"u.txt","old_string":"secret","new_string":"perdu"}`},
	}
	for _, snap := range []bool{false, true} {
		for _, c := range cases {
			root := t.TempDir()
			p := filepath.Join(root, "u.txt")
			write(t, p, "secret")
			if err := os.Chmod(p, 0o200); err != nil {
				t.Fatal(err)
			}
			rec := &snapRecorder{}
			var opts []Option
			if snap {
				opts = append(opts, WithSnapshot(rec.save))
			}
			res := run(t, findWith(t, root, c.tool, opts...), c.args)
			if !res.IsError || !strings.HasPrefix(res.Content, "cannot read existing file: ") {
				t.Fatalf("%s (copie %v) : %+v", c.tool, snap, res)
			}
			if err := os.Chmod(p, 0o600); err != nil {
				t.Fatal(err)
			}
			if data, _ := os.ReadFile(p); string(data) != "secret" {
				t.Fatalf("%s (copie %v) : fichier écrasé : %q", c.tool, snap, data)
			}
			if rec.calls != 0 {
				t.Fatalf("%s : save appelé %d fois", c.tool, rec.calls)
			}
		}
	}
}

func TestWithoutCheckpoints(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "o.txt"), "avant")
	w := findWith(t, root, "write_file", WithoutCheckpoints())
	res := run(t, w, `{"path":"o.txt","content":"après"}`)
	if res.IsError || !res.NoCheckpoint || res.Content != "file written: o.txt" {
		t.Fatalf("write existant : %+v", res)
	}
	res = run(t, findWith(t, root, "edit_file", WithoutCheckpoints()), `{"path":"o.txt","old_string":"après","new_string":"fin"}`)
	if res.IsError || !res.NoCheckpoint {
		t.Fatalf("edit : %+v", res)
	}
	// Un fichier créé se supprime sans copie : pas de no_checkpoint.
	res = run(t, w, `{"path":"neuf.txt","content":"x"}`)
	if res.IsError || res.NoCheckpoint {
		t.Fatalf("write nouveau : %+v", res)
	}
}
