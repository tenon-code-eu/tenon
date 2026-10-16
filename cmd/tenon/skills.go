package main

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/tenon-code-eu/tenon/internal/engine"
	"github.com/tenon-code-eu/tenon/internal/i18n"
	"github.com/tenon-code-eu/tenon/internal/skills"
)

// promptBudget est la taille maximale de la section « skills » du prompt.
const promptBudget = 8000

// discoverSkills découvre les skills de l'utilisateur et du projet wd.
// notices (déjà traduits) : avis de découverte, bannière, skills omis du
// prompt, skills masqués par une commande intégrée. section est le texte à
// ajouter au prompt système ("" s'il n'y a aucun skill invocable).
func discoverSkills(lang i18n.Lang, getenv func(string) string, wd string) (cat *skills.Catalog, section string, notices []string) {
	cat, disc := skills.Discover(skills.Dirs(getenv, wd))
	for _, n := range disc {
		notices = append(notices, string(n))
	}
	list := cat.List()
	if len(list) > 0 {
		project := 0
		for _, s := range list {
			if s.Source == skills.Project {
				project++
			}
		}
		notices = append(notices, i18n.T(lang, "skills.banner", len(list), project))
	}
	section, dropped := cat.PromptSection(lang, promptBudget)
	if dropped > 0 {
		notices = append(notices, i18n.T(lang, "skills.dropped", dropped))
	}
	for _, s := range list {
		if skills.IsBuiltin(s.Name) {
			notices = append(notices, i18n.T(lang, "skills.builtin_conflict", s.Name))
		}
	}
	return cat, section, notices
}

// listSkills affiche « tenon skills » : un skill par ligne, puis les avis.
func listSkills(lang i18n.Lang, stdout io.Writer, getenv func(string) string, wd string) int {
	cat, disc := skills.Discover(skills.Dirs(getenv, wd))
	list := cat.List()
	if len(list) == 0 {
		fmt.Fprintln(stdout, i18n.T(lang, "skills.none"))
	}
	for _, s := range list {
		desc := strings.Join(strings.Fields(s.Description), " ")
		line := fmt.Sprintf("%s  %s  %s  %s", s.Name, s.Source, s.Dir, desc)
		if len(s.AllowedTools) > 0 {
			line += "  allowed-tools: " + strings.Join(s.AllowedTools, ", ")
		}
		fmt.Fprintln(stdout, line)
	}
	for _, n := range disc {
		fmt.Fprintln(stdout, string(n))
	}
	return 0
}

// headlessInput transforme le prompt de -p en entrée du moteur. Les commandes
// intégrées (TUI) sont envoyées telles quelles au modèle ; /<skill> est
// développé, //texte est envoyé sans la première barre. Un /<inconnu> renvoie
// une erreur traduite : rien n'est envoyé.
func headlessInput(lang i18n.Lang, cat *skills.Catalog, prompt string) (engine.Input, error) {
	if tok, _, _ := strings.Cut(strings.TrimPrefix(prompt, "/"), " "); strings.HasPrefix(prompt, "/") && skills.IsBuiltin(tok) {
		return engine.Input{Text: prompt}, nil
	}
	exp, ok, err := cat.Expand(prompt)
	switch {
	case errors.Is(err, skills.ErrUnknownCommand):
		_, cmd, _ := strings.Cut(err.Error(), ": ")
		return engine.Input{}, errors.New(i18n.T(lang, "skills.unknown", cmd))
	case err != nil:
		return engine.Input{}, err
	case !ok:
		return engine.Input{Text: prompt}, nil
	}
	in := engine.Input{Text: exp.Text, Skill: exp.Skill, SkillSHA256: exp.SHA256}
	if exp.Skill != "" {
		in.Typed = prompt
	}
	return in, nil
}

// systemPrompt ajoute la section des skills au prompt système.
func systemPrompt(base, section string) string {
	if section == "" {
		return base
	}
	return base + "\n\n" + section
}
