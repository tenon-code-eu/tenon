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
	"syscall"

	"codeberg.org/tenon/tenon/internal/event"
)

// ErrNoSession means no matching session exists.
var ErrNoSession = errors.New("aucune session trouvée")

// ErrInvalidID means a session ID is not of the form s_<12 hex digits>.
var ErrInvalidID = errors.New("identifiant de session invalide")

// ErrLocked means another process (or writer) already holds the session.
var ErrLocked = errors.New("session déjà ouverte")

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
	lock   *os.File
}

// Open creates dir (0700) and opens <dir>/<sessionID>.jsonl (0600) in append
// mode, so an existing session is continued. A nil redact is the identity.
func Open(dir, sessionID string, redact func(string) string) (*Writer, error) {
	l, err := Acquire(dir, sessionID)
	if err != nil {
		return nil, err
	}
	w, err := l.Open(redact)
	if err != nil {
		l.Release()
		return nil, err
	}
	return w, nil
}

// Lock is a held session lock. It is taken before reading a session to
// resume, so that no other process writes it meanwhile, then handed over to
// the Writer by Open.
type Lock struct {
	id   string
	path string
	f    *os.File
}

// Exists reports ErrInvalidID for a malformed ID and ErrNoSession when
// <dir>/<sessionID>.jsonl does not exist; it creates nothing.
func Exists(dir, sessionID string) error {
	if err := validID(sessionID); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(dir, sessionID+".jsonl")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrNoSession
		}
		return fmt.Errorf("session %s : %w", sessionID, err)
	}
	return nil
}

// Acquire creates dir (0700) and takes the session's lock (ErrLocked if
// another process or writer holds it).
func Acquire(dir, sessionID string) (*Lock, error) {
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
	lock, err := acquireLock(path + ".lock")
	if err != nil {
		return nil, fmt.Errorf("session %s : %w", sessionID, err)
	}
	return &Lock{id: sessionID, path: path, f: lock}, nil
}

// Release drops the lock if it was not handed over to a Writer.
func (l *Lock) Release() {
	if l.f != nil {
		releaseLock(l.f)
		l.f = nil
	}
}

// Open repairs the session file (see repair) and opens it in append mode;
// the returned Writer owns the lock. On error the lock is still held.
func (l *Lock) Open(redact func(string) string) (*Writer, error) {
	if l.f == nil {
		return nil, fmt.Errorf("session %s : verrou libéré", l.id)
	}
	// Le verrou est pris avant la réparation, qui tronque le fichier.
	if err := repair(l.path); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("ouverture de la session %s : %w", l.id, err)
	}
	if err := os.Chmod(l.path, 0o600); err != nil {
		f.Close()
		return nil, fmt.Errorf("permissions de la session %s : %w", l.id, err)
	}
	w := &Writer{id: l.id, redact: redact, f: f, lock: l.f}
	l.f = nil
	return w, nil
}

// acquireLock creates path (0600) and takes a non-blocking exclusive flock.
func acquireLock(path string) (*os.File, error) {
	l, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("verrou de la session : %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		l.Close()
		return nil, fmt.Errorf("permissions du verrou de la session : %w", err)
	}
	if err := syscall.Flock(int(l.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		l.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrLocked
		}
		return nil, fmt.Errorf("verrou de la session : %w", err)
	}
	return l, nil
}

// releaseLock drops the flock and closes the lock file.
func releaseLock(l *os.File) {
	_ = syscall.Flock(int(l.Fd()), syscall.LOCK_UN)
	_ = l.Close()
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
	releaseLock(w.lock)
	w.lock = nil
	return err
}
