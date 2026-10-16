// Package instructions charge AGENTS.md et TENON.md depuis la racine du
// projet pour les injecter dans le prompt système.
package instructions

import (
	"path/filepath"
	"strings"

	"github.com/tenon-code-eu/tenon/internal/safefile"
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
	data, truncated, ok, err := safefile.ReadRegular(path, MaxBytes)
	if err != nil || !ok {
		return File{}, false, err
	}
	return File{Name: name, Content: string(data), Truncated: truncated}, true, nil
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
		b.WriteString("\n\n# Project instructions (" + f.Name + ")\n\n")
		b.WriteString(f.Content)
		if f.Truncated {
			b.WriteString("\n[… truncated]")
		}
	}
	return b.String()
}
