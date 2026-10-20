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

// ReadLines parcourt en lecture seule les fichiers *.jsonl de dir dans l'ordre
// de la chaîne et appelle fn pour chaque ligne non vide (numérotées dès 1).
// Une ligne illisible est transmise avec err non nil et l à zéro. Un retour
// non nil de fn arrête le parcours et est renvoyé. Un dossier absent n'est pas
// une erreur : aucun appel, nil.
func ReadLines(dir string, fn func(file string, lineNo int, l Line, err error) error) error {
	files, err := listFiles(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("reading audit directory: %w", err)
	}
	for _, name := range files {
		if err := readFile(dir, name, fn); err != nil {
			return err
		}
	}
	return nil
}

func readFile(dir, name string, fn func(string, int, Line, error) error) error {
	f, err := os.Open(filepath.Join(dir, name))
	if err != nil {
		return fmt.Errorf("%s: cannot open: %w", name, err)
	}
	defer f.Close()
	r := bufio.NewReader(f)
	for n := 1; ; n++ {
		raw, rerr := r.ReadBytes('\n')
		if rerr != nil && rerr != io.EOF {
			return fmt.Errorf("%s:%d: cannot read: %w", name, n, rerr)
		}
		raw = bytes.TrimSuffix(raw, []byte("\n"))
		if len(bytes.TrimSpace(raw)) > 0 {
			var l Line
			uerr := json.Unmarshal(raw, &l)
			if uerr != nil {
				l = Line{}
			}
			if err := fn(name, n, l, uerr); err != nil {
				return err
			}
		}
		if rerr == io.EOF {
			return nil
		}
	}
}
