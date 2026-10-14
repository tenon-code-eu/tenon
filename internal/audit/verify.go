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
	files, err := listFiles(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return genesis, 0, nil
		}
		return "", 0, fmt.Errorf("lecture du dossier d'audit : %w", err)
	}
	prev, total := genesis, 0
	for _, name := range files {
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			return "", total, fmt.Errorf("%s : ouverture impossible : %w", name, err)
		}
		r := bufio.NewReader(f)
		for n := 1; ; n++ {
			raw, rerr := r.ReadBytes('\n')
			if rerr != nil && rerr != io.EOF {
				f.Close()
				return "", total, fmt.Errorf("%s:%d : lecture impossible : %w", name, n, rerr)
			}
			if len(raw) == 0 {
				break
			}
			complete := rerr == nil
			raw = bytes.TrimSuffix(raw, []byte("\n"))
			var l line
			if err := json.Unmarshal(raw, &l); err != nil {
				f.Close()
				return "", total, fmt.Errorf("%s:%d : ligne illisible : %v", name, n, err)
			}
			if l.Prev != prev {
				f.Close()
				return "", total, fmt.Errorf("%s:%d : chaînage rompu (prev attendu %s, trouvé %s)", name, n, prev, l.Prev)
			}
			want, err := computeHash(l)
			if err != nil || want != l.Hash {
				f.Close()
				return "", total, fmt.Errorf("%s:%d : empreinte invalide, ligne altérée", name, n)
			}
			prev = l.Hash
			total++
			if !complete {
				break
			}
		}
		f.Close()
	}
	return prev, total, nil
}
