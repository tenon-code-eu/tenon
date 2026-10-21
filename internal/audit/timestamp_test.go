package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tenon-code-eu/tenon/internal/tsp"
	"github.com/tenon-code-eu/tenon/internal/tsp/tsptest"
)

var tsGen = time.Date(2026, 10, 21, 15, 30, 0, 0, time.UTC)

// writeAt écrit une ligne par horodatage donné (même jour : un seul fichier).
func writeAt(t *testing.T, dir string, tss ...time.Time) {
	t.Helper()
	w, err := Open(dir, Full, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i, ts := range tss {
		if err := w.Write(mk(t, uint64(i+1), ts, map[string]string{"text": "x"})); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

// hashes renvoie le hash de chaque ligne de la chaîne.
func hashes(t *testing.T, dir string) []string {
	t.Helper()
	var hs []string
	err := ReadLines(dir, func(_ string, _ int, l Line, err error) error {
		hs = append(hs, l.Hash)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return hs
}

// anchor émet un jeton sur head et le range dans dir/timestamps.
func anchor(t *testing.T, ca *tsptest.CA, dir, head string, gen time.Time) string {
	t.Helper()
	d, err := Digest(head)
	if err != nil {
		t.Fatal(err)
	}
	tok := ca.Token(t, d, nil, tsptest.Options{GenTime: gen})
	info, err := tsp.ParseToken(tok)
	if err != nil {
		t.Fatal(err)
	}
	p, err := SaveToken(dir, info, tok)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestHeadEmptyAndFilled(t *testing.T) {
	dir := t.TempDir()
	h, err := Head(dir)
	if err != nil || h != "" {
		t.Fatalf("empty: %q %v", h, err)
	}
	h, err = Head(filepath.Join(dir, "absent"))
	if err != nil || h != "" {
		t.Fatalf("missing: %q %v", h, err)
	}
	writeN(t, dir, 3)
	h, err = Head(dir)
	if err != nil {
		t.Fatal(err)
	}
	hs := hashes(t, dir)
	if h != hs[2] {
		t.Fatalf("head %q want %q", h, hs[2])
	}
}

func TestDigest(t *testing.T) {
	hexs := strings.Repeat("ab", 32)
	d, err := Digest("sha256:" + hexs)
	if err != nil || len(d) != 32 || d[0] != 0xab {
		t.Fatalf("valid: %x %v", d, err)
	}
	for _, bad := range []string{"sha256:0", "sha256:" + strings.Repeat("zz", 32), "sha256:" + strings.Repeat("ab", 31), "", hexs} {
		if _, err := Digest(bad); err == nil {
			t.Errorf("Digest(%q) should fail", bad)
		}
	}
}

func TestSaveTokenAndAnchored(t *testing.T) {
	dir := t.TempDir()
	writeN(t, dir, 2)
	hs := hashes(t, dir)
	ca := tsptest.NewCA(t)
	p := anchor(t, ca, dir, hs[1], tsGen)
	want := "20261021T153000Z-" + strings.TrimPrefix(hs[1], "sha256:")[:12] + ".tsr"
	if filepath.Base(p) != want || filepath.Dir(p) != filepath.Join(dir, TimestampDir) {
		t.Fatalf("path %s want %s", p, want)
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0o600 {
		t.Errorf("file mode %v", st.Mode().Perm())
	}
	sd, _ := os.Stat(filepath.Dir(p))
	if sd.Mode().Perm() != 0o700 {
		t.Errorf("dir mode %v", sd.Mode().Perm())
	}
	ok, err := Anchored(dir, hs[1])
	if err != nil || !ok {
		t.Fatalf("anchored: %v %v", ok, err)
	}
	ok, err = Anchored(dir, hs[0])
	if err != nil || ok {
		t.Fatalf("other head: %v %v", ok, err)
	}
	// Second enregistrement identique : même chemin, contenu inchangé.
	before, _ := os.ReadFile(p)
	d, _ := Digest(hs[1])
	tok := ca.Token(t, d, nil, tsptest.Options{GenTime: tsGen})
	info, _ := tsp.ParseToken(tok)
	p2, err := SaveToken(dir, info, tok)
	if err != nil || p2 != p {
		t.Fatalf("second save: %s %v", p2, err)
	}
	after, _ := os.ReadFile(p)
	if string(before) != string(after) {
		t.Fatal("content changed")
	}
	// Dossier absent.
	ok, err = Anchored(t.TempDir(), hs[1])
	if err != nil || ok {
		t.Fatalf("missing dir: %v %v", ok, err)
	}
}

func TestVerifyTimestampsValid(t *testing.T) {
	dir := t.TempDir()
	writeN(t, dir, 3)
	hs := hashes(t, dir)
	ca := tsptest.NewCA(t)
	anchor(t, ca, dir, hs[1], day1.Add(time.Minute))
	s, err := VerifyTimestamps(dir, ca.Pool)
	if err != nil {
		t.Fatal(err)
	}
	if s.Count != 1 || s.Valid != 1 || s.LastLine != 2 || s.LinesAfter != 1 || len(s.Errors) != 0 || s.Warnings == nil || s.Errors == nil {
		t.Fatalf("%+v", s)
	}
	if s.LastFile == "" || s.LastTime.IsZero() {
		t.Fatalf("%+v", s)
	}
}

func TestVerifyTimestampsNoDir(t *testing.T) {
	dir := t.TempDir()
	writeN(t, dir, 2)
	s, err := VerifyTimestamps(dir, tsptest.NewCA(t).Pool)
	if err != nil || s.Count != 0 || s.LinesAfter != 2 || s.Errors == nil || s.Warnings == nil {
		t.Fatalf("%+v %v", s, err)
	}
}

func TestVerifyTimestampsRewrittenChain(t *testing.T) {
	dir := t.TempDir()
	writeN(t, dir, 3)
	hs := hashes(t, dir)
	ca := tsptest.NewCA(t)
	anchor(t, ca, dir, hs[1], day1.Add(time.Minute))
	file := filepath.Join(dir, day1.Format("2006-01-02")+".jsonl")
	prev := genesis
	var out []string
	for _, raw := range readLines(t, file) {
		var l Line
		if err := json.Unmarshal([]byte(raw), &l); err != nil {
			t.Fatal(err)
		}
		l.Payload = json.RawMessage(`{"text":"forged"}`)
		l.Prev = prev
		h, err := computeHash(l)
		if err != nil {
			t.Fatal(err)
		}
		l.Hash = h
		prev = h
		b, _ := json.Marshal(l)
		out = append(out, string(b))
	}
	if err := os.WriteFile(file, []byte(strings.Join(out, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := VerifyTimestamps(dir, ca.Pool)
	if err != nil {
		t.Fatal(err)
	}
	if s.Valid != 0 || len(s.Errors) != 1 || !strings.HasSuffix(s.Errors[0], ": anchored hash not in chain") || !strings.HasPrefix(s.Errors[0], "timestamp 2026") {
		t.Fatalf("%+v", s)
	}
}

func TestVerifyTimestampsLogDeleted(t *testing.T) {
	dir := t.TempDir()
	writeN(t, dir, 2)
	hs := hashes(t, dir)
	ca := tsptest.NewCA(t)
	anchor(t, ca, dir, hs[1], day1)
	if err := os.Remove(filepath.Join(dir, day1.Format("2006-01-02")+".jsonl")); err != nil {
		t.Fatal(err)
	}
	s, err := VerifyTimestamps(dir, ca.Pool)
	if err != nil {
		t.Fatal(err)
	}
	if s.Count != 1 || s.Valid != 0 || len(s.Errors) != 1 || !strings.Contains(s.Errors[0], "anchored hash not in chain") {
		t.Fatalf("%+v", s)
	}
}

func TestVerifyTimestampsJunk(t *testing.T) {
	dir := t.TempDir()
	writeN(t, dir, 2)
	hs := hashes(t, dir)
	ca := tsptest.NewCA(t)
	p := anchor(t, ca, dir, hs[1], day1)
	td := filepath.Join(dir, TimestampDir)
	if err := os.WriteFile(filepath.Join(td, "README"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	der, _ := os.ReadFile(p)
	bad := "20261022T000000Z-aaaaaaaaaaaa.tsr"
	if err := os.WriteFile(filepath.Join(td, bad), der[:len(der)/2], 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := VerifyTimestamps(dir, ca.Pool)
	if err != nil {
		t.Fatal(err)
	}
	if s.Count != 2 || s.Valid != 1 || len(s.Errors) != 1 || !strings.HasPrefix(s.Errors[0], "timestamp "+bad+": ") {
		t.Fatalf("%+v", s)
	}
}

func TestVerifyTimestampsClockWarning(t *testing.T) {
	for _, tc := range []struct {
		skew time.Duration
		warn bool
	}{{10 * time.Minute, true}, {4 * time.Minute, false}} {
		dir := t.TempDir()
		writeAt(t, dir, day1, day1.Add(tc.skew))
		hs := hashes(t, dir)
		ca := tsptest.NewCA(t)
		p := anchor(t, ca, dir, hs[1], day1)
		s, err := VerifyTimestamps(dir, ca.Pool)
		if err != nil {
			t.Fatal(err)
		}
		if s.Valid != 1 || len(s.Errors) != 0 {
			t.Fatalf("%+v", s)
		}
		if !tc.warn {
			if len(s.Warnings) != 0 {
				t.Fatalf("%+v", s.Warnings)
			}
			continue
		}
		want := "timestamp " + filepath.Base(p) + ": " + day1.Format("2006-01-02") + ".jsonl:2 ts after genTime (local clock?)"
		if len(s.Warnings) != 1 || s.Warnings[0] != want {
			t.Fatalf("%q want %q", s.Warnings, want)
		}
	}
}

func TestVerifyTimestampsBrokenChain(t *testing.T) {
	dir := t.TempDir()
	writeN(t, dir, 3)
	file := filepath.Join(dir, day1.Format("2006-01-02")+".jsonl")
	ls := readLines(t, file)
	os.WriteFile(file, []byte(ls[0]+"\n"+ls[2]+"\n"), 0o600)
	_, _, verr := Verify(dir)
	if verr == nil {
		t.Fatal("chain should be broken")
	}
	_, err := VerifyTimestamps(dir, tsptest.NewCA(t).Pool)
	if err == nil || err.Error() != verr.Error() {
		t.Fatalf("%v vs %v", err, verr)
	}
}
