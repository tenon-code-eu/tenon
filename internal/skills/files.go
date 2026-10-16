package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tenon-code-eu/tenon/internal/safefile"
	"github.com/tenon-code-eu/tenon/internal/tools"
)

const (
	// MaxListDepth est la profondeur maximale listée, SKILL.md étant à 1.
	MaxListDepth = 3
	// MaxListEntries est le nombre maximal de fichiers listés.
	MaxListEntries = 100

	// maxReadBytesUnlimited borne la lecture quand limit <= 0.
	maxReadBytesUnlimited = 1 << 20
)

func refused(reason string) error {
	return fmt.Errorf("skill file refused: %s", reason)
}

// skillDir renvoie le dossier du skill après avoir vérifié que c'est un vrai
// répertoire (pas un lien symbolique), comme Load.
func (c *Catalog) skillDir(name string) (string, error) {
	s, ok := c.Get(name)
	if !ok {
		return "", fmt.Errorf("unknown skill %q", name)
	}
	fi, err := os.Lstat(s.Dir)
	if err != nil {
		return "", fmt.Errorf("skill %q: folder unavailable: %w", name, err)
	}
	if !fi.IsDir() {
		return "", refused("skill folder is not a real directory")
	}
	return s.Dir, nil
}

// ReadFile lit un fichier du dossier du skill. rel est relatif au dossier ;
// les chemins absolus, « .. » et tout lien symbolique sont refusés. Le
// contenu est tronqué à limit caractères (0 = sans troncature) ; le sha256
// porte sur les octets lus sur disque.
func (c *Catalog) ReadFile(name, rel string, limit int) (content string, abs string, sha256hex string, err error) {
	dir, err := c.skillDir(name)
	if err != nil {
		return "", "", "", err
	}
	if rel == "" {
		return "", "", "", refused("empty path")
	}
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") {
		return "", "", "", refused("path escapes the skill folder (absolute path)")
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	for _, p := range parts {
		if p == ".." {
			return "", "", "", refused("path escapes the skill folder")
		}
	}
	cur := dir
	for i, p := range parts {
		if p == "" || p == "." {
			continue
		}
		cur = filepath.Join(cur, p)
		fi, err := os.Lstat(cur)
		if errors.Is(err, fs.ErrNotExist) {
			return "", "", "", fmt.Errorf("skill file not found: %s", rel)
		}
		if err != nil {
			return "", "", "", err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return "", "", "", refused("symbolic link")
		}
		if i < len(parts)-1 && !fi.IsDir() {
			return "", "", "", refused("not a directory in path")
		}
	}
	if cur == dir {
		return "", "", "", refused("not a regular file")
	}
	if tools.IsSensitive(cur) {
		return "", "", "", refused("sensitive file")
	}
	max := maxReadBytesUnlimited
	if limit > 0 {
		// 4 octets par rune au plus : assez pour détecter le dépassement.
		max = limit*4 + 1
	}
	data, _, ok, err := safefile.ReadRegular(cur, max)
	if err != nil {
		return "", "", "", err
	}
	if !ok {
		return "", "", "", refused("not a regular file or symbolic link")
	}
	sum := sha256.Sum256(data)
	return tools.Truncate(string(data), limit), cur, hex.EncodeToString(sum[:]), nil
}

// Files liste les fichiers du skill (chemins relatifs, triés, SKILL.md
// compris), sur MaxListDepth niveaux et MaxListEntries entrées au plus. Les
// liens symboliques sont ignorés et jamais suivis. truncated signale que la
// liste a été coupée.
func (c *Catalog) Files(name string) ([]string, bool, error) {
	dir, err := c.skillDir(name)
	if err != nil {
		return nil, false, err
	}
	var out []string
	truncated := false
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == dir {
				return err
			}
			return nil
		}
		if p == dir {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		depth := strings.Count(rel, string(filepath.Separator)) + 1
		if d.IsDir() {
			if depth >= MaxListDepth {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	sort.Strings(out)
	if len(out) > MaxListEntries {
		out, truncated = out[:MaxListEntries], true
	}
	return out, truncated, nil
}
