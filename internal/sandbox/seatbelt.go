package sandbox

import (
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
