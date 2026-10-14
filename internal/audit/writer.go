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

// Writer appends chained, fsynced lines. Safe for concurrent use.
type Writer struct {
	mu       sync.Mutex
	dir      string
	level    Level
	redact   func(string) string
	f        *os.File
	fname    string
	lastHash string
	closed   bool
}

// Open creates dir (0700) and resumes the chain from the last line of the
// most recent file. An unparsable last line is an error.
func Open(dir string, level Level, redact func(string) string) (*Writer, error) {
	if level != Full && level != Metadata {
		return nil, fmt.Errorf("niveau d'audit inconnu : %q", level)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("création du dossier d'audit : %w", err)
	}
	files, err := listFiles(dir)
	if err != nil {
		return nil, err
	}
	last := genesis
	for i := len(files) - 1; i >= 0; i-- {
		data, err := os.ReadFile(filepath.Join(dir, files[i]))
		if err != nil {
			return nil, fmt.Errorf("lecture de %s : %w", files[i], err)
		}
		data = bytes.TrimRight(data, "\n")
		if len(data) == 0 {
			continue
		}
		raw := data
		n := 1
		if idx := bytes.LastIndexByte(data, '\n'); idx >= 0 {
			raw = data[idx+1:]
			n = bytes.Count(data, []byte("\n")) + 1
		}
		var l line
		if err := json.Unmarshal(raw, &l); err != nil || l.Hash == "" {
			return nil, fmt.Errorf("%s:%d : dernière ligne du journal illisible, chaîne non reprise", files[i], n)
		}
		last = l.Hash
		break
	}
	return &Writer{dir: dir, level: level, redact: redact, lastHash: last}, nil
}

// Write appends e to the log and fsyncs. On error the chain is unchanged.
func (w *Writer) Write(e event.Event) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return errors.New("journal d'audit fermé")
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
	l := line{Seq: e.Seq, TS: string(bytes.Trim(tb, `"`)), Session: e.Session, Type: e.Type, Payload: pl, Prev: w.lastHash}
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

	name := ts.Format("2006-01-02") + ".jsonl"
	if w.f == nil || w.fname != name {
		if w.f != nil {
			if err := w.f.Close(); err != nil {
				return fmt.Errorf("fermeture de %s : %w", w.fname, err)
			}
			w.f = nil
		}
		f, err := os.OpenFile(filepath.Join(w.dir, name), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return fmt.Errorf("ouverture de %s : %w", name, err)
		}
		w.f, w.fname = f, name
	}
	if _, err := w.f.Write(out); err != nil {
		return fmt.Errorf("écriture du journal d'audit : %w", err)
	}
	if err := w.f.Sync(); err != nil {
		return fmt.Errorf("fsync du journal d'audit : %w", err)
	}
	w.lastHash = h
	return nil
}

// Close closes the current file.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
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
