package compliance

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tenon-code-eu/tenon/internal/audit"
	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/llm"
)

// ev construit un événement de test.
func ev(t *testing.T, sid string, seq uint64, ts time.Time, typ event.Type, payload any) event.Event {
	t.Helper()
	e, err := event.New(sid, seq, ts, typ, payload)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// started construit un SessionStarted pour la racine donnée.
func started(t *testing.T, sid string, ts time.Time, root string) event.Event {
	t.Helper()
	return ev(t, sid, 1, ts, event.SessionStarted, event.SessionStartedPayload{
		Provider:    llm.ProviderInfo{ID: "p"},
		ProjectRoot: root,
	})
}

// writeLog écrit une vraie chaîne dans un nouveau répertoire via audit.Writer.
func writeLog(t *testing.T, level audit.Level, events ...event.Event) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "audit")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	w, err := audit.Open(dir, level, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if err := w.Write(e); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return dir
}
