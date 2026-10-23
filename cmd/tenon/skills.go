package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/tenon-code-eu/tenon/internal/engine"
	"github.com/tenon-code-eu/tenon/internal/i18n"
	"github.com/tenon-code-eu/tenon/internal/skills"
)

// promptBudget est la taille maximale de la section « skills » du prompt.
const promptBudget = 8000

// discovered est le résultat de discoverSkills.
type discovered struct {
	cat     *skills.Catalog
	section string
	notices []string
}

// clean remplace les caractères de contrôle et de format (ESC, U+202E…) par
// une espace : les skills du projet ne sont pas dignes de confiance.
func clean(s string) string {
	return strings.Map(func(c rune) rune {
		if unicode.IsControl(c) || unicode.Is(unicode.Cf, c) {
			return ' '
		}
		return c
	}, s)
}

// discoverSkills découvre les skills de l'utilisateur et du projet wd.
// notices (déjà traduits) : avis de découverte, bannière, skills omis du
// prompt, skills masqués par une commande intégrée. section est le texte à
// ajouter au prompt système ("" s'il n'y a aucun skill invocable).
func discoverSkills(lang i18n.Lang, getenv func(string) string, wd string) discovered {
	cat, disc := skills.Discover(skills.Dirs(getenv, wd))
	var notices []string
	for _, n := range disc {
		notices = append(notices, clean(string(n)))
	}
	list := cat.List()
	// Le bandeau ne compte que les skills du dépôt ou de l'utilisateur : les
	// skills intégrés sont toujours présents.
	project, total := 0, 0
	for _, s := range list {
		if s.Source == skills.Builtin {
			continue
		}
		total++
		if s.Source == skills.Project {
			project++
		}
	}
	if total > 0 {
		notices = append(notices, i18n.T(lang, "skills.banner", total, project))
	}
	section, dropped := cat.PromptSection(lang, promptBudget, hasCRADir(wd))
	if dropped > 0 {
		notices = append(notices, i18n.T(lang, "skills.dropped", dropped))
	}
	for _, s := range list {
		if skills.IsBuiltin(s.Name) {
			notices = append(notices, i18n.T(lang, "skills.builtin_conflict", clean(s.Name)))
		}
	}
	return discovered{cat: cat, section: section, notices: notices}
}

// listSkills affiche « tenon skills » : un skill par ligne, puis les avis.
func listSkills(lang i18n.Lang, stdout io.Writer, getenv func(string) string, wd string) int {
	cat, disc := skills.Discover(skills.Dirs(getenv, wd))
	list := cat.List()
	if len(list) == 0 {
		fmt.Fprintln(stdout, i18n.T(lang, "skills.none"))
	}
	for _, s := range list {
		desc := strings.Join(strings.Fields(clean(s.Description)), " ")
		line := fmt.Sprintf("%s  %s  %s  %s", clean(s.Name), s.Source, clean(s.Dir), desc)
		if len(s.AllowedTools) > 0 {
			line += "  allowed-tools: " + clean(strings.Join(s.AllowedTools, ", "))
		}
		fmt.Fprintln(stdout, line)
	}
	for _, n := range disc {
		fmt.Fprintln(stdout, clean(string(n)))
	}
	return 0
}

// headlessInput transforme le prompt de -p en entrée du moteur. Les commandes
// intégrées (TUI) sont envoyées telles quelles au modèle ; /<skill> est
// développé, //texte est envoyé sans la première barre. Un /<inconnu> renvoie
// une erreur traduite : rien n'est envoyé.
func headlessInput(lang i18n.Lang, cat *skills.Catalog, prompt string) (engine.Input, error) {
	rest := strings.TrimPrefix(prompt, "/")
	if i := strings.IndexAny(rest, " \t\r\n"); i >= 0 {
		rest = rest[:i]
	}
	if strings.HasPrefix(prompt, "/") && skills.IsBuiltin(rest) {
		return engine.Input{Text: prompt}, nil
	}
	exp, ok, err := cat.Expand(prompt)
	switch {
	case errors.Is(err, skills.ErrUnknownCommand):
		tok, _, _ := strings.Cut(prompt, " ")
		tok = strings.Fields(tok + " ")[0]
		return engine.Input{}, errors.New(i18n.T(lang, "skills.unknown", clean(tok)))
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

// systemPrompt ajoute au prompt de base chaque section non vide (skills,
// agents).
func systemPrompt(base string, sections ...string) string {
	for _, s := range sections {
		if s != "" {
			base += "\n\n" + s
		}
	}
	return base
}

// hasCRADir indique si la racine du projet contient un dossier cra/ (lien
// symbolique exclu) : alors les skills CRA intégrés entrent dans le prompt.
func hasCRADir(wd string) bool {
	fi, err := os.Lstat(filepath.Join(wd, "cra"))
	return err == nil && fi.IsDir()
}
