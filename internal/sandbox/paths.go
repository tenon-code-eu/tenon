package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// DefaultWrite renvoie les chemins inscriptibles par défaut : projet,
// temporaires, périphériques usuels et cache utilisateur. goos vaut "linux" ou
// "darwin" ; toute autre valeur se comporte comme linux. Les valeurs vides
// (TMPDIR absent, home inconnu) ne produisent aucune entrée.
func DefaultWrite(project, home string, getenv func(string) string, goos string) []string {
	var out []string
	add := func(p string) {
		if p != "" {
			out = append(out, p)
		}
	}
	add(project)
	add(getenv("TMPDIR"))
	add("/tmp")
	if goos == "darwin" {
		add("/private/tmp")
		add("/private/var/folders")
	}
	for _, d := range []string{"null", "tty", "zero", "random", "urandom"} {
		add("/dev/" + d)
	}
	xdg := getenv("XDG_CACHE_HOME")
	add(xdg)
	switch {
	case goos == "darwin":
		if home != "" {
			add(filepath.Join(home, "Library", "Caches"))
		}
	case xdg == "" && home != "":
		add(filepath.Join(home, ".cache"))
	}
	return out
}

// DefaultDenyRead renvoie les chemins interdits en lecture et en écriture : les
// secrets usuels du dossier personnel, puis les dossiers de tenon et le
// fichier de politique. Les arguments vides sont ignorés.
func DefaultDenyRead(home, configDir, dataDir, policyPath string) []string {
	var out []string
	if home != "" {
		for _, p := range []string{
			".ssh", ".gnupg", ".aws", ".azure", ".config/gcloud",
			".kube", ".docker", ".netrc", ".password-store",
		} {
			out = append(out, filepath.Join(home, p))
		}
	}
	for _, p := range []string{configDir, dataDir, policyPath} {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Canonical résout les liens symboliques des chemins existants et nettoie les
// autres. Les doublons sont retirés, l'ordre est conservé.
func Canonical(paths []string) []string {
	seen := make(map[string]bool, len(paths))
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		c := filepath.Clean(p)
		if r, err := filepath.EvalSymlinks(c); err == nil {
			c = r
		}
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}

// splitAround renvoie les entrées à autoriser sous root pour que tout soit
// couvert sauf deny. On descend de root jusqu'à chaque chemin interdit et on
// ajoute chaque voisin à chaque niveau, jamais le composant interdit. Un
// chemin interdit hors de root ou absent est ignoré ; si root lui-même est
// interdit, le résultat est vide. Le résultat est trié, en chemins absolus.
func splitAround(root string, deny []string) ([]string, error) {
	root = Canonical([]string{root})[0]
	var denied []string
	for _, d := range Canonical(deny) {
		if _, err := os.Lstat(d); err != nil {
			continue
		}
		if d == root {
			return nil, nil
		}
		if isUnder(root, d) {
			denied = append(denied, d)
		}
	}
	if len(denied) == 0 {
		return []string{root}, nil
	}
	var out []string
	if err := expand(root, denied, &out); err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// expand ajoute à out les voisins de dir qui ne mènent à aucun chemin interdit
// et descend dans ceux qui y mènent.
func expand(dir string, denied []string, out *[]string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read %s: %w", dir, err)
	}
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		var inside []string
		skip := false
		for _, d := range denied {
			if d == p {
				skip = true
				break
			}
			if isUnder(p, d) {
				inside = append(inside, d)
			}
		}
		switch {
		case skip:
		case len(inside) > 0:
			if err := expand(p, inside, out); err != nil {
				return err
			}
		default:
			*out = append(*out, p)
		}
	}
	return nil
}

// isUnder indique si p est strictement sous dir.
func isUnder(dir, p string) bool {
	rel, err := filepath.Rel(dir, p)
	if err != nil || rel == "." || rel == ".." {
		return false
	}
	return len(rel) < 3 || rel[:3] != ".."+string(filepath.Separator)
}
