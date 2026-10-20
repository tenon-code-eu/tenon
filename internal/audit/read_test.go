package audit

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type got struct {
	file string
	n    int
	l    Line
	err  error
}

func collect(t *testing.T, dir string) []got {
	t.Helper()
	var out []got
	err := ReadLines(dir, func(file string, n int, l Line, err error) error {
		out = append(out, got{file, n, l, err})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestReadLinesOrder(t *testing.T) {
	dir := t.TempDir()
	w, err := Open(dir, Full, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i, ts := range []time.Time{day1, day1, day1.AddDate(0, 0, 1)} {
		if err := w.Write(mk(t, uint64(i+1), ts, map[string]string{"a": "b"})); err != nil {
			t.Fatal(err)
		}
	}
	w.Close()
	res := collect(t, dir)
	if len(res) != 3 {
		t.Fatalf("got %d lines", len(res))
	}
	want := []struct {
		file string
		n    int
	}{{"2026-10-14.jsonl", 1}, {"2026-10-14.jsonl", 2}, {"2026-10-15.jsonl", 1}}
	prev := genesis
	for i, r := range res {
		if r.err != nil || r.file != want[i].file || r.n != want[i].n {
			t.Fatalf("line %d: %+v", i, r)
		}
		if r.l.Seq != uint64(i+1) || r.l.Prev != prev || r.l.Hash == "" {
			t.Fatalf("line %d chain: %+v", i, r.l)
		}
		prev = r.l.Hash
	}
}

func TestReadLinesUnreadable(t *testing.T) {
	dir := t.TempDir()
	writeN(t, dir, 2)
	p := filepath.Join(dir, "2026-10-14.jsonl")
	b, _ := os.ReadFile(p)
	if err := os.WriteFile(p, append(append([]byte("garbage\n\n"), b...), '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	res := collect(t, dir)
	if len(res) != 3 {
		t.Fatalf("got %d lines", len(res))
	}
	if res[0].err == nil || res[0].n != 1 || res[0].l.Hash != "" {
		t.Fatalf("garbage: %+v", res[0])
	}
	if res[1].err != nil || res[1].n != 3 || res[2].err != nil || res[2].n != 4 {
		t.Fatalf("next lines: %+v %+v", res[1], res[2])
	}
}

func TestReadLinesMissingDir(t *testing.T) {
	calls := 0
	err := ReadLines(filepath.Join(t.TempDir(), "nope"), func(string, int, Line, error) error {
		calls++
		return nil
	})
	if err != nil || calls != 0 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestReadLinesStop(t *testing.T) {
	dir := t.TempDir()
	writeN(t, dir, 3)
	stop := errors.New("stop")
	calls := 0
	err := ReadLines(dir, func(string, int, Line, error) error {
		calls++
		return stop
	})
	if err != stop || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestReadLinesNoWrite(t *testing.T) {
	dir := t.TempDir()
	writeN(t, dir, 2)
	p := filepath.Join(dir, "2026-10-14.jsonl")
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(p)
	collect(t, dir)
	after, _ := os.Stat(p)
	if after.Mode() != before.Mode() || !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("file changed: %v/%v %v/%v", before.Mode(), after.Mode(), before.ModTime(), after.ModTime())
	}
}
