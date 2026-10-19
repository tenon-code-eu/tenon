package tui

import (
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/tenon-code-eu/tenon/internal/agents"
	"github.com/tenon-code-eu/tenon/internal/hooks"
	"github.com/tenon-code-eu/tenon/internal/i18n"
	"github.com/tenon-code-eu/tenon/internal/skills"
)

// cleanToken retire les caractères de contrôle et de format (Cf) du jeton.
func cleanToken(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, s)
}

// spaceClean remplace les caractères de contrôle et de format par des espaces
// (avant le regroupement des blancs : « a\nb » devient « a b »).
func spaceClean(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return ' '
		}
		return r
	}, s)
}

// skillNames renvoie les noms des skills (hors noms réservés aux commandes
// intégrées, inutilisables par /<nom>), dans l'ordre du catalogue.
func (m Model) skillNames() []string {
	if m.opts.Skills == nil {
		return nil
	}
	var out []string
	for _, s := range m.opts.Skills.List() {
		if !skills.IsBuiltin(s.Name) {
			out = append(out, s.Name)
		}
	}
	return out
}

// skillsNotices affiche /skills : un skill par ligne, ou « aucun skill ».
func (m *Model) skillsNotices() {
	var list []skills.Skill
	if m.opts.Skills != nil {
		list = m.opts.Skills.List()
	}
	if len(list) == 0 {
		m.tr.notice(i18n.T(m.lang, "skills.none"))
		return
	}
	for _, s := range list {
		m.tr.notice(fmt.Sprintf("/%s (%s) — %s", s.Name, s.Source, s.Description))
	}
}

// complete complète la saisie quand c'est un seul jeton « /... » et que le
// curseur est en fin de texte : candidat unique (+ espace) ou plus long
// préfixe commun ; sans correspondance, rien ne change.
func (m *Model) complete() {
	v := m.input.Value()
	if !strings.HasPrefix(v, "/") || strings.ContainsFunc(v, unicode.IsSpace) {
		return
	}
	li := m.input.LineInfo()
	if m.input.Line() != 0 || li.StartColumn+li.ColumnOffset != len([]rune(v)) {
		return
	}
	prefix := v[1:]
	var matches []string
	for _, n := range append(slices.Clone(skills.Builtins), m.skillNames()...) {
		if strings.HasPrefix(n, prefix) && !slices.Contains(matches, n) {
			matches = append(matches, n)
		}
	}
	switch len(matches) {
	case 0:
		return
	case 1:
		m.input.SetValue("/" + matches[0] + " ")
		return
	}
	common := matches[0]
	for _, n := range matches[1:] {
		for !strings.HasPrefix(n, common) {
			common = common[:len(common)-1]
		}
	}
	m.input.SetValue("/" + common)
}

// agentsNotices affiche /agents : un agent par ligne « nom  source  description »
// (description sur une ligne), ou « aucun agent ».
func (m *Model) agentsNotices() {
	var list []agents.Agent
	if m.opts.Agents != nil {
		list = m.opts.Agents.List()
	}
	if len(list) == 0 {
		m.tr.notice(i18n.T(m.lang, "agents.none"))
		return
	}
	for _, a := range list {
		desc := strings.Join(strings.Fields(spaceClean(a.Description)), " ")
		m.tr.notice(cleanToken(a.Name) + "  " + cleanToken(string(a.Source)) + "  " + desc)
	}
}

// hooksNotices affiche /hooks comme « tenon hooks » : un hook par ligne
// « id  matcher  type  commande ou prompt » (80 caractères au plus), ou
// « aucun hook ».
func (m *Model) hooksNotices() {
	var list []hooks.Hook
	if m.opts.Hooks != nil {
		list = m.opts.Hooks.List()
	}
	if len(list) == 0 {
		m.tr.notice(i18n.T(m.lang, "hooks.none"))
		return
	}
	for _, h := range list {
		matcher := h.Matcher
		if matcher == "" {
			matcher = "*"
		}
		text := h.Command
		if h.Type != hooks.Command {
			text = h.Prompt
		}
		text = strings.Join(strings.Fields(spaceClean(text)), " ")
		if r := []rune(text); len(r) > hookTextMax {
			text = string(r[:hookTextMax])
		}
		m.tr.notice(cleanToken(h.ID) + "  " + cleanToken(matcher) + "  " + cleanToken(string(h.Type)) + "  " + text)
	}
}

// hookTextMax borne l'aperçu d'une commande ou d'un prompt dans /hooks.
const hookTextMax = 80
