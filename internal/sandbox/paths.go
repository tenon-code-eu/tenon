package sandbox

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// DefaultWrite renvoie les chemins inscriptibles par défaut : projet,
// temporaires, périphériques usuels, cache utilisateur et ~/.npm. goos vaut "linux" ou
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
	// npm et npx (serveurs MCP stdio courants) écrivent journaux et cache
	// sous ~/.npm et échouent sans lui.
	if home != "" {
		add(filepath.Join(home, ".npm"))
	}
	return out
}

// DefaultDenyRead renvoie les chemins interdits en lecture et en écriture : les
// secrets usuels du dossier personnel (dont le trousseau macOS
// ~/Library/Keychains, où tenon range les clés des fournisseurs ; absent sous
// Linux, donc ignoré), puis les dossiers de tenon et le
// fichier de politique. Les arguments vides sont ignorés.
func DefaultDenyRead(home, configDir, dataDir, policyPath string) []string {
	var out []string
	if home != "" {
		for _, p := range []string{
			".ssh", ".gnupg", ".aws", ".azure", ".config/gcloud",
			".kube", ".docker", ".netrc", ".password-store",
			"Library/Keychains",
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
// chemin interdit sans rapport avec root ou absent est ignoré ; si root est
// interdit ou se trouve sous un chemin interdit, le résultat est vide. Les
// liens symboliques voisins ne sont jamais accordés : une règle ouverte sur un
// lien viserait sa cible, potentiellement interdite ou hors de root. Le résultat est trié, en chemins absolus.
func splitAround(root string, deny []string) ([]string, error) {
	root = Canonical([]string{root})[0]
	var denied []string
	for _, d := range Canonical(deny) {
		if _, err := os.Lstat(d); err != nil {
			continue
		}
		if d == root || isUnder(d, root) {
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
		if e.Type()&fs.ModeSymlink != 0 {
			continue
		}
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

// denyAncestors renvoie les dossiers ancêtres de chaque chemin interdit
// existant, de son parent jusqu'à /, sans doublon. Seuls les vrais dossiers
// sont gardés, jamais un lien symbolique : les chemins sont canonisés avant.
// Ils reçoivent sous Linux le seul droit de lister (READ_DIR) pour que `ls /`
// ou `ls $HOME` fonctionnent malgré un interdit en dessous.
func denyAncestors(deny []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, d := range Canonical(deny) {
		if _, err := os.Lstat(d); err != nil {
			continue
		}
		for p := filepath.Dir(d); !seen[p]; p = filepath.Dir(p) {
			if fi, err := os.Lstat(p); err == nil && fi.IsDir() {
				seen[p] = true
				out = append(out, p)
			}
			if p == filepath.Dir(p) {
				break
			}
		}
	}
	return out
}

// GitDenyWrite renvoie les chemins de project/.git à interdire en écriture
// (Spec.DenyWrite), présents ou non : hooks et config (un hook ou un
// core.fsmonitor s'exécuterait hors bac à sable à la prochaine commande git
// de l'utilisateur), commondir (redirigerait git vers une copie piégée de
// .git) et config.worktree. Seul un dossier .git ordinaire est traité ; un
// fichier .git (worktree, sous-module) pointe hors du projet. Les dépôts
// imbriqués et les dossiers git des sous-modules (.git/modules) ne sont pas
// couverts.
func GitDenyWrite(project string) []string {
	git := filepath.Join(project, ".git")
	if fi, err := os.Lstat(git); err != nil || !fi.IsDir() {
		return nil
	}
	var out []string
	for _, name := range []string{"hooks", "config", "commondir", "config.worktree"} {
		out = append(out, filepath.Join(git, name))
	}
	return out
}
