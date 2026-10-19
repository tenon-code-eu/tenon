// Package checkpoint conserve le contenu des fichiers avant que l'agent les
// écrase, pour /rewind. Le stockage est adressé par le contenu : un fichier
// <2 premiers hex>/<sha256 hex>, écrit une seule fois.
package checkpoint

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tenon-code-eu/tenon/internal/config"
)

// MaxBytes : taille maximale d'une copie (10 Mio).
const MaxBytes = 10 << 20

var (
	ErrCorrupted = errors.New("corrupted checkpoint")
	ErrMissing   = errors.New("missing checkpoint")
)

// Store est un magasin de copies adressées par le contenu.
type Store struct{ dir string }

// Dir : <répertoire de données>/checkpoints.
func Dir(getenv func(string) string) string {
	return filepath.Join(config.DataDir(getenv), "checkpoints")
}

// Open crée le répertoire (0700) s'il n'existe pas.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

// validHash : 64 caractères hexadécimaux minuscules (jamais un chemin).
func validHash(h string) bool {
	if len(h) != 64 {
		return false
	}
	for _, c := range h {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func (s *Store) path(h string) string { return filepath.Join(s.dir, h[:2], h) }

// Save écrit data (s'il n'existe pas déjà intact) et renvoie son sha256 hex.
func (s *Store) Save(data []byte) (string, error) {
	if len(data) > MaxBytes {
		return "", errors.New("file too large")
	}
	sum := sha256.Sum256(data)
	h := hex.EncodeToString(sum[:])
	if s.Has(h) {
		return h, nil
	}
	d := filepath.Join(s.dir, h[:2])
	if err := os.MkdirAll(d, 0o700); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(d, ".tmp-*")
	if err != nil {
		return "", err
	}
	tmp := f.Name()
	fail := func(err error) (string, error) {
		f.Close()
		os.Remove(tmp)
		return "", err
	}
	if err := f.Chmod(0o600); err != nil {
		return fail(err)
	}
	if _, err := f.Write(data); err != nil {
		return fail(err)
	}
	if err := f.Sync(); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, s.path(h)); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return h, nil
}

// Load relit la copie et vérifie son empreinte.
func (s *Store) Load(h string) ([]byte, error) {
	if !validHash(h) {
		return nil, fmt.Errorf("%w: invalid hash", ErrMissing)
	}
	b, err := os.ReadFile(s.path(h))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrMissing
	}
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(b)
	if hex.EncodeToString(sum[:]) != h {
		return nil, ErrCorrupted
	}
	return b, nil
}

// Has indique si la copie existe et est intacte.
func (s *Store) Has(h string) bool {
	_, err := s.Load(h)
	return err == nil
}

// Prune supprime les fichiers plus vieux que maxAge (mtime) dont l'empreinte
// n'est pas dans keep, et les temporaires .tmp-* périmés.
func (s *Store) Prune(maxAge time.Duration, keep map[string]bool, now time.Time) (int, error) {
	removed := 0
	err := filepath.WalkDir(s.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if now.Sub(fi.ModTime()) <= maxAge {
			return nil
		}
		name := d.Name()
		if !strings.HasPrefix(name, ".tmp-") && keep[name] {
			return nil
		}
		if err := os.Remove(p); err != nil {
			return err
		}
		removed++
		return nil
	})
	return removed, err
}
