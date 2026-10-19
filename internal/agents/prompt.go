package agents

import (
	"strings"

	"github.com/tenon-code-eu/tenon/internal/i18n"
)

// maxPromptDescChars est la longueur des descriptions quand le budget est dépassé.
const maxPromptDescChars = 200

// PromptSection renvoie la section « agents disponibles » du prompt système.
// Au-delà de budget octets, les descriptions sont coupées à 200 caractères,
// puis les derniers agents sont retirés (dropped).
func (c *Catalog) PromptSection(lang i18n.Lang, budget int) (section string, dropped int) {
	list := c.List()
	header := i18n.T(lang, "agents.prompt_header")
	build := func(n, max int) string {
		var b strings.Builder
		b.WriteString(header)
		for _, a := range list[:n] {
			d := strings.Join(strings.Fields(a.Description), " ")
			if r := []rune(d); max > 0 && len(r) > max {
				d = string(r[:max])
			}
			b.WriteString("\n- " + a.Name + ": " + d)
		}
		return b.String()
	}
	if out := build(len(list), 0); len(out) <= budget {
		return out, 0
	}
	n := len(list)
	out := build(n, maxPromptDescChars)
	for len(out) > budget && n > 0 {
		n--
		out = build(n, maxPromptDescChars)
	}
	if n == 0 {
		return "", len(list)
	}
	return out, len(list) - n
}

// SystemPrompt assemble le prompt système d'un sous-agent : base, section des
// skills (si withSkills), préambule, puis le corps de l'agent.
func SystemPrompt(lang i18n.Lang, base, skillsSection string, a Agent, withSkills bool) string {
	parts := []string{base}
	if withSkills && skillsSection != "" {
		parts = append(parts, skillsSection)
	}
	parts = append(parts, i18n.T(lang, "agents.preamble"))
	if a.Body != "" {
		parts = append(parts, a.Body)
	}
	return strings.Join(parts, "\n\n")
}
