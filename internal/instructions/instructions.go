// Package instructions charge AGENTS.md et TENON.md depuis la racine du
// projet pour les injecter dans le prompt système.
package instructions

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf8"
)

// MaxBytes est la taille maximale retenue pour chaque fichier d'instructions.
const MaxBytes = 32768

// names fixe l'ordre de chargement.
var names = []string{"AGENTS.md", "TENON.md"}

// File est un fichier d'instructions chargé.
type File struct {
	Name      string
	Content   string
	Truncated bool
}

// Load lit AGENTS.md puis TENON.md à la racine uniquement. Les fichiers
// absents ou non réguliers (liens symboliques inclus) sont ignorés : un dépôt
// non fiable ne doit pas pouvoir injecter un fichier extérieur au projet.
func Load(root string) ([]File, error) {
	var out []File
	for _, name := range names {
		f, ok, err := loadOne(filepath.Join(root, name), name)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, f)
		}
	}
	return out, nil
}

func loadOne(path, name string) (File, bool, error) {
	fi, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return File{}, false, nil
	}
	if err != nil {
		return File{}, false, err
	}
	if !fi.Mode().IsRegular() {
		return File{}, false, nil
	}
	// O_NOFOLLOW referme la fenêtre entre Lstat et ouverture (remplacement
	// par un lien symbolique) ; O_NONBLOCK évite de bloquer sur un FIFO échangé.
	fd, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) || errors.Is(err, fs.ErrNotExist) {
			return File{}, false, nil
		}
		return File{}, false, err
	}
	defer fd.Close()
	// On lit un octet de plus pour détecter le dépassement sans tout charger.
	data, err := io.ReadAll(io.LimitReader(fd, MaxBytes+1))
	if err != nil {
		return File{}, false, err
	}
	f := File{Name: name}
	if len(data) > MaxBytes {
		data = data[:MaxBytes]
		// Une rune coupée occupe au plus 3 octets de queue : on retire la
		// rune incomplète finale, sans toucher aux runes entières.
		if i := lastRuneStart(data); i >= 0 && !utf8.FullRune(data[i:]) {
			data = data[:i]
		}
		f.Truncated = true
	}
	f.Content = string(data)
	return f, true, nil
}

// Render ajoute une section par fichier à base ; sans fichier, base est
// renvoyé inchangé.
func Render(base string, files []File) string {
	if len(files) == 0 {
		return base
	}
	var b strings.Builder
	b.WriteString(base)
	for _, f := range files {
		b.WriteString("\n\n# Instructions du projet (" + f.Name + ")\n\n")
		b.WriteString(f.Content)
		if f.Truncated {
			b.WriteString("\n[… tronqué]")
		}
	}
	return b.String()
}

// lastRuneStart renvoie l'index du dernier octet de début de rune, ou -1.
func lastRuneStart(b []byte) int {
	for i := len(b) - 1; i >= 0 && i >= len(b)-utf8.UTFMax; i-- {
		if utf8.RuneStart(b[i]) {
			return i
		}
	}
	return -1
}
