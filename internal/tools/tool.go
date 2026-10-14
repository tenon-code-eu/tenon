// Package tools définit l'interface des outils de l'agent et les outils fichiers.
package tools

import (
	"context"
	"encoding/json"
	"sort"

	"codeberg.org/tenon/tenon/internal/llm"
)

// Risk classe l'effet d'un outil.
type Risk int

const (
	Read Risk = iota
	Write
	Exec
	Network
)

// Target décrit ce qu'une invocation touche (pour le moteur de permissions).
type Target struct {
	Paths   []string // chemins absolus nettoyés
	Command string
}

// Result est le résultat d'une exécution d'outil.
type Result struct {
	Content    string
	IsError    bool
	Path       string
	HashBefore string
	HashAfter  string
}

// Tool est un outil appelable par le modèle.
type Tool interface {
	Name() string
	Description() string
	Schema() json.RawMessage
	Risk() Risk
	Target(args json.RawMessage) (Target, error)
	// Run renvoie une error uniquement en cas d'annulation du contexte.
	Run(ctx context.Context, args json.RawMessage) (Result, error)
}

// Registry indexe les outils par nom.
type Registry struct {
	tools map[string]Tool
}

func NewRegistry(ts ...Tool) *Registry {
	r := &Registry{tools: make(map[string]Tool, len(ts))}
	for _, t := range ts {
		r.tools[t.Name()] = t
	}
	return r
}

func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// Defs renvoie les définitions triées par nom.
func (r *Registry) Defs() []llm.ToolDef {
	defs := make([]llm.ToolDef, 0, len(r.tools))
	for _, t := range r.tools {
		defs = append(defs, llm.ToolDef{Name: t.Name(), Description: t.Description(), Schema: t.Schema()})
	}
	sort.Slice(defs, func(i, j int) bool { return defs[i].Name < defs[j].Name })
	return defs
}
