// Package frontmatter lit l'en-tête YAML minimal des fichiers Markdown
// (SKILL.md, agents) : scalaires, listes, blocs `>` / `|`.
package frontmatter

import (
	"errors"
	"strings"
)

const (
	maxFrontmatterBytes = 8192
	maxFrontmatterLines = 200
)

// Fields regroupe les champs de premier niveau de l'en-tête. Chaque clé garde
// sa valeur brute et ses lignes de continuation ; le décodage se fait à la demande.
type Fields struct {
	raw map[string]rawField
}

type rawField struct {
	val  string
	cont []string
}

// Has indique si la clé est présente, même avec une valeur vide.
func (f Fields) Has(key string) bool {
	_, ok := f.raw[key]
	return ok
}

// String renvoie la valeur scalaire de la clé ("" si absente).
func (f Fields) String(key string) string {
	r, ok := f.raw[key]
	if !ok {
		return ""
	}
	return scalarValue(r.val, r.cont)
}

// List renvoie la valeur de la clé comme liste (nil si absente ou vide).
func (f Fields) List(key string) []string {
	r, ok := f.raw[key]
	if !ok {
		return nil
	}
	return listValue(r.val, r.cont)
}

// Bool renvoie vrai si la valeur de la clé est `true` (insensible à la casse).
func (f Fields) Bool(key string) bool {
	return strings.EqualFold(f.String(key), "true")
}

// Name renvoie le champ name.
func (f Fields) Name() string { return f.String("name") }

// Description renvoie le champ description.
func (f Fields) Description() string { return f.String("description") }

// Parse lit l'en-tête YAML minimal et renvoie les champs et le corps
// (contenu après la ligne `---` de fermeture).
// Seule la présence de name et description est vérifiée ici.
func Parse(data []byte) (f Fields, body string, err error) {
	text := strings.TrimPrefix(string(data), "\xef\xbb\xbf")
	text = strings.ReplaceAll(text, "\r\n", "\n")

	first, rest, _ := strings.Cut(text, "\n")
	if strings.TrimRight(first, " \t") != "---" {
		return f, "", errors.New("frontmatter missing: file must start with ---")
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
			return f, "", errors.New("frontmatter too long (more than 200 lines)")
		}
		if !found {
			break
		}
	}
	if !closed {
		return f, "", errors.New("frontmatter not closed: missing closing ---")
	}
	if len(strings.Join(lines, "\n")) > maxFrontmatterBytes {
		return f, "", errors.New("frontmatter too large (more than 8 KiB)")
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

		if f.raw == nil {
			f.raw = map[string]rawField{}
		}
		f.raw[key] = rawField{val: val, cont: cont}
	}

	if f.Name() == "" {
		return f, "", errors.New("frontmatter: missing name")
	}
	if f.Description() == "" {
		return f, "", errors.New("frontmatter: missing description")
	}
	return f, rest, nil
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
	if val != "" {
		if c := val[0]; (c == '>' || c == '|') && strings.Trim(val[1:], "+-0123456789") == "" {
			return blockScalar(c == '>', cont)
		}
	}
	// Scalaire simple ou entre guillemets sur plusieurs lignes : les lignes
	// de continuation sont jointes par une espace (sémantique YAML).
	parts := []string{}
	if val != "" {
		parts = append(parts, val)
	}
	for _, l := range cont {
		if l = strings.TrimSpace(l); l != "" {
			parts = append(parts, l)
		}
	}
	return unquote(strings.Join(parts, " "))
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
