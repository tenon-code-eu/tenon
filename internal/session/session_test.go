package session

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/tenon-code-eu/tenon/internal/event"
)

const (
	sidA = "s_aaaaaaaaaaaa"
	sidB = "s_bbbbbbbbbbbb"
	sidC = "s_cccccccccccc"
)

func ev(t *testing.T, sid string, seq uint64, typ event.Type, p any) event.Event {
	t.Helper()
	e, err := event.New(sid, seq, time.Unix(1700000000+int64(seq), 0).UTC(), typ, p)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestWriteAndLoad(t *testing.T) {
	dir := t.TempDir()
	w, err := Open(dir, sidA, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	for _, e := range []event.Event{
		ev(t, sidA, 1, event.UserMessage, event.UserMessagePayload{Text: "salut"}),
		ev(t, sidA, 2, event.AssistantDelta, event.AssistantDeltaPayload{Text: "sa"}),
		ev(t, sidB, 3, event.UserMessage, event.UserMessagePayload{Text: "autre"}),
		ev(t, sidA, 4, event.TurnCompleted, event.TurnCompletedPayload{Reason: "done"}),
	} {
		if err := w.Write(e); err != nil {
			t.Fatal(err)
		}
	}
	got, corrupt, err := Load(dir, sidA)
	if err != nil || corrupt {
		t.Fatalf("err=%v corrupt=%v", err, corrupt)
	}
	if len(got) != 2 || got[0].Type != event.UserMessage || got[1].Type != event.TurnCompleted {
		t.Fatalf("événements: %+v", got)
	}
}

func TestSessionFileRedacted(t *testing.T) {
	dir := t.TempDir()
	w, err := Open(dir, sidA, func(s string) string { return strings.ReplaceAll(s, "Zx8kq", "[MASQUE]") })
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := w.Write(ev(t, sidA, 1, event.UserMessage, event.UserMessagePayload{Text: "clé Zx8kq"})); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, sidA+".jsonl"))
	if strings.Contains(string(b), "Zx8kq") || !strings.Contains(string(b), "[MASQUE]") {
		t.Fatalf("fichier: %s", b)
	}
}

func TestRedactInvalidFailsClosed(t *testing.T) {
	dir := t.TempDir()
	w, err := Open(dir, sidA, func(string) string { return "{pas du json" })
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := w.Write(ev(t, sidA, 1, event.UserMessage, event.UserMessagePayload{Text: "x"})); err == nil {
		t.Fatal("erreur attendue")
	}
	b, _ := os.ReadFile(filepath.Join(dir, sidA+".jsonl"))
	if len(b) != 0 {
		t.Fatalf("rien ne doit être écrit: %s", b)
	}
}

