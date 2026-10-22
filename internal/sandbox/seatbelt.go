package sandbox

import (
	"os"
	"path/filepath"
	"strings"
)

// seatbeltProfile génère le profil SBPL de sandbox-exec. L'ordre compte : la
// dernière règle qui correspond l'emporte, donc les interdits viennent après
// les autorisations d'écriture. Les chemins sont supposés déjà canoniques.
func seatbeltProfile(s Spec) string {
	var b strings.Builder
	b.WriteString("(version 1)\n(allow default)\n(deny file-write*)\n")
	if len(s.Write) > 0 {
		b.WriteString("(allow file-write*")
		for _, p := range s.Write {
			b.WriteString(" " + sbplPath(p))
		}
		b.WriteString(")\n")
	}
	if len(s.DenyWrite) > 0 {
		paths, parents := denyWriteRules(s.DenyWrite)
		b.WriteString("(deny file-write*")
		for _, r := range paths {
			b.WriteString(" " + r)
		}
		b.WriteString(")\n")
		// Parents : ni suppression ni renommage (mv .git ailleurs, modifier,
		// remettre), mais dates et droits modifiables (touch, tar, rsync).
		b.WriteString("(deny file-write-unlink")
		for _, r := range parents {
			b.WriteString(" " + r)
		}
		b.WriteString(")\n")
	}
	if len(s.DenyRead) > 0 {
		b.WriteString("(deny file-read* file-write*")
		for _, p := range s.DenyRead {
			b.WriteString(` (subpath "` + sbplEscape(p) + `")`)
		}
		b.WriteString(")\n")
	}
	if !s.Network {
		b.WriteString("(deny network*)\n")
	}
	return b.String()
}

// sbplPath formate un chemin inscriptible : fichier de périphérique en
// (literal ...), le reste en (subpath ...).
func sbplPath(p string) string {
	if strings.HasPrefix(p, "/dev/") {
		return `(literal "` + sbplEscape(p) + `")`
	}
	return `(subpath "` + sbplEscape(p) + `")`
}

// sbplEscape échappe une chaîne SBPL entre guillemets.
func sbplEscape(p string) string {
	p = strings.ReplaceAll(p, `\`, `\\`)
	return strings.ReplaceAll(p, `"`, `\"`)
}

// denyWriteRules renvoie les filtres SBPL des chemins interdits en écriture
// et ceux de leurs dossiers parents. Un fichier existant est en
// (literal ...) ; un dossier ou un chemin absent en (subpath ...), pour
// couvrir aussi ce qu'on créerait dessous (mkdir .git/hooks puis un hook).
// Chaque dossier parent, jusqu'à /, est renvoyé à part en (literal ...).
func denyWriteRules(paths []string) (rules, parents []string) {
	seen := map[string]bool{}
	for _, p := range paths {
		if seen[p] {
			continue
		}
		seen[p] = true
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			rules = append(rules, `(literal "`+sbplEscape(p)+`")`)
		} else {
			rules = append(rules, `(subpath "`+sbplEscape(p)+`")`)
		}
	}
	for _, p := range paths {
		for d := filepath.Dir(p); !seen[d]; d = filepath.Dir(d) {
			seen[d] = true
			parents = append(parents, `(literal "`+sbplEscape(d)+`")`)
			if d == filepath.Dir(d) {
				break
			}
		}
	}
	return rules, parents
}
