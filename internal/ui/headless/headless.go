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
	"codeberg.org/tenon/tenon/internal/i18n"
	"codeberg.org/tenon/tenon/internal/llm"
)

// Output choisit le format de sortie.
type Output string

const (
	Text Output = "text"
	JSON Output = "json"
)

// Options configure Run.
type Options struct {
	Output Output
	// Lang est la langue des messages affichés (vide : français).
	Lang i18n.Lang
	// Redact masque une charge utile JSON avant affichage (sortie json).
	// nil : aucun masquage.
	Redact func(string) string
	// RedactText masque le texte libre : réponse finale (sortie texte) et
	// messages écrits sur stderr. nil : Redact est utilisé.
	RedactText func(string) string
}

// Codes de sortie.
const (
	exitOK      = 0
	exitError   = 1
	exitRefused = 3
)

// Run soumet prompt à eng dans la session sessionID et renvoie le code de
// sortie : 3 si une action a été refusée (permission, politique) ou le tour
// bloqué, 1 si Submit échoue ou si le tour n'a pas pu tourner (contexte trop
// volumineux), sinon 0.
func Run(ctx context.Context, eng *engine.Engine, sessionID, prompt string, opts Options, stdout, stderr io.Writer) int {
	out := opts.Output
	redact := opts.Redact
	if redact == nil {
		redact = func(s string) string { return s }
	}
	redactText := opts.RedactText
	if redactText == nil {
		redactText = redact
	}
	lang := opts.Lang
	if lang != i18n.EN {
		lang = i18n.FR
	}
	ch := eng.Subscribe()
	errc := make(chan error, 1)
	go func() { errc <- eng.Submit(ctx, sessionID, prompt) }()

	r := &renderer{eng: eng, out: out, lang: lang, redact: redact, redactText: redactText, stdout: stdout, stderr: stderr}
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
		fmt.Fprintln(stdout, redactText(r.final))
	}
	switch {
	case err != nil:
		fmt.Fprint(stderr, redactText(i18n.T(lang, "err.prefix", err)+"\n"))
		return exitError
	case r.failed:
		return exitError
	case r.refused:
		return exitRefused
	}
	return exitOK
}

type renderer struct {
	eng            *engine.Engine
	out            Output
	lang           i18n.Lang
	redact         func(string) string // charge utile JSON
	redactText     func(string) string // texte libre
	stdout, stderr io.Writer

	final   string // dernier texte complet de l'assistant
	refused bool
	failed  bool // le tour n'a pas pu tourner (context_too_large)
}

func (r *renderer) handle(ev event.Event) {
	if r.out == JSON {
		r.printJSON(ev)
	}
	switch ev.Type {
	case event.SessionStarted:
		var p event.SessionStartedPayload
		if r.decode(ev, &p) {
			r.text("%s\n", Banner(r.lang, p.Provider))
			if len(p.Instructions) > 0 {
				r.text("%s\n", i18n.T(r.lang, "banner.instructions", strings.Join(p.Instructions, ", ")))
			}
		}
	case event.JurisdictionWarning:
		var p event.JurisdictionWarningPayload
		if r.decode(ev, &p) {
			r.text("%s\n", i18n.T(r.lang, "warn.non_eu",
				providerLabel(p.Provider), jurisdictionLabel(r.lang, p.Provider.Jurisdiction)))
		}
	case event.PermissionRequested:
		var p event.PermissionRequestedPayload
		if !r.decode(ev, &p) {
			return
		}
		r.refused = true
		// Respond n'échoue que si la demande est déjà résolue (contexte annulé).
		_ = r.eng.Respond(p.PermissionID, engine.Reply{Allow: false})
		r.text("%s\n", i18n.T(r.lang, "refused.user", describe(r.lang, p)))
	case event.ToolResult:
		var p event.ToolResultPayload
		if r.decode(ev, &p) && p.Denied != "" {
			r.refused = true
			if p.Denied == "policy" {
				r.text("%s\n", i18n.T(r.lang, "refused.policy", p.Tool))
			}
		}
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
				r.text("%s\n", i18n.T(r.lang, "turn.blocked"))
			case "context_too_large":
				// Une erreur, pas un refus de la politique : l'Error qui
				// précède l'a déjà expliquée.
				r.failed = true
			case "max_iterations", "incomplete", "cancelled":
				r.text("%s\n", i18n.T(r.lang, "turn.ended", p.Reason))
			}
		}
	case event.Error:
		var p event.ErrorPayload
		if r.decode(ev, &p) {
			r.text("%s\n", i18n.T(r.lang, "err.event", p.Code, p.Message))
		}
	case event.ContextCompacted:
		var p event.ContextCompactedPayload
		if r.decode(ev, &p) {
			r.text("%s\n", i18n.T(r.lang, "compaction.done", p.Replaced))
		}
	}
}

