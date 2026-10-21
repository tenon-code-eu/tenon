package audit

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Verify walks all *.jsonl files in name order and checks the hash chain.
// It returns the last hash and the number of lines. A missing or empty
// dir yields ("sha256:0", 0, nil). Errors name <file>:<line>.
func Verify(dir string) (string, int, error) {
	return walk(dir, nil)
}

// walk applique les règles de Verify ; fn (optionnel) reçoit chaque ligne
// validée avec son fichier et son numéro, dans l'ordre de la chaîne.
func walk(dir string, fn func(file string, n int, l Line)) (string, int, error) {
	files, err := listFiles(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return genesis, 0, nil
		}
		return "", 0, fmt.Errorf("reading audit directory: %w", err)
	}
	prev, total := genesis, 0
	for _, name := range files {
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			return "", total, fmt.Errorf("%s: cannot open: %w", name, err)
		}
		r := bufio.NewReader(f)
		for n := 1; ; n++ {
			raw, rerr := r.ReadBytes('\n')
			if rerr != nil && rerr != io.EOF {
				f.Close()
				return "", total, fmt.Errorf("%s:%d: cannot read: %w", name, n, rerr)
			}
			if len(raw) == 0 {
				break
			}
			complete := rerr == nil
			raw = bytes.TrimSuffix(raw, []byte("\n"))
			var l Line
			if err := json.Unmarshal(raw, &l); err != nil {
				f.Close()
				return "", total, fmt.Errorf("%s:%d: unreadable line: %v", name, n, err)
			}
			if l.Prev != prev {
				f.Close()
				return "", total, fmt.Errorf("%s:%d: broken chain (expected prev %s, found %s)", name, n, prev, l.Prev)
			}
			want, err := computeHash(l)
			if err != nil || want != l.Hash {
				f.Close()
				return "", total, fmt.Errorf("%s:%d: invalid hash, line tampered", name, n)
			}
			l.Hash = want
			if re, err := json.Marshal(l); err != nil || !bytes.Equal(re, raw) {
				f.Close()
				return "", total, fmt.Errorf("%s:%d: non-canonical line", name, n)
			}
			prev = l.Hash
			total++
			if fn != nil {
				fn(name, n, l)
			}
			if !complete {
				break
			}
		}
		f.Close()
	}
	return prev, total, nil
}
