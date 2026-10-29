package skills

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/tenon-code-eu/tenon/internal/i18n"
)

// maxPromptDescChars est la longueur des descriptions quand le budget est dépassé.
const maxPromptDescChars = 200

// ErrUnknownCommand indique un /<nom> qui n'est ni intégré ni un skill.
var ErrUnknownCommand = errors.New("unknown skill or command")

// Expansion est le résultat du traitement d'une ligne commençant par « / ».
type Expansion struct {
	Text, Skill, SHA256 string
}

// Builtins liste les commandes intégrées (sans la barre oblique), source
// unique pour IsBuiltin et la complétion de la TUI.
var Builtins = []string{"help", "quit", "skills", "agents", "hooks", "rewind", "remember", "memory", "theme"}

// IsBuiltin indique si name (sans la barre oblique) est une commande
// intégrée, prioritaire sur les skills.
func IsBuiltin(name string) bool {
	return slices.Contains(Builtins, name)
}

// PromptSection renvoie la section du prompt système listant les skills
// invocables par le modèle, ou "" s'il n'y en a aucun. Au-delà de budget
// octets, les descriptions sont coupées à 200 caractères, puis les derniers
// skills sont retirés (dropped). Les skills intégrés n'y figurent que si
// withBuiltin est vrai (projet comportant un dossier cra/) ; ils restent dans
// le catalogue dans tous les cas.
func (c *Catalog) PromptSection(lang i18n.Lang, budget int, withBuiltin bool) (section string, dropped int) {
	var skills []Skill
	for _, s := range c.List() {
		if s.ModelInvocable && (withBuiltin || s.Source != Builtin) {
			skills = append(skills, s)
		}
	}
	if len(skills) == 0 {
		return "", 0
	}
	header := i18n.T(lang, "skills.prompt_header")
	build := func(n int, max int) string {
		var b strings.Builder
		b.WriteString(header)
		for _, s := range skills[:n] {
			d := strings.Join(strings.Fields(s.Description), " ")
			if r := []rune(d); max > 0 && len(r) > max {
				d = string(r[:max])
			}
			b.WriteString("\n- " + s.Name + ": " + d)
		}
		return b.String()
	}
	if out := build(len(skills), 0); len(out) <= budget {
		return out, 0
	}
	n := len(skills)
	out := build(n, maxPromptDescChars)
	for len(out) > budget && n > 0 {
		n--
		out = build(n, maxPromptDescChars)
	}
	if n == 0 {
		return "", len(skills)
	}
	return out, len(skills) - n
}

// Expand traite une ligne de l'utilisateur. ok=false si ce n'est pas une
// commande slash. Les commandes intégrées sont traitées par l'appelant.
func (c *Catalog) Expand(line string) (exp Expansion, ok bool, err error) {
	if !strings.HasPrefix(line, "/") {
		return Expansion{}, false, nil
	}
	rest := line[1:]
	if rest == "" || rest[0] == ' ' || rest[0] == '\t' || rest[0] == '\n' {
		return Expansion{}, false, nil
	}
	if rest[0] == '/' {
		return Expansion{Text: rest}, true, nil
	}
	tok, args := rest, ""
	if i := strings.IndexAny(rest, " \t\r\n"); i >= 0 {
		tok, args = rest[:i], strings.TrimSpace(rest[i:])
	}
	if !nameRe.MatchString(tok) {
		return Expansion{}, true, fmt.Errorf("%w: /%s", ErrUnknownCommand, tok)
	}
	s, found := c.Get(tok)
	// Les commandes intégrées l'emportent sur un skill du même nom.
	if !found || IsBuiltin(tok) {
		return Expansion{}, true, fmt.Errorf("%w: /%s", ErrUnknownCommand, tok)
	}
	body, sum, trunc, err := c.Load(tok)
	if err != nil {
		return Expansion{}, true, err
	}
	body = strings.TrimRight(body, "\r\n")
	if trunc {
		body += "\n[… truncated]"
	}
	text := fmt.Sprintf("Use the skill %q.\n<skill name=%q source=%q dir=%q>\n%s\n</skill>", s.Name, s.Name, string(s.Source), s.Dir, body)
	if args != "" {
		text += "\nUser request: " + args
	}
	return Expansion{Text: text, Skill: s.Name, SHA256: sum}, true, nil
}