// printJSON écrit ev sur une ligne, charge utile masquée. Si le masquage rend
// la charge illisible, un événement Error{redaction} remplace l'original
// (fermeture par défaut : rien n'est écrit en clair).
func (r *renderer) printJSON(ev event.Event) {
	masked := r.redact(string(ev.Payload))
	if json.Valid([]byte(masked)) {
		ev.Payload = json.RawMessage(masked)
	} else {
		ev.Type = event.Error
		ev.Payload, _ = json.Marshal(event.ErrorPayload{
			Code:    "redaction",
			Message: "événement masqué illisible : non affiché",
		})
	}
	b, err := json.Marshal(ev)
	if err != nil {
		b = []byte(`{"type":"Error","payload":{"code":"redaction","message":"événement non affichable"}}`)
	}
	fmt.Fprintf(r.stdout, "%s\n", b)
}

// decode lit la charge utile ; une charge illisible est signalée sur stderr.
func (r *renderer) decode(ev event.Event, v any) bool {
	if err := json.Unmarshal(ev.Payload, v); err != nil {
		fmt.Fprintln(r.stderr, i18n.T(r.lang, "err.unreadable_event", ev.Type, err))
		return false
	}
	return true
}

// text écrit sur stderr en sortie texte uniquement (en JSON, tout est sur stdout).
func (r *renderer) text(format string, a ...any) {
	if r.out == Text {
		fmt.Fprint(r.stderr, r.redactText(fmt.Sprintf(format, a...)))
	}
}

// Banner renvoie la bannière de transparence dans la langue l :
// « tenon · <modèle> · <nom fournisseur> · <pays> · juridiction <UE|hors UE|locale> ».
// Un nom ou un pays vide est omis.
func Banner(l i18n.Lang, p llm.ProviderInfo) string {
	parts := []string{"tenon", p.Model}
	if n := providerLabel(p); n != "" {
		parts = append(parts, n)
	}
	if p.Country != "" {
		parts = append(parts, p.Country)
	}
	parts = append(parts, i18n.T(l, "banner.prefix", jurisdictionLabel(l, p.Jurisdiction)))
	return strings.Join(parts, " · ")
}

func providerLabel(p llm.ProviderInfo) string {
	if p.Name != "" {
		return p.Name
	}
	return p.ID
}

func jurisdictionLabel(l i18n.Lang, j llm.Jurisdiction) string {
	switch j {
	case llm.EU:
		return i18n.T(l, "banner.jurisdiction.eu")
	case llm.Local:
		return i18n.T(l, "banner.jurisdiction.local")
	}
	return i18n.T(l, "banner.jurisdiction.non_eu")
}

func describe(l i18n.Lang, p event.PermissionRequestedPayload) string {
	if p.Kind == "jurisdiction" && p.Provider != nil {
		return i18n.T(l, "refused.jurisdiction", providerLabel(*p.Provider), jurisdictionLabel(l, p.Provider.Jurisdiction))
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
