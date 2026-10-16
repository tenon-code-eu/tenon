package skills

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/tenon-code-eu/tenon/internal/tools"
)

// skillTool charge un skill (SKILL.md) ou un de ses fichiers annexes.
type skillTool struct {
	c     *Catalog
	limit int
}

// NewTool renvoie l'outil `skill` ; outputLimit borne la lecture d'un fichier.
func NewTool(c *Catalog, outputLimit int) tools.Tool {
	return &skillTool{c: c, limit: outputLimit}
}

func (*skillTool) Name() string { return "skill" }
func (*skillTool) Description() string {
	return "Load a skill: its instructions (SKILL.md) or one of its files. " +
		"Call it with the skill's name before acting when a listed skill matches the request. " +
		"Use `file` for a file listed by a previous call."
}
func (*skillTool) Risk() tools.Risk { return tools.Read }
func (*skillTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"},"file":{"type":"string"}},"required":["name"]}`)
}

type skillArgs struct {
	Name string `json:"name"`
	File string `json:"file"`
}

func parseSkillArgs(args json.RawMessage) (skillArgs, error) {
	var a skillArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return a, fmt.Errorf("invalid arguments: %w", err)
	}
	if a.Name == "" {
		return a, errors.New("invalid arguments: name required")
	}
	return a, nil
}

// Target ne porte pas de chemin : le dossier du skill est hors projet et
// les règles portent sur le nom (Command).
func (*skillTool) Target(args json.RawMessage) (tools.Target, error) {
	a, err := parseSkillArgs(args)
	if err != nil {
		return tools.Target{}, err
	}
	if a.File != "" {
		return tools.Target{Command: a.Name + "/" + a.File}, nil
	}
	return tools.Target{Command: a.Name}, nil
}

func fail(err error) tools.Result {
	return tools.Result{IsError: true, Content: err.Error()}
}

func (t *skillTool) Run(ctx context.Context, args json.RawMessage) (tools.Result, error) {
	if err := ctx.Err(); err != nil {
		return tools.Result{}, err
	}
	a, err := parseSkillArgs(args)
	if err != nil {
		return fail(err), nil
	}
	if a.File != "" {
		content, abs, sum, err := t.c.ReadFile(a.Name, a.File, t.limit)
		if err != nil {
			return fail(err), nil
		}
		return tools.Result{Content: content, Path: abs, HashAfter: "sha256:" + sum}, nil
	}
	s, ok := t.c.Get(a.Name)
	if !ok {
		return fail(fmt.Errorf("unknown skill %q", a.Name)), nil
	}
	body, sum, trunc, err := t.c.Load(a.Name)
	if err != nil {
		return fail(err), nil
	}
	files, filesTrunc, err := t.c.Files(a.Name)
	if err != nil {
		return fail(err), nil
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Skill %s (%s, %s)\n\n%s", s.Name, s.Source, s.Dir, body)
	if trunc {
		sb.WriteString("\n[… truncated]")
	}
	sb.WriteString("\n\nFiles:\n")
	for _, f := range files {
		sb.WriteString("- " + f + "\n")
	}
	if filesTrunc {
		sb.WriteString("- …\n")
	}
	return tools.Result{Content: sb.String(), Path: filepath.Join(s.Dir, skillFile), HashAfter: "sha256:" + sum}, nil
}
