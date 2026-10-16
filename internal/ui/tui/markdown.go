package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// mdStyles : styles du rendu markdown des réponses de l'assistant.
type mdStyles struct {
	bold, code, heading lipgloss.Style
}

// renderMarkdown rend le sous-ensemble courant du markdown des modèles :
// titres (#), listes (-, *), blocs de code (```), **gras** et `code`.
// s est déjà nettoyé par sanitize : les seules séquences d'échappement du
// résultat sont celles des styles. Un marqueur non refermé (réponse en cours
// de streaming) reste affiché tel quel.
func renderMarkdown(s string, st mdStyles) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	inFence := false
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue // la ligne de clôture n'est pas affichée
		}
		if inFence {
			out = append(out, "  "+st.code.Render(l))
			continue
		}
		if h := headingText(trimmed); h != "" {
			out = append(out, st.heading.Render(inlineMarkdown(h, mdStyles{bold: st.heading, code: st.code})))
			continue
		}
		indent := l[:len(l)-len(strings.TrimLeft(l, " "))]
		rest := l[len(indent):]
		if strings.HasPrefix(rest, "- ") || strings.HasPrefix(rest, "* ") {
			out = append(out, indent+"• "+inlineMarkdown(rest[2:], st))
			continue
		}
		out = append(out, inlineMarkdown(l, st))
	}
	return strings.Join(out, "\n")
}

// headingText renvoie le texte d'un titre markdown (« ## Titre »), "" sinon.
func headingText(l string) string {
	n := 0
	for n < len(l) && n < 6 && l[n] == '#' {
		n++
	}
	if n == 0 || n >= len(l) || l[n] != ' ' {
		return ""
	}
	return strings.TrimSpace(l[n:])
}

// inlineMarkdown rend `code` puis **gras** hors des segments de code.
func inlineMarkdown(l string, st mdStyles) string {
	var b strings.Builder
	for {
		i := strings.IndexByte(l, '`')
		if i < 0 {
			break
		}
		j := strings.IndexByte(l[i+1:], '`')
		if j < 0 {
			break
		}
		b.WriteString(bold(l[:i], st))
		b.WriteString(st.code.Render(l[i+1 : i+1+j]))
		l = l[i+1+j+1:]
	}
	b.WriteString(bold(l, st))
	return b.String()
}

func bold(s string, st mdStyles) string {
	var b strings.Builder
	for {
		i := strings.Index(s, "**")
		if i < 0 {
			break
		}
		j := strings.Index(s[i+2:], "**")
		if j <= 0 { // non refermé, ou « **** » vide
			break
		}
		b.WriteString(s[:i])
		b.WriteString(st.bold.Render(s[i+2 : i+2+j]))
		s = s[i+2+j+2:]
	}
	b.WriteString(s)
	return b.String()
}