func TestLoadStopsAtCorruptLine(t *testing.T) {
	dir := t.TempDir()
	w := mustOpen(t, dir, sidA)
	mustWrite(t, w, ev(t, sidA, 1, event.UserMessage, event.UserMessagePayload{Text: "a"}))
	mustWrite(t, w, ev(t, sidA, 2, event.UserMessage, event.UserMessagePayload{Text: "b"}))
	w.Close()
	f, _ := os.OpenFile(filepath.Join(dir, sidA+".jsonl"), os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString(`{"id":"x","sess` + "\n")
	f.Close()
	got, corrupt, err := Load(dir, sidA)
	if err != nil || !corrupt || len(got) != 2 {
		t.Fatalf("got=%d corrupt=%v err=%v", len(got), corrupt, err)
	}
}

func TestLoadMissing(t *testing.T) {
	_, _, err := Load(t.TempDir(), sidA)
	if !errors.Is(err, ErrNoSession) {
		t.Fatalf("err=%v", err)
	}
}

func TestInvalidSessionID(t *testing.T) {
	dir := t.TempDir()
	for _, id := range []string{"../x", "s_XYZ", "", "s_aaaaaaaaaaaa/../x"} {
		if _, err := Open(dir, id, nil); err == nil {
			t.Errorf("Open(%q) doit échouer", id)
		}
		if _, _, err := Load(dir, id); err == nil || errors.Is(err, ErrNoSession) {
			t.Errorf("Load(%q) doit échouer (err=%v)", id, err)
		}
	}
}

func mkSession(t *testing.T, dir, sid, root, prompt string, turns int, mod time.Time) {
	t.Helper()
	w := mustOpen(t, dir, sid)
	seq := uint64(1)
	mustWrite(t, w, ev(t, sid, seq, event.SessionStarted, event.SessionStartedPayload{ProjectRoot: root}))
	seq++
	mustWrite(t, w, ev(t, sid, seq, event.UserMessage, event.UserMessagePayload{Text: prompt}))
	for i := 0; i < turns; i++ {
		seq++
		mustWrite(t, w, ev(t, sid, seq, event.TurnCompleted, event.TurnCompletedPayload{Reason: "done"}))
	}
	w.Close()
	if err := os.Chtimes(filepath.Join(dir, sid+".jsonl"), mod, mod); err != nil {
		t.Fatal(err)
	}
}

func TestListFiltersProjectAndSorts(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	long := strings.Repeat("é", 100)
	mkSession(t, dir, sidA, "/p", "ancienne", 1, now.Add(-time.Hour))
	mkSession(t, dir, sidB, "/autre", "ailleurs", 1, now)
	mkSession(t, dir, sidC, "/p", long, 2, now.Add(-time.Minute))
	got, err := List(dir, "/p")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != sidC || got[1].ID != sidA {
		t.Fatalf("liste: %+v", got)
	}
	if got[0].Turns != 2 || got[1].FirstPrompt != "ancienne" || got[1].ProjectRoot != "/p" {
		t.Fatalf("liste: %+v", got)
	}
	if n := len([]rune(got[0].FirstPrompt)); n != 80 {
		t.Fatalf("troncature: %d runes", n)
	}
	id, err := Latest(dir, "/p")
	if err != nil || id != sidC {
		t.Fatalf("Latest=%q err=%v", id, err)
	}
	if _, err := Latest(dir, "/rien"); !errors.Is(err, ErrNoSession) {
		t.Fatalf("err=%v", err)
	}
	if got, err := List(filepath.Join(dir, "absent"), "/p"); err != nil || len(got) != 0 {
		t.Fatalf("dossier absent: %v %v", got, err)
	}
}

func TestPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	w, err := Open(dir, sidA, nil)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	ds, _ := os.Stat(dir)
	fs, _ := os.Stat(filepath.Join(dir, sidA+".jsonl"))
	if ds.Mode().Perm() != 0o700 || fs.Mode().Perm() != 0o600 {
		t.Fatalf("dir=%v file=%v", ds.Mode().Perm(), fs.Mode().Perm())
	}
	os.Chmod(dir, 0o755)
	os.Chmod(filepath.Join(dir, sidA+".jsonl"), 0o644)
	w = mustOpen(t, dir, sidA)
	w.Close()
	ds, _ = os.Stat(dir)
	fs, _ = os.Stat(filepath.Join(dir, sidA+".jsonl"))
	if ds.Mode().Perm() != 0o700 || fs.Mode().Perm() != 0o600 {
		t.Fatalf("après réouverture dir=%v file=%v", ds.Mode().Perm(), fs.Mode().Perm())
	}
}

func TestConcurrentWrite(t *testing.T) {
	dir := t.TempDir()
	w := mustOpen(t, dir, sidA)
	defer w.Close()
	var evs []event.Event
	for i := 0; i < 8; i++ {
		evs = append(evs, ev(t, sidA, uint64(i), event.UserMessage, event.UserMessagePayload{Text: "x"}))
	}
	errs := make(chan error, len(evs))
	for _, e := range evs {
		go func(e event.Event) { errs <- w.Write(e) }(e)
	}
	for range evs {
		if err := <-errs; err != nil {
			t.Error(err)
		}
	}
	got, corrupt, err := Load(dir, sidA)
	if err != nil || corrupt || len(got) != 8 {
		t.Fatalf("got=%d corrupt=%v err=%v", len(got), corrupt, err)
	}
}

