package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/tenon-code-eu/tenon/internal/config"
	"github.com/tenon-code-eu/tenon/internal/hooks"
	"github.com/tenon-code-eu/tenon/internal/i18n"
)

// hookTextMax borne l'aperçu d'une commande ou d'un prompt dans « tenon hooks ».
const hookTextMax = 80

// projectIgnoredPrefix est le début de l'avis (anglais) de hooks.Load pour un
// fichier de hooks de projet, traduit par loadHooks.
const projectIgnoredPrefix = "project hooks ignored: "

// loadHooks charge les hooks utilisateur de wd. notices (déjà traduits et
// nettoyés) : avis du chargement, puis la bannière s'il y a des hooks. Une
// erreur de fichier est fatale.
func loadHooks(lang i18n.Lang, getenv func(string) string, wd string, cfg config.Hooks) (*hooks.Set, []string, error) {
	set, disc, err := hooks.Load(hooks.Options{Getenv: getenv, ProjectRoot: wd, Config: cfg})
	if err != nil {
		return nil, nil, err
	}
	var notices []string
	for _, n := range disc {
		s := string(n)
		if path, ok := strings.CutPrefix(s, projectIgnoredPrefix); ok {
			s = i18n.T(lang, "hooks.project_ignored", path)
		}
		notices = append(notices, clean(s))
	}
	if total, claude := set.Count(); total > 0 {
		notices = append(notices, i18n.T(lang, "hooks.banner", total, claude))
	}
	return set, notices, nil
}

// listHooks affiche « tenon hooks » : un hook par ligne, puis les avis.
func listHooks(lang i18n.Lang, stdout, stderr io.Writer, getenv func(string) string, wd, globalPath string) int {
	s, err := loadSetup(lang, getenv, wd, globalPath)
	if err != nil {
		fmt.Fprintln(stderr, i18n.T(lang, "err.prefix", err))
		return 1
	}
	set, disc, err := hooks.Load(hooks.Options{Getenv: getenv, ProjectRoot: wd, Config: s.cfg.Hooks})
	if err != nil {
		fmt.Fprintln(stderr, i18n.T(lang, "err.prefix", err))
		return 1
	}
	list := set.List()
	if len(list) == 0 {
		fmt.Fprintln(stdout, i18n.T(lang, "hooks.none"))
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
		text = strings.Join(strings.Fields(clean(text)), " ")
		if r := []rune(text); len(r) > hookTextMax {
			text = string(r[:hookTextMax])
		}
		fmt.Fprintf(stdout, "%s  %s  %s  %s\n", clean(h.ID), clean(matcher), h.Type, text)
	}
	for _, n := range disc {
		s := string(n)
		if path, ok := strings.CutPrefix(s, projectIgnoredPrefix); ok {
			s = i18n.T(lang, "hooks.project_ignored", path)
		}
		fmt.Fprintln(stdout, clean(s))
	}
	return 0
}
