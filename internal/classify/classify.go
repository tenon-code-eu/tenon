// Package classify reconnaît les chemins, marqueurs de contenu et commandes
// shell classifiés (données sensibles) d'après les règles de configuration.
package classify

import (
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/tenon-code-eu/tenon/internal/policy"
)

// BuiltinMarkers sont les marqueurs de classification toujours actifs.
var BuiltinMarkers = []string{"DIFFUSION RESTREINTE", "SPÉCIAL FRANCE", "SPECIAL FRANCE", "RESTREINT UE", "EU RESTRICTED"}

// Rules regroupe les motifs de chemins et les marqueurs définis par
// l'utilisateur ou la politique. Les marqueurs intégrés s'ajoutent toujours.
type Rules struct {
	Paths, Markers []string
}

// Empty indique l'absence de chemins et de marqueurs utilisateur.
func (r Rules) Empty() bool { return len(r.Paths) == 0 && len(r.Markers) == 0 }

// resolve rend le chemin absolu, nettoyé, avec les liens symboliques résolus.
// Si le chemin n'existe pas, le plus proche ancêtre existant est résolu et le
// reste est rajouté tel quel.
func resolve(root, path string) string {
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	path = filepath.Clean(path)
	rest := ""
	cur := path
	for {
		if real, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(real, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return path
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

// MatchPath rapporte le motif qui classifie path (absolu ou relatif à root).
// Les motifs relatifs s'appliquent au chemin relatif à la racine résolue ; un
// chemin hors du projet ne correspond jamais à un motif relatif.
func (r Rules) MatchPath(root, path string) (string, bool) {
	if len(r.Paths) == 0 {
		return "", false
	}
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	root = filepath.Clean(root)
	// Chemin résolu (liens symboliques) et chemin lexical : un lien vers
	// l'extérieur du projet ne doit pas échapper au motif.
	lex := path
	if !filepath.IsAbs(lex) {
		lex = filepath.Join(root, lex)
	}
	cands := []string{resolve(root, path), filepath.Clean(lex)}
	for _, pat := range r.Paths {
		for _, abs := range cands {
			if r.matchOne(root, abs, pat) {
				return pat, true
			}
		}
	}
	return "", false
}

func (r Rules) matchOne(root, abs, pat string) bool {
	if filepath.IsAbs(pat) {
		return globMatch(splitSegs(filepath.Clean(pat)), splitSegs(abs))
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return globMatch(splitSegs(strings.TrimPrefix(filepath.ToSlash(pat), "./")), splitSegs(rel))
}

func splitSegs(p string) []string {
	var out []string
	for _, s := range strings.Split(filepath.ToSlash(p), "/") {
		if s != "" && s != "." {
			// casse et accents repliés : volumes insensibles à la casse, NFC/NFD
			out = append(out, fold(s))
		}
	}
	return out
}

// globMatch compare segment par segment : `**` couvre zéro ou plusieurs
// segments, `*` reste dans un segment (via policy.MatchWildcard).
func globMatch(pat, segs []string) bool {
	if len(pat) == 0 {
		return len(segs) == 0
	}
	if pat[0] == "**" {
		for i := 0; i <= len(segs); i++ {
			if globMatch(pat[1:], segs[i:]) {
				return true
			}
		}
		return false
	}
	if len(segs) == 0 || !policy.MatchWildcard(pat[0], segs[0]) {
		return false
	}
	return globMatch(pat[1:], segs[1:])
}

// FindMarker cherche un marqueur dans text, sans tenir compte de la casse,
// des accents ni des espaces multiples, avec frontières de mot. Il rend le
// marqueur tel que configuré.
func (r Rules) FindMarker(text string) (string, bool) {
	ft := fold(text)
	if ft == "" {
		return "", false
	}
	try := func(m string) bool {
		fm := fold(m)
		if fm == "" {
			return false
		}
		for from := 0; from < len(ft); {
			i := strings.Index(ft[from:], fm)
			if i < 0 {
				return false
			}
			start, end := from+i, from+i+len(fm)
			if boundary(ft, start, end) {
				return true
			}
			from = start + 1
		}
		return false
	}
	for _, m := range r.Markers {
		if try(m) {
			return m, true
		}
	}
	for _, m := range BuiltinMarkers {
		if try(m) {
			return m, true
		}
	}
	return "", false
}

// boundary vérifie que les caractères voisins de s[start:end] ne sont ni
// lettres ni chiffres.
func boundary(s string, start, end int) bool {
	isWord := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }
	if start > 0 {
		r := []rune(s[:start])
		if isWord(r[len(r)-1]) {
			return false
		}
	}
	if end < len(s) {
		for _, c := range s[end:] {
			return !isWord(c)
		}
	}
	return true
}

// MatchCommand découpe cmd en mots et teste ceux qui ressemblent à un chemin
// (contient `/` ou existe relativement à root).
func (r Rules) MatchCommand(root, cmd string) (string, bool) {
	_, pat, ok := r.matchCommand(root, cmd)
	return pat, ok
}

// CommandPath est MatchCommand, mais rend le mot de la commande classé (le
// chemin tel qu'écrit) plutôt que le motif.
func (r Rules) CommandPath(root, cmd string) (string, bool) {
	w, _, ok := r.matchCommand(root, cmd)
	return w, ok
}

func (r Rules) matchCommand(root, cmd string) (word, pat string, ok bool) {
	if len(r.Paths) == 0 {
		return "", "", false
	}
	for _, w := range splitWords(cmd) {
		if !strings.Contains(w, "/") {
			if _, err := os.Lstat(filepath.Join(root, w)); err != nil {
				continue
			}
		}
		if pat, ok := r.MatchPath(root, w); ok {
			return w, pat, true
		}
	}
	return "", "", false
}

// splitWords sépare sur les espaces (hors guillemets) et sur `= < > ; | & ( )`,
// guillemets retirés.
func splitWords(cmd string) []string {
	var words []string
	var cur strings.Builder
	var quote rune
	flush := func() {
		if cur.Len() > 0 {
			words = append(words, cur.String())
			cur.Reset()
		}
	}
	for _, c := range cmd {
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			} else if strings.ContainsRune("=<>;|&()", c) {
				flush()
			} else {
				cur.WriteRune(c)
			}
		case c == '"' || c == '\'':
			quote = c
		case unicode.IsSpace(c) || strings.ContainsRune("=<>;|&()", c):
			flush()
		default:
			cur.WriteRune(c)
		}
	}
	flush()
	return words
}
