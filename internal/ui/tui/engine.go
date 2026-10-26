package tui

import (
	"context"

	"github.com/tenon-code-eu/tenon/internal/checkpoint"
	"github.com/tenon-code-eu/tenon/internal/engine"
	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/memory"
)

// Engine est la partie de l'engine dont la TUI a besoin.
type Engine interface {
	// Subscribe renvoie un abonnement aux événements ; l'engine bloque quand
	// son tampon est plein, la TUI doit donc le lire en continu.
	Subscribe() <-chan event.Event
	// SubmitInput exécute un tour (texte simple ou skill développé) et rend
	// la main à sa fin.
	SubmitInput(ctx context.Context, sessionID string, in engine.Input) error
	// Respond répond à une demande de permission.
	Respond(permissionID string, r engine.Reply) error
	// RewindPoints, RewindPlan et Rewind : /rewind (points récents d'abord,
	// plan sans effet, exécution).
	RewindPoints(sessionID string) []checkpoint.Point
	RewindPlan(sessionID string, toSeq uint64) (checkpoint.Plan, error)
	// Remember : /remember (écriture directe d'une note, sans approbation).
	Remember(sessionID string, scope memory.Scope, text string) (memory.Entry, []string, error)
	Rewind(ctx context.Context, sessionID string, toSeq uint64, mode engine.RewindMode, preview checkpoint.Plan, overwrite []string) (engine.RewindResult, error)
}

var _ Engine = (*engine.Engine)(nil)
