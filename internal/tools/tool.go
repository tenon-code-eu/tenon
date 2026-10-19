// Package tools définit l'interface des outils de l'agent et les outils fichiers.
package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tenon-code-eu/tenon/internal/llm"
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
	// ForceAsk impose une validation humaine : Permissions.Check renvoie Ask
	// (Deny si une règle deny correspond), quelles que soient les règles allow.
	ForceAsk bool
}

// Result est le résultat d'une exécution d'outil.
type Result struct {
	Content    string
	IsError    bool
	Path       string
	HashBefore string
	HashAfter  string
	// Denied vaut "policy" quand l'outil a refusé l'appel par règle (vide sinon).
	Denied string
	// ServerDown porte l'identifiant d'un serveur MCP qui vient de tomber pendant
	// cet appel (vide sinon) ; signalé une seule fois par serveur.
	ServerDown string
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
	if r == nil {
		return nil, false
	}
	t, ok := r.tools[name]
	return t, ok
}

// With renvoie une copie du registre complétée de ts (r nil : registre vide
// plus ts). Le récepteur n'est pas modifié.
func (r *Registry) With(ts ...Tool) *Registry {
	out := NewRegistry()
	if r != nil {
		for n, t := range r.tools {
			out.tools[n] = t
		}
	}
	for _, t := range ts {
		out.tools[t.Name()] = t
	}
	return out
}

// Only renvoie une copie restreinte aux outils de names (noms inconnus
// ignorés ; r nil : registre vide). Le récepteur n'est pas modifié.
func (r *Registry) Only(names []string) *Registry {
	out := NewRegistry()
	if r == nil {
		return out
	}
	for _, n := range names {
		if t, ok := r.tools[n]; ok {
			out.tools[n] = t
		}
	}
	return out
}

// Names renvoie les noms des outils, triés.
func (r *Registry) Names() []string {
	if r == nil {
		return nil
	}
	names := make([]string, 0, len(r.tools))
	for n := range r.tools {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Defs renvoie les définitions triées par nom.
func (r *Registry) Defs() []llm.ToolDef {
	if r == nil {
		return nil
	}
	defs := make([]llm.ToolDef, 0, len(r.tools))
	for _, t := range r.tools {
		defs = append(defs, llm.ToolDef{Name: t.Name(), Description: t.Description(), Schema: t.Schema()})
	}
	sort.Slice(defs, func(i, j int) bool { return defs[i].Name < defs[j].Name })
	return defs
}

// IsSensitive rapporte si le nom de base du chemin désigne un fichier de
// secrets : préfixe .env ou id_rsa, suffixe .pem (insensible à la casse).
func IsSensitive(path string) bool {
	b := strings.ToLower(filepath.Base(path))
	return strings.HasPrefix(b, ".env") || strings.HasSuffix(b, ".pem") || strings.HasPrefix(b, "id_rsa")
}
