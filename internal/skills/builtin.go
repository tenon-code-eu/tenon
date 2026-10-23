package skills

import (
	"embed"
	"io/fs"
	"sort"
	"strings"

	"github.com/tenon-code-eu/tenon/internal/frontmatter"
)

//go:embed builtin/*/SKILL.md
var builtinFS embed.FS

// builtinPrefix préfixe le « dossier » d'un skill intégré : il n'existe pas
// sur disque.
const builtinPrefix = "builtin:"

// readBuiltin lit le SKILL.md embarqué d'un skill intégré.
func readBuiltin(dir string) ([]byte, error) {
	return builtinFS.ReadFile("builtin/" + strings.TrimPrefix(dir, builtinPrefix) + "/" + skillFile)
}

// addBuiltin ajoute les skills intégrés, à la plus basse priorité : un skill
// utilisateur ou projet du même nom l'emporte, sans avertissement.
func (c *Catalog) addBuiltin() []Notice {
	var notices []Notice
	entries, err := fs.ReadDir(builtinFS, "builtin")
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		if _, dup := c.byName[name]; dup || len(c.skills) >= MaxSkills {
			continue
		}
		dir := builtinPrefix + name
		data, err := readBuiltin(dir)
		if err != nil {
			continue
		}
		fm, _, err := frontmatter.Parse(data)
		if err == nil {
			err = validate(fm, name)
		}
		if err != nil {
			notices = append(notices, Notice("built-in skill "+name+" ignored: "+err.Error()))
			continue
		}
		c.byName[name] = len(c.skills)
		c.skills = append(c.skills, Skill{
			Name:           name,
			Description:    fm.Description(),
			Source:         Builtin,
			Dir:            dir,
			AllowedTools:   fm.List("allowed-tools"),
			ModelInvocable: !fm.Bool("disable-model-invocation"),
		})
	}
	return notices
}
