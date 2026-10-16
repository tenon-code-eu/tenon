package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tenon-code-eu/tenon/internal/event"
)

var day1 = time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)

func mk(t *testing.T, seq uint64, ts time.Time, payload any) event.Event {
	t.Helper()
	e, err := event.New("s1", seq, ts, event.UserMessage, payload)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func writeN(t *testing.T, dir string, n int) {
	t.Helper()
	w, err := Open(dir, Full, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= n; i++ {
		if err := w.Write(mk(t, uint64(i), day1, map[string]string{"text": "salut"})); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
}

func TestChainVerifies(t *testing.T) {
	dir := t.TempDir()
	writeN(t, dir, 3)
	last, n, err := Verify(dir)
	if err != nil || n != 3 {
		t.Fatalf("err=%v n=%d", err, n)
	}
	lines := readLines(t, filepath.Join(dir, "2026-10-14.jsonl"))
	var l struct{ Hash string }
	if err := json.Unmarshal([]byte(lines[2]), &l); err != nil {
		t.Fatal(err)
	}
	if last != l.Hash {
		t.Fatalf("lastHash %s != %s", last, l.Hash)
	}
}

func TestTamperedPayloadDetected(t *testing.T) {
	dir := t.TempDir()
	writeN(t, dir, 3)
	p := filepath.Join(dir, "2026-10-14.jsonl")
	lines := readLines(t, p)
	lines[1] = strings.Replace(lines[1], "salut", "hacked", 1)
	os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
	_, _, err := Verify(dir)
	if err == nil || !strings.Contains(err.Error(), "2026-10-14.jsonl:2") {
		t.Fatalf("err=%v", err)
	}
}

func TestDeletedLineDetected(t *testing.T) {
	dir := t.TempDir()
	writeN(t, dir, 3)
	p := filepath.Join(dir, "2026-10-14.jsonl")
	lines := readLines(t, p)
	os.WriteFile(p, []byte(lines[0]+"\n"+lines[2]+"\n"), 0o600)
	_, _, err := Verify(dir)
	if err == nil || !strings.Contains(err.Error(), "2026-10-14.jsonl:2") {
		t.Fatalf("err=%v", err)
	}
}

func TestReopenContinuesChain(t *testing.T) {
	dir := t.TempDir()
	writeN(t, dir, 3)
	w, err := Open(dir, Full, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Write(mk(t, 4, day1, "x")); err != nil {
		t.Fatal(err)
	}
	w.Close()
	if _, n, err := Verify(dir); err != nil || n != 4 {
		t.Fatalf("err=%v n=%d", err, n)
	}
}

func TestReopenCorruptLastLineFails(t *testing.T) {
	dir := t.TempDir()
	writeN(t, dir, 2)
	p := filepath.Join(dir, "2026-10-14.jsonl")
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString("garbage\n")
	f.Close()
	if _, err := Open(dir, Full, nil); err == nil {
		t.Fatal("want error")
	}
}

func TestDailyRotation(t *testing.T) {
	dir := t.TempDir()
	w, _ := Open(dir, Full, nil)
	w.Write(mk(t, 1, day1, "a"))
	w.Write(mk(t, 2, day1.Add(24*time.Hour), "b"))
	w.Close()
	m, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if len(m) != 2 {
		t.Fatalf("files=%v", m)
	}
	if _, n, err := Verify(dir); err != nil || n != 2 {
		t.Fatalf("err=%v n=%d", err, n)
	}
}

func TestPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "audit")
	w, err := Open(dir, Full, nil)
	if err != nil {
		t.Fatal(err)
	}
	w.Write(mk(t, 1, day1, "a"))
	w.Close()
	di, _ := os.Stat(dir)
	fi, _ := os.Stat(filepath.Join(dir, "2026-10-14.jsonl"))
	if di.Mode().Perm() != 0o700 || fi.Mode().Perm() != 0o600 {
		t.Fatalf("dir=%v file=%v", di.Mode().Perm(), fi.Mode().Perm())
	}
}

func TestMetadataLevelHidesContent(t *testing.T) {
	dir := t.TempDir()
	w, _ := Open(dir, Metadata, nil)
	w.Write(mk(t, 1, day1, map[string]string{"text": "salut"}))
	w.Close()
	b, _ := os.ReadFile(filepath.Join(dir, "2026-10-14.jsonl"))
	if strings.Contains(string(b), "salut") || !strings.Contains(string(b), `"sha256"`) {
		t.Fatalf("content: %s", b)
	}
	if _, n, err := Verify(dir); err != nil || n != 1 {
		t.Fatalf("err=%v n=%d", err, n)
	}
}

func TestRedactApplied(t *testing.T) {
	dir := t.TempDir()
	w, _ := Open(dir, Full, func(s string) string { return strings.ReplaceAll(s, "Zx8kq", "[MASQUE]") })
	w.Write(mk(t, 1, day1, map[string]string{"text": "clé Zx8kq"}))
	w.Close()
	b, _ := os.ReadFile(filepath.Join(dir, "2026-10-14.jsonl"))
	if strings.Contains(string(b), "Zx8kq") || !strings.Contains(string(b), "[MASQUE]") {
		t.Fatalf("content: %s", b)
	}
}

func TestRedactInvalidJSONFailsClosed(t *testing.T) {
	dir := t.TempDir()
	w, _ := Open(dir, Full, func(string) string { return "{broken" })
	defer w.Close()
	if err := w.Write(mk(t, 1, day1, "a")); err == nil {
		t.Fatal("want error")
	}
	if _, n, _ := Verify(dir); n != 0 {
		t.Fatalf("n=%d", n)
	}
}

func TestVerifyEmptyOrMissing(t *testing.T) {
	for _, d := range []string{t.TempDir(), filepath.Join(t.TempDir(), "nope")} {
		h, n, err := Verify(d)
		if h != "sha256:0" || n != 0 || err != nil {
			t.Fatalf("%q %d %v", h, n, err)
		}
	}
}

func tamperLine2(t *testing.T, f func(string) string) error {
	t.Helper()
	dir := t.TempDir()
	writeN(t, dir, 3)
	p := filepath.Join(dir, "2026-10-14.jsonl")
	lines := readLines(t, p)
	lines[1] = f(lines[1])
	os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
	_, _, err := Verify(dir)
	return err
}

func TestNonCanonicalLineDetected(t *testing.T) {
	cases := map[string]func(string) string{
		"extra field": func(s string) string { return strings.TrimSuffix(s, "}") + `,"note":"x"}` },
		"dup payload": func(s string) string { return strings.TrimSuffix(s, "}") + `,"payload":{"text":"salut"}}` },
		"key case":    func(s string) string { return strings.Replace(s, `"session"`, `"SESSION"`, 1) },
	}
	for name, f := range cases {
		err := tamperLine2(t, f)
		if err == nil || !strings.Contains(err.Error(), "2026-10-14.jsonl:2") {
			t.Errorf("%s: err=%v", name, err)
		}
	}
}

func TestClockBackwardsKeepsFile(t *testing.T) {
	dir := t.TempDir()
	w, _ := Open(dir, Full, nil)
	w.Write(mk(t, 1, day1.Add(24*time.Hour), "a"))
	w.Write(mk(t, 2, day1, "b"))
	w.Close()
	m, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if len(m) != 1 || filepath.Base(m[0]) != "2026-10-15.jsonl" {
		t.Fatalf("files=%v", m)
	}
	if _, n, err := Verify(dir); err != nil || n != 2 {
		t.Fatalf("err=%v n=%d", err, n)
	}
}

func TestTwoWritersSameDir(t *testing.T) {
	dir := t.TempDir()
	a, _ := Open(dir, Full, nil)
	b, _ := Open(dir, Full, nil)
	for i := 0; i < 5; i++ {
		if err := a.Write(mk(t, uint64(i), day1, "a")); err != nil {
			t.Fatal(err)
		}
		if err := b.Write(mk(t, uint64(i), day1, "b")); err != nil {
			t.Fatal(err)
		}
	}
	a.Close()
	b.Close()
	if _, n, err := Verify(dir); err != nil || n != 10 {
		t.Fatalf("err=%v n=%d", err, n)
	}
}

func TestFailedWriteTruncatesAndBreaks(t *testing.T) {
	for _, mode := range []string{"write", "sync"} {
		dir := t.TempDir()
		w, _ := Open(dir, Full, nil)
		if err := w.Write(mk(t, 1, day1, "a")); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, "2026-10-14.jsonl")
		before, _ := os.ReadFile(p)
		if mode == "write" {
			w.writeFn = func(f *os.File, b []byte) (int, error) {
				f.Write(b[:10])
				return 10, os.ErrInvalid
			}
		} else {
			w.syncFn = func(*os.File) error { return os.ErrInvalid }
		}
		if err := w.Write(mk(t, 2, day1, "b")); err == nil {
			t.Fatalf("%s: want error", mode)
		}
		after, _ := os.ReadFile(p)
		if string(after) != string(before) {
			t.Fatalf("%s: not truncated: %q", mode, after)
		}
		if err := w.Write(mk(t, 3, day1, "c")); err == nil {
			t.Fatalf("%s: writer should be broken", mode)
		}
		w.Close()
		if _, n, err := Verify(dir); err != nil || n != 1 {
			t.Fatalf("%s: err=%v n=%d", mode, err, n)
		}
	}
}

func TestOpenChmodsExistingDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "audit")
	os.Mkdir(dir, 0o755)
	os.Chmod(dir, 0o755)
	w, err := Open(dir, Full, nil)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o700 {
		t.Fatalf("perm=%v", fi.Mode().Perm())
	}
}

func TestOpenChmodsExistingFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "audit")
	os.MkdirAll(dir, 0o700)
	p := filepath.Join(dir, "2026-10-13.jsonl")
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	os.Chmod(p, 0o644)
	w, err := Open(dir, Full, nil)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Fatalf("perm=%v", fi.Mode().Perm())
	}
}
