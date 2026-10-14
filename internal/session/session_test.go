package session

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codeberg.org/tenon/tenon/internal/event"
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
	w, _ := Open(dir, sidA, func(string) string { return "{pas du json" })
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
	w, _ := Open(dir, sidA, nil)
	w.Write(ev(t, sidA, 1, event.UserMessage, event.UserMessagePayload{Text: "a"}))
	w.Write(ev(t, sidA, 2, event.UserMessage, event.UserMessagePayload{Text: "b"}))
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
	w, err := Open(dir, sid, nil)
	if err != nil {
		t.Fatal(err)
	}
	seq := uint64(1)
	w.Write(ev(t, sid, seq, event.SessionStarted, event.SessionStartedPayload{ProjectRoot: root}))
	seq++
	w.Write(ev(t, sid, seq, event.UserMessage, event.UserMessagePayload{Text: prompt}))
	for i := 0; i < turns; i++ {
		seq++
		w.Write(ev(t, sid, seq, event.TurnCompleted, event.TurnCompletedPayload{Reason: "done"}))
	}
	w.Close()
	os.Chtimes(filepath.Join(dir, sid+".jsonl"), mod, mod)
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
	w, _ = Open(dir, sidA, nil)
	w.Close()
	ds, _ = os.Stat(dir)
	fs, _ = os.Stat(filepath.Join(dir, sidA+".jsonl"))
	if ds.Mode().Perm() != 0o700 || fs.Mode().Perm() != 0o600 {
		t.Fatalf("après réouverture dir=%v file=%v", ds.Mode().Perm(), fs.Mode().Perm())
	}
}

func TestConcurrentWrite(t *testing.T) {
	dir := t.TempDir()
	w, _ := Open(dir, sidA, nil)
	defer w.Close()
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func(i int) {
			w.Write(ev(t, sidA, uint64(i), event.UserMessage, event.UserMessagePayload{Text: "x"}))
			done <- struct{}{}
		}(i)
	}
	for i := 0; i < 8; i++ {
		<-done
	}
	got, corrupt, err := Load(dir, sidA)
	if err != nil || corrupt || len(got) != 8 {
		t.Fatalf("got=%d corrupt=%v err=%v", len(got), corrupt, err)
	}
}
