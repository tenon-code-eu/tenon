// Package headless exécute un tour d'agent sans interface interactive : toute
// demande de permission est refusée, la sortie est du texte ou du JSON.
package headless

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"

	"github.com/tenon-code-eu/tenon/internal/engine"
	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/i18n"
	"github.com/tenon-code-eu/tenon/internal/llm"
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

// Run soumet in à eng dans la session sessionID et renvoie le code de
// sortie : 3 si une action a été refusée (permission, politique) ou le tour
// bloqué, 1 si SubmitInput échoue ou si le tour n'a pas pu tourner (contexte trop
// volumineux), sinon 0.
func Run(ctx context.Context, eng *engine.Engine, sessionID string, in engine.Input, opts Options, stdout, stderr io.Writer) int {
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
	go func() { errc <- eng.SubmitInput(ctx, sessionID, in) }()

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
			if line := MCPBanner(r.lang, p.MCP); line != "" {
				r.text("%s\n", line)
			}
			if len(p.Instructions) > 0 {
				r.text("%s\n", i18n.T(r.lang, "banner.instructions", strings.Join(p.Instructions, ", ")))
			}
		}
	case event.McpServerDown:
		var p event.McpServerDownPayload
		if r.decode(ev, &p) {
			r.text("%s\n", ServerDownNotice(r.lang, p, r.redactText))
		}
	case event.JurisdictionWarning:
		var p event.JurisdictionWarningPayload
		if r.decode(ev, &p) {
			r.text("%s\n", i18n.T(r.lang, "warn.non_eu",
				ProviderLabel(p.Provider), JurisdictionLabel(r.lang, p.Provider.Jurisdiction)))
		}
	case event.PermissionRequested:
		var p event.PermissionRequestedPayload
		if !r.decode(ev, &p) {
			return
		}
		r.refused = true
		// Respond n'échoue que si la demande est déjà résolue (contexte annulé).
		_ = r.eng.Respond(p.PermissionID, engine.Reply{Allow: false})
		r.text("%s\n", i18n.T(r.lang, "refused.user", describe(r.lang, p, r.redactText)))
	case event.ToolResult:
		var p event.ToolResultPayload
		if r.decode(ev, &p) && p.Denied != "" {
			r.refused = true
			if p.Denied == "policy" {
				r.text("%s\n", i18n.T(r.lang, "refused.policy", r.redactText(p.Tool)))
			}
		}
	case event.AssistantMessage:
		var p event.AssistantMessagePayload
		if r.decode(ev, &p) && p.Text != "" && p.ParentCallID == "" {
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
			msg := p.Message
			if p.Code == "context_too_large" {
				msg = i18n.T(r.lang, "err.context_too_large")
			}
			r.text("%s\n", i18n.T(r.lang, "err.event", p.Code, msg))
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
			Message: "unreadable redacted event: not shown",
		})
	}
	b, err := json.Marshal(ev)
	if err != nil {
		b = []byte(`{"type":"Error","payload":{"code":"redaction","message":"event cannot be shown"}}`)
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
	if n := ProviderLabel(p); n != "" {
		parts = append(parts, n)
	}
	if p.Country != "" {
		parts = append(parts, p.Country)
	}
	parts = append(parts, i18n.T(l, "banner.prefix", JurisdictionLabel(l, p.Jurisdiction)))
	return strings.Join(parts, " · ")
}

// ServerDownNotice renvoie l'avis d'un serveur MCP indisponible. Chaque
// partie est nettoyée puis masquée seule, lignes conservées (une règle ancrée
// en fin de ligne y reconnaît un secret), avant d'être mise sur une ligne.
func ServerDownNotice(l i18n.Lang, p event.McpServerDownPayload, redact func(string) string) string {
	return i18n.T(l, "mcp.down", cleanMasked(p.Server, redact), cleanMasked(p.Error, redact))
}

// MCPBanner renvoie la ligne « MCP : id (juridiction), … » des serveurs MCP
// actifs, ou "" s'il n'y en a aucun.
func MCPBanner(l i18n.Lang, servers []event.MCPServerInfo) string {
	if len(servers) == 0 {
		return ""
	}
	parts := make([]string, len(servers))
	for i, s := range servers {
		parts[i] = OneLine(s.ID) + " (" + JurisdictionLabel(l, s.Jurisdiction) + ")"
	}
	return i18n.T(l, "banner.mcp", strings.Join(parts, ", "))
}

// Clean retire de s les séquences d'échappement ANSI, les caractères de
// contrôle autres que le saut de ligne et les caractères de format
// (bidirectionnels, largeur nulle) ; tabulations, retours chariot et
// séparateurs Unicode deviennent des espaces. Le texte vient d'un serveur MCP,
// non fiable. Les lignes sont conservées : un masquage ancré en fin de ligne
// s'applique ensuite.
func Clean(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n':
			return r
		case r == '\r', r == '\t', r == '\u2028', r == '\u2029':
			return ' '
		case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f, r == utf8.RuneError, unicode.Is(unicode.Cf, r):
			return -1
		}
		return r
	}, ansi.Strip(s))
}

// OneLine nettoie s (Clean) et le met sur une ligne.
func OneLine(s string) string { return strings.ReplaceAll(Clean(s), "\n", " ") }

// cleanMasked nettoie s, le masque par redact puis le met sur une ligne.
func cleanMasked(s string, redact func(string) string) string {
	return strings.ReplaceAll(redact(Clean(s)), "\n", " ")
}

// ProviderLabel renvoie le nom du fournisseur, à défaut son identifiant.
func ProviderLabel(p llm.ProviderInfo) string {
	if p.Name != "" {
		return p.Name
	}
	return p.ID
}

// JurisdictionLabel renvoie le libellé traduit de la juridiction j.
func JurisdictionLabel(l i18n.Lang, j llm.Jurisdiction) string {
	switch j {
	case llm.EU:
		return i18n.T(l, "banner.jurisdiction.eu")
	case llm.Local:
		return i18n.T(l, "banner.jurisdiction.local")
	}
	return i18n.T(l, "banner.jurisdiction.non_eu")
}

// describe décrit la demande refusée. Chaque partie (outil, commande, chemin)
// est masquée par redact avant d'être jointe : une règle ancrée en fin de
// ligne ne reconnaîtrait pas un secret suivi d'une autre partie.
func describe(l i18n.Lang, p event.PermissionRequestedPayload, redact func(string) string) string {
	if p.Kind == "jurisdiction" && p.Provider != nil {
		return i18n.T(l, "refused.jurisdiction", redact(ProviderLabel(*p.Provider)), JurisdictionLabel(l, p.Provider.Jurisdiction))
	}
	s := redact(p.Tool)
	if p.Command != "" {
		s += " " + redact(p.Command)
	}
	if len(p.Paths) > 0 {
		paths := make([]string, len(p.Paths))
		for i, path := range p.Paths {
			paths[i] = redact(path)
		}
		s += " " + strings.Join(paths, ", ")
	}
	return s
}
