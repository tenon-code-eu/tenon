package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// maxToolName est la longueur maximale d'un nom d'outil exposé au modèle.
const maxToolName = 64

// ToolName construit le nom exposé au modèle : mcp__<serveur>__<outil>, les
// caractères hors [A-Za-z0-9_-] remplacés par « _ », 64 caractères au plus.
// En cas de collision avec un nom de taken, le nom est tronqué puis suffixé
// par « _<4 hex> » d'un hash. Le nom retenu est ajouté à taken (si non nil).
func ToolName(server, tool string, taken map[string]bool) string {
	base := safeName("mcp__" + server + "__" + tool)
	if len(base) > maxToolName {
		base = base[:maxToolName]
	}
	name := base
	for i := 0; taken[name]; i++ {
		seed := server + "\x00" + tool
		if i > 0 {
			seed += "\x00" + strconv.Itoa(i)
		}
		sum := sha256.Sum256([]byte(seed))
		suffix := "_" + hex.EncodeToString(sum[:2])
		prefix := base
		if len(prefix)+len(suffix) > maxToolName {
			prefix = prefix[:maxToolName-len(suffix)]
		}
		name = prefix + suffix
	}
	if taken != nil {
		taken[name] = true
	}
	return name
}

// safeName remplace chaque caractère hors [A-Za-z0-9_-] par « _ » (résultat ASCII).
func safeName(s string) string {
	return strings.Map(func(r rune) rune {
		if r < unicode.MaxASCII && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return r
		}
		return '_'
	}, s)
}

// cleanText retire les caractères de contrôle C0 (sauf saut de ligne), C1, DEL,
// les caractères de format Unicode (Cf : bidi, largeur nulle…) et les octets
// invalides ; tabulations et séparateurs de ligne Unicode deviennent des espaces.
func cleanText(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n':
			return r
		case r == '\t', r == ' ', r == ' ':
			return ' '
		case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f, r == unicode.ReplacementChar,
			unicode.Is(unicode.Cf, r):
			return -1
		}
		return r
	}, s)
}

// reURLQuery reconnaît la requête et le fragment d'une URL dans un texte.
var reURLQuery = regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://[^\s?#"']*)[?#][^\s"']*`)

// errText est le texte affichable d'une erreur : sans caractère de contrôle
// ni requête ou fragment d'URL (un identifiant pourrait s'y trouver).
func errText(err error) string {
	return cleanText(reURLQuery.ReplaceAllString(err.Error(), "${1}"))
}

// cutRunes coupe s à n caractères au plus.
func cutRunes(s string, n int) string {
	i := 0
	for j := range s {
		if i == n {
			return s[:j]
		}
		i++
	}
	return s
}
