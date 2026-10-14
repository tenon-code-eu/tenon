// Package audit writes a hash-chained JSONL audit log and verifies it.
package audit

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"

	"codeberg.org/tenon/tenon/internal/event"
)

// Level selects how much of each payload is stored.
type Level string

const (
	Full     Level = "full"
	Metadata Level = "metadata"
)

const genesis = "sha256:0"

var _ event.Sink = (*Writer)(nil)

// line is the canonical on-disk record; field order is part of the hash.
type line struct {
	Seq     uint64          `json:"seq"`
	TS      string          `json:"ts"`
	Session string          `json:"session"`
	Type    event.Type      `json:"type"`
	Payload json.RawMessage `json:"payload"`
	Prev    string          `json:"prev"`
	Hash    string          `json:"hash,omitempty"`
}

func computeHash(l line) (string, error) {
	l.Hash = ""
	b, err := json.Marshal(l)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	h.Write([]byte(l.Prev))
	h.Write(b)
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// Writer appends chained, fsynced lines. Safe for concurrent use, and across
// processes sharing the same dir (exclusive flock on <dir>/.lock per Write;
// the chain tail is re-read from disk inside the lock).
type Writer struct {
	mu     sync.Mutex
	dir    string
	level  Level
	redact func(string) string
	lock   *os.File
	broken error
	closed bool

	// test seams
	writeFn func(*os.File, []byte) (int, error)
	syncFn  func(*os.File) error
}

// Open creates dir (0700, chmod'ed if it already exists), chmods existing
// *.jsonl files to 0600 and checks the chain tail. An unparsable last line is an error.
func Open(dir string, level Level, redact func(string) string) (*Writer, error) {
	if level != Full && level != Metadata {
		return nil, fmt.Errorf("niveau d'audit inconnu : %q", level)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("création du dossier d'audit : %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("permissions du dossier d'audit : %w", err)
	}
	// Des journaux créés par une version antérieure ou à la main peuvent être
	// lisibles par d'autres : on les ramène à 0600.
	files, err := listFiles(dir)
	if err != nil {
		return nil, fmt.Errorf("lecture du dossier d'audit : %w", err)
	}
	for _, f := range files {
		if err := os.Chmod(filepath.Join(dir, f), 0o600); err != nil {
			return nil, fmt.Errorf("permissions du journal d'audit %s : %w", f, err)
		}
	}
	if _, _, err := readTail(dir); err != nil {
		return nil, err
	}
	lk, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("ouverture du verrou d'audit : %w", err)
	}
	return &Writer{
		dir: dir, level: level, redact: redact, lock: lk,
		writeFn: func(f *os.File, b []byte) (int, error) { return f.Write(b) },
		syncFn:  func(f *os.File) error { return f.Sync() },
	}, nil
}

// readTail returns the newest file name ("" if none) and the hash of the
// last line of the chain (genesis if none).
func readTail(dir string) (string, string, error) {
	files, err := listFiles(dir)
	if err != nil {
		return "", "", fmt.Errorf("lecture du dossier d'audit : %w", err)
	}
	newest := ""
	if len(files) > 0 {
		newest = files[len(files)-1]
	}
	for i := len(files) - 1; i >= 0; i-- {
		path := filepath.Join(dir, files[i])
		raw, err := lastLine(path)
		if err != nil {
			return "", "", fmt.Errorf("lecture de %s : %w", files[i], err)
		}
		if len(raw) == 0 {
			continue
		}
		var l line
		if err := json.Unmarshal(raw, &l); err != nil || l.Hash == "" {
			n := 0
			if data, rerr := os.ReadFile(path); rerr == nil {
				n = bytes.Count(bytes.TrimRight(data, "\n"), []byte("\n")) + 1
			}
			return "", "", fmt.Errorf("%s:%d : dernière ligne du journal illisible, chaîne non reprise", files[i], n)
		}
		return newest, l.Hash, nil
	}
	return newest, genesis, nil
}

// lastLine returns the last non-empty line of the file without reading it all.
func lastLine(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := st.Size()
	chunk := int64(4096)
	for {
		start := size - chunk
		if start < 0 {
			start = 0
		}
		buf := make([]byte, size-start)
		if _, err := f.ReadAt(buf, start); err != nil {
			return nil, err
		}
		buf = bytes.TrimRight(buf, "\n")
		if i := bytes.LastIndexByte(buf, '\n'); i >= 0 {
			return buf[i+1:], nil
		}
		if start == 0 {
			return buf, nil
		}
		chunk *= 2
	}
}

// Write appends e to the log and fsyncs. After a failed write the Writer is
// broken and every later Write fails (fail closed).
func (w *Writer) Write(e event.Event) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return errors.New("journal d'audit fermé")
	}
	if w.broken != nil {
		return fmt.Errorf("journal d'audit inutilisable après erreur : %w", w.broken)
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
	pl := json.RawMessage(buf.Bytes())
	if w.level == Metadata {
		sum := sha256.Sum256(buf.Bytes())
		m, err := json.Marshal(struct {
			SHA256 string `json:"sha256"`
			Size   int    `json:"size"`
		}{hex.EncodeToString(sum[:]), buf.Len()})
		if err != nil {
			return err
		}
		pl = m
	}
	ts := e.TS.UTC()
	// encoding/json's own time formatting (RFC3339Nano) keeps ts canonical.
	tb, err := json.Marshal(ts)
	if err != nil {
		return err
	}

	if err := syscall.Flock(int(w.lock.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("verrou du journal d'audit : %w", err)
	}
	defer syscall.Flock(int(w.lock.Fd()), syscall.LOCK_UN)

	newest, last, err := readTail(w.dir)
	if err != nil {
		return err
	}
	l := line{Seq: e.Seq, TS: string(bytes.Trim(tb, `"`)), Session: e.Session, Type: e.Type, Payload: pl, Prev: last}
	h, err := computeHash(l)
	if err != nil {
		return err
	}
	l.Hash = h
	out, err := json.Marshal(l)
	if err != nil {
		return err
	}
	out = append(out, '\n')

	// The file name never goes backwards, whatever the clock does.
	name := ts.Format("2006-01-02") + ".jsonl"
	if newest > name {
		name = newest
	}
	f, err := os.OpenFile(filepath.Join(w.dir, name), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("ouverture de %s : %w", name, err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return fmt.Errorf("lecture de %s : %w", name, err)
	}
	size := st.Size()
	werr := func() error {
		if _, err := w.writeFn(f, out); err != nil {
			return fmt.Errorf("écriture du journal d'audit : %w", err)
		}
		if err := w.syncFn(f); err != nil {
			return fmt.Errorf("fsync du journal d'audit : %w", err)
		}
		return nil
	}()
	if werr != nil {
		if terr := f.Truncate(size); terr != nil {
			werr = fmt.Errorf("%w (retour arrière impossible : %v)", werr, terr)
		}
		w.broken = werr
		return werr
	}
	return nil
}

// Close releases the lock file.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	if w.lock == nil {
		return nil
	}
	err := w.lock.Close()
	w.lock = nil
	return err
}

func listFiles(dir string) ([]string, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range ents {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".jsonl" {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}
