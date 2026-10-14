// Package headless exécute un tour d'agent sans interface interactive : toute
// demande de permission est refusée, la sortie est du texte ou du JSON.
package headless

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"codeberg.org/tenon/tenon/internal/engine"
	"codeberg.org/tenon/tenon/internal/event"
	"codeberg.org/tenon/tenon/internal/llm"
)

// Output choisit le format de sortie.
type Output string

const (
	Text Output = "text"
	JSON Output = "json"
)

// Codes de sortie.
const (
	exitOK      = 0
	exitError   = 1
	exitRefused = 3
)

// Run soumet prompt à eng dans la session sessionID et renvoie le code de
// sortie : 3 si une permission a été refusée ou le tour bloqué, 1 si Submit
// échoue, sinon 0.
func Run(ctx context.Context, eng *engine.Engine, sessionID, prompt string, out Output, stdout, stderr io.Writer) int {
	ch := eng.Subscribe()
	errc := make(chan error, 1)
	go func() { errc <- eng.Submit(ctx, sessionID, prompt) }()

	r := &renderer{eng: eng, out: out, stdout: stdout, stderr: stderr}
	var err error
loop:
	for {
		select {
		case ev := <-ch:
			r.handle(ev)
		case err = <-errc:
			break loop
		}
	}
	// Quand Submit rend la main, tous les événements du tour sont dans le canal.
	for drained := false; !drained; {
		select {
		case ev := <-ch:
			r.handle(ev)
		default:
			drained = true
		}
	}

	if out == Text && r.final != "" {
		fmt.Fprintln(stdout, r.final)
	}
	switch {
	case err != nil:
		fmt.Fprintf(stderr, "tenon : %v\n", err)
		return exitError
	case r.refused:
		return exitRefused
	}
	return exitOK
}

type renderer struct {
	eng            *engine.Engine
	out            Output
	stdout, stderr io.Writer

	final   string // dernier texte complet de l'assistant
	refused bool
}

func (r *renderer) handle(ev event.Event) {
	if r.out == JSON {
		if b, err := json.Marshal(ev); err == nil {
			fmt.Fprintf(r.stdout, "%s\n", b)
		}
	}
	switch ev.Type {
	case event.SessionStarted:
		var p event.SessionStartedPayload
		if r.decode(ev, &p) {
			r.text("%s\n", Banner(p.Provider))
		}
	case event.JurisdictionWarning:
		var p event.JurisdictionWarningPayload
		if r.decode(ev, &p) {
			r.text("attention : %s traite les données hors UE (juridiction %s)\n",
				providerLabel(p.Provider), jurisdictionLabel(p.Provider.Jurisdiction))
		}
	case event.PermissionRequested:
		var p event.PermissionRequestedPayload
		if !r.decode(ev, &p) {
			return
		}
		r.refused = true
		// Respond n'échoue que si la demande est déjà résolue (contexte annulé).
		_ = r.eng.Respond(p.PermissionID, engine.Reply{Allow: false})
		r.text("refusé (mode non interactif) : %s\n", describe(p))
	case event.AssistantMessage:
		var p event.AssistantMessagePayload
		if r.decode(ev, &p) && p.Text != "" {
			r.final = p.Text
		}
	case event.TurnCompleted:
		var p event.TurnCompletedPayload
		if r.decode(ev, &p) {
			switch p.Reason {
			case "blocked":
				r.refused = true
				r.text("tour bloqué : envoi au fournisseur refusé\n")
			case "max_iterations", "incomplete", "cancelled":
				r.text("tour terminé : %s\n", p.Reason)
			}
		}
	case event.Error:
		var p event.ErrorPayload
		if r.decode(ev, &p) {
			r.text("erreur (%s) : %s\n", p.Code, p.Message)
		}
	}
}

// decode lit la charge utile ; une charge illisible est signalée sur stderr.
func (r *renderer) decode(ev event.Event, v any) bool {
	if err := json.Unmarshal(ev.Payload, v); err != nil {
		fmt.Fprintf(r.stderr, "tenon : événement %s illisible : %v\n", ev.Type, err)
		return false
	}
	return true
}

// text écrit sur stderr en sortie texte uniquement (en JSON, tout est sur stdout).
func (r *renderer) text(format string, a ...any) {
	if r.out == Text {
		fmt.Fprintf(r.stderr, format, a...)
	}
}

// Banner renvoie la bannière de transparence :
// « tenon · <modèle> · <nom fournisseur> · <pays> · juridiction <UE|hors UE|locale> ».
// Un nom ou un pays vide est omis.
func Banner(p llm.ProviderInfo) string {
	parts := []string{"tenon", p.Model}
	if n := providerLabel(p); n != "" {
		parts = append(parts, n)
	}
	if p.Country != "" {
		parts = append(parts, p.Country)
	}
	parts = append(parts, "juridiction "+jurisdictionLabel(p.Jurisdiction))
	return strings.Join(parts, " · ")
}

func providerLabel(p llm.ProviderInfo) string {
	if p.Name != "" {
		return p.Name
	}
	return p.ID
}

func jurisdictionLabel(j llm.Jurisdiction) string {
	switch j {
	case llm.EU:
		return "UE"
	case llm.Local:
		return "locale"
	}
	return "hors UE"
}

func describe(p event.PermissionRequestedPayload) string {
	if p.Kind == "jurisdiction" && p.Provider != nil {
		return fmt.Sprintf("envoi à %s (juridiction %s) ; relancez avec --allow-non-eu pour l'accepter",
			providerLabel(*p.Provider), jurisdictionLabel(p.Provider.Jurisdiction))
	}
	s := p.Tool
	if p.Command != "" {
		s += " " + p.Command
	}
	if len(p.Paths) > 0 {
		s += " " + strings.Join(p.Paths, ", ")
	}
	return s
}
