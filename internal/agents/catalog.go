// Package agents découvre les définitions de sous-agents (fichiers <nom>.md
// compatibles Claude Code) et construit leur prompt système.
package agents

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/tenon-code-eu/tenon/internal/frontmatter"
	"github.com/tenon-code-eu/tenon/internal/i18n"
	"github.com/tenon-code-eu/tenon/internal/safefile"
	"github.com/tenon-code-eu/tenon/internal/skills"
)

const (
	// GeneralPurpose est le nom de l'agent intégré, utilisé par défaut.
	GeneralPurpose = "general-purpose"
	// MaxAgents est le nombre maximal de fichiers d'agents retenus.
	MaxAgents = 100
	// MaxAgentBytes est la taille maximale lue pour un fichier d'agent.
	MaxAgentBytes = 32768

	maxDescriptionChars = 1024
	agentExt            = ".md"
)

var nameRe = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)

// Source indique l'origine d'un agent.
type Source string

const (
	Builtin Source = "builtin"
	User    Source = "user"
	Project Source = "project"
)

// Dir est un répertoire d'agents et son origine.
type Dir struct {
	Path   string
	Source Source
}

// Notice est un message d'information (agent ignoré, limite atteinte).
type Notice string

// Agent décrit un sous-agent. Tools == nil signifie tous les outils de la
// session sauf agent ; une clé tools: présente donne toujours un non-nil.
type Agent struct {
	Name, Description string
	Source            Source
	Path              string
	Tools             []string
	Model             string
	Body              string
	Truncated         bool
}

// Catalog contient les agents découverts.
type Catalog struct {
	agents []Agent
	byName map[string]int
}

// builtinTools associe les noms d'outils Claude Code à ceux de tenon.
var builtinTools = map[string]string{
	"Read": "read_file", "Write": "write_file", "Edit": "edit_file",
	"MultiEdit": "edit_file", "Bash": "shell", "Grep": "grep",
	"Glob": "glob", "Skill": "skill",
	// Noms natifs de tenon, conservés tels quels.
	"read_file": "read_file", "write_file": "write_file", "edit_file": "edit_file",
	"shell": "shell", "grep": "grep", "glob": "glob", "skill": "skill",
}

// Dirs renvoie les répertoires d'agents, mêmes bases et même priorité que
// les skills, le dernier élément « skills » devenant « agents ».
func Dirs(getenv func(string) string, projectRoot string) []Dir {
	var dirs []Dir
	for _, d := range skills.Dirs(getenv, projectRoot) {
		dirs = append(dirs, Dir{filepath.Join(filepath.Dir(d.Path), "agents"), Source(d.Source)})
	}
	return dirs
}

// TranslateTools convertit une liste d'outils Claude Code en noms tenon
// (sans doublon, suffixe « (...) » retiré, mcp__* conservé). Les noms
// inconnus (dont agent) sont renvoyés dans unknown. tools est jamais nil.
func TranslateTools(names []string) (tools []string, unknown []string) {
	tools = []string{}
	seen := map[string]bool{}
	for _, n := range names {
		n = strings.TrimSpace(n)
		if i := strings.IndexByte(n, '('); i >= 0 {
			n = strings.TrimSpace(n[:i])
		}
		t, ok := builtinTools[n]
		if !ok && strings.HasPrefix(n, "mcp__") {
			t, ok = n, true
		}
		if !ok {
			unknown = append(unknown, n)
			continue
		}
		if !seen[t] {
			seen[t] = true
			tools = append(tools, t)
		}
	}
	return tools, unknown
}

