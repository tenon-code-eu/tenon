// Package session persists a session's events as redacted JSONL and reloads them.
package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"

	"codeberg.org/tenon/tenon/internal/event"
)

// ErrNoSession means no matching session exists.
var ErrNoSession = errors.New("aucune session trouvée")

// ErrInvalidID means a session ID is not of the form s_<12 hex digits>.
var ErrInvalidID = errors.New("identifiant de session invalide")

var idRe = regexp.MustCompile(`^s_[0-9a-f]{12}$`)

var _ event.Sink = (*Writer)(nil)

func validID(id string) error {
	if !idRe.MatchString(id) {
		return fmt.Errorf("%w : %q", ErrInvalidID, id)
	}
	return nil
}

// Writer appends one redacted, fsynced JSON line per event of its session.
// Safe for concurrent use.
type Writer struct {
	mu     sync.Mutex
	id     string
	redact func(string) string
	f      *os.File
}

// Open creates dir (0700) and opens <dir>/<sessionID>.jsonl (0600) in append
// mode, so an existing session is continued. A nil redact is the identity.
func Open(dir, sessionID string, redact func(string) string) (*Writer, error) {
	if err := validID(sessionID); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("création du dossier des sessions : %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("permissions du dossier des sessions : %w", err)
	}
	path := filepath.Join(dir, sessionID+".jsonl")
	if err := repair(path); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("ouverture de la session %s : %w", sessionID, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		f.Close()
		return nil, fmt.Errorf("permissions de la session %s : %w", sessionID, err)
	}
	return &Writer{id: sessionID, redact: redact, f: f}, nil
}

// repair cuts an existing file at the first unreadable line (same rules as
// Load) and makes sure it ends with a newline, so appended events stay visible.
func repair(path string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("lecture de la session : %w", err)
	}
	good, off := 0, 0
	for off < len(data) {
		next := len(data)
		if end := bytes.IndexByte(data[off:], '\n'); end >= 0 {
			next = off + end + 1
		}
		if t := bytes.TrimSpace(data[off:next]); len(t) > 0 {
			var e event.Event
			if json.Unmarshal(t, &e) != nil {
				break
			}
		}
		off = next
		good = next
	}
	if good < len(data) {
		// L'original complet est gardé à côté avant la troncature.
		if err := saveCorrupt(path+".corrupt", data); err != nil {
			return err
		}
		if err := os.Truncate(path, int64(good)); err != nil {
			return fmt.Errorf("réparation de la session : %w", err)
		}
	}
	if good > 0 && data[good-1] != '\n' {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return fmt.Errorf("réparation de la session : %w", err)
		}
		defer f.Close()
		if _, err := f.Write([]byte("\n")); err != nil {
			return fmt.Errorf("réparation de la session : %w", err)
		}
	}
	return nil
}

// saveCorrupt writes data to path (0600), replacing any previous copy.
func saveCorrupt(path string, data []byte) error {
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("copie de la session endommagée : %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("copie de la session endommagée : %w", err)
	}
	return nil
}

// Write persists e. Events of other sessions and AssistantDelta are ignored.
func (w *Writer) Write(e event.Event) error {
	if e.Session != w.id || e.Type == event.AssistantDelta {
		return nil
	}
	payload := e.Payload
	if len(payload) == 0 {
		payload = json.RawMessage("null")
	}
	text := string(payload)
	if w.redact != nil {
		text = w.redact(text)
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, []byte(text)); err != nil {
		return fmt.Errorf("charge utile invalide après masquage : %w", err)
	}
	e.Payload = json.RawMessage(buf.Bytes())
	out, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("sérialisation de l'événement : %w", err)
	}
	out = append(out, '\n')

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return errors.New("session fermée")
	}
	if _, err := w.f.Write(out); err != nil {
		return fmt.Errorf("écriture de la session : %w", err)
	}
	if err := w.f.Sync(); err != nil {
		return fmt.Errorf("fsync de la session : %w", err)
	}
	return nil
}

// Close closes the file.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}