func TestOpenRepairsPartialLine(t *testing.T) {
	dir := t.TempDir()
	w := mustOpen(t, dir, sidA)
	mustWrite(t, w, ev(t, sidA, 1, event.UserMessage, event.UserMessagePayload{Text: "a"}))
	mustWrite(t, w, ev(t, sidA, 2, event.UserMessage, event.UserMessagePayload{Text: "b"}))
	w.Close()
	f, err := os.OpenFile(filepath.Join(dir, sidA+".jsonl"), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"id":"s_`)
	f.Close()
	w = mustOpen(t, dir, sidA)
	mustWrite(t, w, ev(t, sidA, 3, event.UserMessage, event.UserMessagePayload{Text: "c"}))
	w.Close()
	got, corrupt, err := Load(dir, sidA)
	if err != nil || corrupt || len(got) != 3 {
		t.Fatalf("got=%d corrupt=%v err=%v", len(got), corrupt, err)
	}
}

func TestOpenAddsMissingNewline(t *testing.T) {
	dir := t.TempDir()
	w := mustOpen(t, dir, sidA)
	mustWrite(t, w, ev(t, sidA, 1, event.UserMessage, event.UserMessagePayload{Text: "a"}))
	w.Close()
	p := filepath.Join(dir, sidA+".jsonl")
	b, _ := os.ReadFile(p)
	os.WriteFile(p, b[:len(b)-1], 0o600)
	w = mustOpen(t, dir, sidA)
	mustWrite(t, w, ev(t, sidA, 2, event.UserMessage, event.UserMessagePayload{Text: "b"}))
	w.Close()
	got, corrupt, err := Load(dir, sidA)
	if err != nil || corrupt || len(got) != 2 {
		t.Fatalf("got=%d corrupt=%v err=%v", len(got), corrupt, err)
	}
}

func TestFirstPromptSanitised(t *testing.T) {
	dir := t.TempDir()
	mkSession(t, dir, sidA, "/p", "a\nb\x1b[31mc\td\re", 0, time.Now())
	got, err := List(dir, "/p")
	if err != nil || len(got) != 1 {
		t.Fatalf("%v %v", got, err)
	}
	for _, r := range got[0].FirstPrompt {
		if unicode.IsControl(r) {
			t.Fatalf("contrôle dans %q", got[0].FirstPrompt)
		}
	}
}

func mustOpen(t *testing.T, dir, id string) *Writer {
	t.Helper()
	w, err := Open(dir, id, nil)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func mustWrite(t *testing.T, w *Writer, e event.Event) {
	t.Helper()
	if err := w.Write(e); err != nil {
		t.Fatal(err)
	}
}

func TestFirstPromptStripsFormatChars(t *testing.T) {
	dir := t.TempDir()
	mkSession(t, dir, sidA, "/p", "a\u202eb\u200bc\ufeffd", 0, time.Now())
	got, err := List(dir, "/p")
	if err != nil || len(got) != 1 {
		t.Fatalf("%v %v", got, err)
	}
	if got[0].FirstPrompt != "a b c d" {
		t.Fatalf("FirstPrompt %q", got[0].FirstPrompt)
	}
}

// Avant de tronquer une fin illisible, Open en garde une copie complète
// <id>.jsonl.corrupt (0600), remplacée à chaque réparation.
func TestOpenKeepsCorruptCopy(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, sidA+".jsonl")
	for i, tail := range []string{`{"id":"s_`, "pas du json\n"} {
		w := mustOpen(t, dir, sidA)
		mustWrite(t, w, ev(t, sidA, uint64(i+1), event.UserMessage, event.UserMessagePayload{Text: "a"}))
		w.Close()
		f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		f.WriteString(tail)
		f.Close()
		orig, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		mustOpen(t, dir, sidA).Close()
		cp, err := os.ReadFile(p + ".corrupt")
		if err != nil || string(cp) != string(orig) {
			t.Fatalf("copie %d : %q (%v), attendu %q", i, cp, err, orig)
		}
		if fi, err := os.Stat(p + ".corrupt"); err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("permissions de la copie : %v %v", fi.Mode(), err)
		}
	}
	// Un fichier sain n'est pas copié de nouveau.
	os.Remove(p + ".corrupt")
	mustOpen(t, dir, sidA).Close()
	if _, err := os.Stat(p + ".corrupt"); !os.IsNotExist(err) {
		t.Fatalf("copie d'un fichier sain : %v", err)
	}
}

