// Package skills découvre et charge les skills au format Agent Skills (SKILL.md).
package skills

import (
	"errors"
	"strings"
)

const (
	maxFrontmatterBytes = 8192
	maxFrontmatterLines = 200
)

// frontmatter regroupe les champs lus de l'en-tête d'un SKILL.md.
// Les autres champs sont ignorés.
type frontmatter struct {
	Name                   string
	Description            string
	DisableModelInvocation bool
	AllowedTools           []string
}

// parseFrontmatter lit l'en-tête YAML minimal d'un SKILL.md et renvoie
// les champs et le corps (contenu après la ligne `---` de fermeture).
// Seule la présence de name et description est vérifiée ici.
func parseFrontmatter(data []byte) (fm frontmatter, body string, err error) {
	text := strings.TrimPrefix(string(data), "\xef\xbb\xbf")
	text = strings.ReplaceAll(text, "\r\n", "\n")

	first, rest, _ := strings.Cut(text, "\n")
	if strings.TrimRight(first, " \t") != "---" {
		return fm, "", errors.New("frontmatter missing: file must start with ---")
	}

	var lines []string
	closed := false
	for {
		var line string
		var found bool
		line, rest, found = strings.Cut(rest, "\n")
		if strings.TrimRight(line, " \t") == "---" {
			closed = true
			break
		}
		lines = append(lines, line)
		if len(lines) > maxFrontmatterLines {
			return fm, "", errors.New("frontmatter too long (more than 200 lines)")
		}
		if !found {
			break
		}
	}
	if !closed {
		return fm, "", errors.New("frontmatter not closed: missing closing ---")
	}
	if len(strings.Join(lines, "\n")) > maxFrontmatterBytes {
		return fm, "", errors.New("frontmatter too large (more than 8 KiB)")
	}

	for i := 0; i < len(lines); {
		key, val, ok := splitKey(lines[i])
		i++
		if !ok {
			continue
		}
		// Lignes rattachées à la clé : indentées, vides, ou éléments `- x`.
		start := i
		for i < len(lines) && (lines[i] == "" || lines[i][0] == ' ' || lines[i][0] == '\t' ||
			(val == "" && strings.HasPrefix(lines[i], "- "))) {
			i++
		}
		cont := lines[start:i]

		switch key {
		case "name":
			fm.Name = scalarValue(val, cont)
		case "description":
			fm.Description = scalarValue(val, cont)
		case "disable-model-invocation":
			fm.DisableModelInvocation = strings.EqualFold(scalarValue(val, cont), "true")
		case "allowed-tools":
			fm.AllowedTools = listValue(val, cont)
		}
	}

	if fm.Name == "" {
		return fm, "", errors.New("frontmatter: missing name")
	}
	if fm.Description == "" {
		return fm, "", errors.New("frontmatter: missing description")
	}
	return fm, rest, nil
}

// splitKey découpe une ligne `clé: valeur` de premier niveau.
func splitKey(line string) (key, val string, ok bool) {
	k, v, found := strings.Cut(line, ":")
	if !found || k == "" {
		return "", "", false
	}
	for _, r := range k {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return "", "", false
		}
	}
	if v != "" && v[0] != ' ' && v[0] != '\t' {
		return "", "", false
	}
	return k, strings.TrimSpace(v), true
}

// scalarValue renvoie la valeur d'un scalaire : simple, entre guillemets
// ou bloc `>` / `|`. Une table imbriquée (valeur vide) donne "".
func scalarValue(val string, cont []string) string {
	if val == "" {
		return ""
	}
	if c := val[0]; (c == '>' || c == '|') && strings.Trim(val[1:], "+-0123456789") == "" {
		return blockScalar(c == '>', cont)
	}
	return unquote(val)
}

// blockScalar assemble un bloc `>` (lignes jointes par un espace) ou
// `|` (lignes conservées), en retirant l'indentation commune.
func blockScalar(folded bool, cont []string) string {
	indent := -1
	for _, l := range cont {
		if strings.TrimSpace(l) == "" {
			continue
		}
		n := len(l) - len(strings.TrimLeft(l, " \t"))
		if indent < 0 || n < indent {
			indent = n
		}
	}
	var out []string
	for _, l := range cont {
		if len(l) >= indent && indent > 0 {
			l = l[indent:]
		} else {
			l = strings.TrimSpace(l)
		}
		out = append(out, strings.TrimRight(l, " \t"))
	}
	sep := "\n"
	if folded {
		sep = " "
	}
	return strings.Trim(strings.Join(out, sep), " \n")
}

// unquote retire les guillemets simples ou doubles et un commentaire final.
func unquote(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if q := s[0]; q == '"' || q == '\'' {
		if end := closingQuote(s); end > 0 {
			inner := s[1:end]
			if q == '\'' {
				return strings.ReplaceAll(inner, "''", "'")
			}
			return strings.NewReplacer(`\"`, `"`, `\\`, `\`, `\n`, "\n", `\t`, "\t").Replace(inner)
		}
		return s
	}
	if i := strings.Index(s, " #"); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	return s
}

// closingQuote renvoie l'index du guillemet fermant de s (s[0] ouvre), ou -1.
func closingQuote(s string) int {
	q := s[0]
	for i := 1; i < len(s); i++ {
		switch {
		case q == '"' && s[i] == '\\':
			i++
		case q == '\'' && s[i] == '\'' && i+1 < len(s) && s[i+1] == '\'':
			i++
		case s[i] == q:
			return i
		}
	}
	return -1
}

// listValue lit une liste : `a, b`, `[a, "b"]` ou éléments `- x`.
func listValue(val string, cont []string) []string {
	var items []string
	if val == "" {
		for _, l := range cont {
			l = strings.TrimSpace(l)
			if rest, ok := strings.CutPrefix(l, "-"); ok {
				items = append(items, unquote(rest))
			}
		}
	} else {
		val = strings.TrimSpace(val)
		if strings.HasPrefix(val, "[") && strings.HasSuffix(val, "]") {
			val = val[1 : len(val)-1]
		}
		items = splitCommas(val)
	}
	var out []string
	for _, it := range items {
		if it != "" {
			out = append(out, it)
		}
	}
	return out
}

// splitCommas découpe sur les virgules hors guillemets.
func splitCommas(s string) []string {
	var out []string
	var cur strings.Builder
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
			cur.WriteByte(c)
		case c == '"' || c == '\'':
			quote = c
			cur.WriteByte(c)
		case c == ',':
			out = append(out, unquote(cur.String()))
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	return append(out, unquote(cur.String()))
}