// Discover parcourt les répertoires dans l'ordre ; en cas de doublon, le
// premier gagne. Les bases de projet en lien symbolique et les fichiers non
// réguliers sont ignorés. L'agent intégré est ajouté s'il n'est pas défini.
func Discover(lang i18n.Lang, dirs []Dir) (*Catalog, []Notice) {
	c := &Catalog{byName: map[string]int{}}
	var notices []Notice
	notef := func(format string, args ...any) {
		notices = append(notices, Notice(fmt.Sprintf(format, args...)))
	}
	files := 0
	for _, d := range dirs {
		// Une base de projet en lien symbolique n'est pas suivie ; une base
		// utilisateur peut l'être (dotfiles).
		if fi, err := os.Lstat(d.Path); err == nil && fi.Mode()&fs.ModeSymlink != 0 && d.Source == Project {
			notef("agents directory %s ignored: it is a symbolic link", d.Path)
			continue
		}
		entries, err := os.ReadDir(d.Path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			notef("agents directory %s unreadable: %v", d.Path, err)
			continue
		}
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), agentExt) {
				continue
			}
			path := filepath.Join(d.Path, e.Name())
			data, trunc, ok, err := safefile.ReadRegular(path, MaxAgentBytes)
			if err != nil {
				notef("agent %q in %s ignored: %v", e.Name(), path, err)
				continue
			}
			if !ok {
				continue // lien symbolique, FIFO, etc.
			}
			a, err := parse(data, strings.TrimSuffix(e.Name(), agentExt))
			if err != nil {
				notef("agent %q in %s ignored: %v", e.Name(), path, err)
				continue
			}
			if i, dup := c.byName[a.Name]; dup {
				notef("agent %q in %s ignored: shadowed by %s", a.Name, path, c.agents[i].Path)
				continue
			}
			if files >= MaxAgents {
				notef("agent %q in %s ignored: limit of %d agents reached", a.Name, path, MaxAgents)
				continue
			}
			for _, u := range a.unknown {
				notef("agent %q in %s: tool %q ignored", a.Name, path, u)
			}
			a.Source, a.Path, a.Truncated = d.Source, path, trunc
			files++
			c.byName[a.Name] = len(c.agents)
			c.agents = append(c.agents, a.Agent)
		}
	}
	if _, ok := c.byName[GeneralPurpose]; !ok {
		c.agents = append([]Agent{{
			Name:        GeneralPurpose,
			Description: i18n.T(lang, "agents.general_purpose"),
			Source:      Builtin,
		}}, c.agents...)
		for i, a := range c.agents {
			c.byName[a.Name] = i
		}
	}
	return c, notices
}

type parsed struct {
	Agent
	unknown []string
}

// parse lit un fichier d'agent et valide ses champs (file = nom sans .md).
func parse(data []byte, file string) (parsed, error) {
	fm, body, err := frontmatter.Parse(data)
	if err != nil {
		return parsed{}, err
	}
	name := fm.Name()
	if !nameRe.MatchString(name) {
		return parsed{}, fmt.Errorf("invalid name %q (expected [a-z0-9-], 1 to 64 chars)", name)
	}
	if name != file {
		return parsed{}, fmt.Errorf("name %q does not match file name %q", name, file)
	}
	if n := utf8.RuneCountInString(fm.Description()); n > maxDescriptionChars {
		return parsed{}, fmt.Errorf("description too long (%d chars, max %d)", n, maxDescriptionChars)
	}
	p := parsed{Agent: Agent{Name: name, Description: fm.Description(), Model: fm.String("model"), Body: body}}
	if fm.Has("tools") {
		p.Tools, p.unknown = TranslateTools(fm.List("tools"))
	}
	return p, nil
}

// Get renvoie l'agent de ce nom.
func (c *Catalog) Get(name string) (Agent, bool) {
	i, ok := c.byName[name]
	if !ok {
		return Agent{}, false
	}
	return c.agents[i], true
}

// List renvoie les agents : l'intégré d'abord (sauf s'il est remplacé), puis
// l'ordre de découverte.
func (c *Catalog) List() []Agent {
	return append([]Agent(nil), c.agents...)
}

// FileCount renvoie le nombre d'agents issus de fichiers et, parmi eux,
// ceux du projet.
func (c *Catalog) FileCount() (total, project int) {
	for _, a := range c.agents {
		if a.Source == Builtin {
			continue
		}
		total++
		if a.Source == Project {
			project++
		}
	}
	return total, project
}