// Un chemin de projet passant par un lien symbolique désigne le même projet.
func TestListResolvesSymlinks(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "reel")
	link := filepath.Join(base, "lien")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, "sessions")
	mkSession(t, dir, sidA, real, "x", 1, time.Now())
	for _, root := range []string{link, link + "/", real} {
		if got, err := List(dir, root); err != nil || len(got) != 1 {
			t.Fatalf("List(%q) : %v %v", root, got, err)
		}
		if !SameRoot(real, root) {
			t.Fatalf("SameRoot(%q, %q) faux", real, root)
		}
	}
	if SameRoot(real, base) {
		t.Fatal("SameRoot vrai pour un autre dossier")
	}
}

func TestSecondWriterRefused(t *testing.T) {
	dir := t.TempDir()
	w1, err := Open(dir, sidA, nil)
	if err != nil {
		t.Fatal(err)
	}
	w2, err := Open(dir, sidA, nil)
	if !errors.Is(err, ErrLocked) {
		if w2 != nil {
			w2.Close()
		}
		t.Fatalf("2e Open : err=%v, attendu ErrLocked", err)
	}
	if err := w1.Close(); err != nil {
		t.Fatal(err)
	}
	w3, err := Open(dir, sidA, nil)
	if err != nil {
		t.Fatalf("Open après Close : %v", err)
	}
	w3.Close()
}

func TestLockFilePermissions(t *testing.T) {
	dir := t.TempDir()
	w, err := Open(dir, sidA, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	fi, err := os.Stat(filepath.Join(dir, sidA+".jsonl.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("permissions = %v, attendu 0600", fi.Mode().Perm())
	}
}

func TestDifferentSessionsIndependent(t *testing.T) {
	dir := t.TempDir()
	wa, err := Open(dir, sidA, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer wa.Close()
	wb, err := Open(dir, sidB, nil)
	if err != nil {
		t.Fatalf("session B : %v", err)
	}
	wb.Close()
}

// Le verrou pris par Acquire exclut un autre Acquire jusqu'à Release ou
// jusqu'à la fermeture du Writer à qui Open l'a confié.
func TestAcquireThenOpen(t *testing.T) {
	dir := t.TempDir()
	if err := Exists(dir, sidA); !errors.Is(err, ErrNoSession) {
		t.Fatalf("Exists = %v, attendu ErrNoSession", err)
	}
	if err := Exists(dir, "x"); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("Exists = %v, attendu ErrInvalidID", err)
	}
	l, err := Acquire(dir, sidA)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(dir, sidA); !errors.Is(err, ErrLocked) {
		t.Fatalf("2e Acquire : %v, attendu ErrLocked", err)
	}
	l.Release()
	l, err = Acquire(dir, sidA)
	if err != nil {
		t.Fatalf("Acquire après Release : %v", err)
	}
	w, err := l.Open(nil)
	if err != nil {
		t.Fatal(err)
	}
	l.Release() // sans effet : le verrou appartient au Writer
	if _, err := Acquire(dir, sidA); !errors.Is(err, ErrLocked) {
		t.Fatalf("Acquire pendant le Writer : %v, attendu ErrLocked", err)
	}
	if err := Exists(dir, sidA); err != nil {
		t.Fatalf("Exists après Open : %v", err)
	}
	w.Close()
	l2, err := Acquire(dir, sidA)
	if err != nil {
		t.Fatalf("Acquire après Close : %v", err)
	}
	l2.Release()
}

func TestFirstPromptPrefersTyped(t *testing.T) {
	dir := t.TempDir()
	w := mustOpen(t, dir, sidA)
	mustWrite(t, w, ev(t, sidA, 1, event.SessionStarted, event.SessionStartedPayload{ProjectRoot: "/p"}))
	mustWrite(t, w, ev(t, sidA, 2, event.UserMessage, event.UserMessagePayload{Text: "Use the skill \"review\".\n<skill>", Typed: "/review\x1b[31m main.go", Skill: "review"}))
	w.Close()
	got, err := List(dir, "/p")
	if err != nil || len(got) != 1 {
		t.Fatalf("%v %v", got, err)
	}
	if got[0].FirstPrompt != "/review [31m main.go" {
		t.Fatalf("FirstPrompt = %q", got[0].FirstPrompt)
	}
}
