package tui

import (
	"context"

	"codeberg.org/tenon/tenon/internal/engine"
	"codeberg.org/tenon/tenon/internal/event"
)

// Engine est la partie de l'engine dont la TUI a besoin.
type Engine interface {
	// Subscribe renvoie un abonnement aux événements ; l'engine bloque quand
	// son tampon est plein, la TUI doit donc le lire en continu.
	Subscribe() <-chan event.Event
	// Submit exécute un tour et rend la main à sa fin.
	Submit(ctx context.Context, sessionID, input string) error
	// Respond répond à une demande de permission.
	Respond(permissionID string, r engine.Reply) error
}

var _ Engine = (*engine.Engine)(nil)
