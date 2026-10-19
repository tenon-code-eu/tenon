package engine

import (
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/hooks"
	"github.com/tenon-code-eu/tenon/internal/llm"
	"github.com/tenon-code-eu/tenon/internal/provider"
)

const (
	// DefaultContextWindow s'applique quand Deps.ContextWindow vaut 0 ou moins.
	DefaultContextWindow = 32000
	// DefaultCompactionThreshold s'applique quand Deps.CompactionThreshold
	// vaut 0 ou moins.
	DefaultCompactionThreshold = 0.8
	// tailFraction est la part de la fenêtre réservée à la queue conservée.
	tailFraction = 0.3
)

// Raisons de fin de tour propres à la compaction.
const (
	reasonTooLarge  = "context_too_large"
	reasonBlocked   = "blocked"
	reasonCancelled = "cancelled"
)

// summaryPrompt est le prompt système de la requête de résumé.
const summaryPrompt = "Faithfully summarize the following conversation so that a coding agent can continue it: goals, decisions, modified files, open items. Reply with the summary only."

// omittedLine précède le texte à résumer quand ses plus anciens messages ont
// été omis pour tenir dans la fenêtre.
const omittedLine = "[… older messages omitted]"

// truncatedMark termine un message tronqué pour tenir dans la fenêtre.
const truncatedMark = "[… truncated]"

// summaryPrefix précède le résumé dans le message qui remplace la partie
// compactée.
const summaryPrefix = "Summary of the previous conversation:\n\n"

// summaryMessage construit le message qui remplace la partie résumée, avec le
// contexte hc des hooks SessionStart (compact) ; partagé par la compaction et
// par sa relecture dans Restore.
func summaryMessage(summary, hc string) llm.Message {
	return llm.Message{Role: llm.User, Content: withHookContext(summaryPrefix+summary, hc)}
}

// isSummary indique si m est un message de résumé construit par
// summaryMessage.
func isSummary(m llm.Message) bool {
	return m.Role == llm.User && len(m.ToolCalls) == 0 && strings.HasPrefix(m.Content, summaryPrefix)
}

// estimate approxime le nombre de jetons : (runes des contenus, des arguments
// d'appels d'outils et des schémas d'outils) / 4.
func estimate(msgs []llm.Message, defs []llm.ToolDef) int {
	n := 0
	for _, m := range msgs {
		n += utf8.RuneCountInString(m.Content)
		for _, c := range m.ToolCalls {
			n += utf8.RuneCount(c.Args)
		}
	}
	for _, d := range defs {
		n += utf8.RuneCount(d.Schema)
	}
	return n / 4
}

// tailStart renvoie l'indice, dans conv, du début de la queue conservée : le
// plus long suffixe commençant par un message user et dont l'estimation tient
// dans budget ; à défaut, le dernier message user. -1 si conv n'a aucun
// message user. Commencer par un message user garantit que la queue ne
// commence jamais par un message tool et ne sépare jamais un message
// assistant de ses résultats d'outils.
func tailStart(conv []llm.Message, budget int) int {
	last := -1
	for i, m := range conv {
		if m.Role != llm.User {
			continue
		}
		if estimate(conv[i:], nil) <= budget {
			return i
		}
		last = i
	}
	return last
}

// renderConversation rend les messages en texte pour la requête de résumé.
func renderConversation(msgs []llm.Message) string {
	var b strings.Builder
	for i, m := range msgs {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(string(m.Role))
		b.WriteString(": ")
		b.WriteString(m.Content)
		for _, c := range m.ToolCalls {
			b.WriteString("\n[tool call ")
			b.WriteString(c.Name)
			b.WriteString(" (")
			b.WriteString(c.ID)
			b.WriteString("): ")
			b.Write(safeArgs(c.Args))
			b.WriteString("]")
		}
	}
	return b.String()
}

// summaryInput rend old en texte pour la requête de résumé, de sorte que
// cette requête (prompt de résumé + texte) ne dépasse pas limit jetons
// estimés : les plus anciens messages sont omis (une ligne omittedLine le
// signale) et, si le dernier ne tient pas seul, son rendu est tronqué
// (truncatedMark).
func summaryInput(old []llm.Message, limit int) string {
	fits := func(s string) bool {
		return estimate([]llm.Message{{Content: summaryPrompt}, {Content: s}}, nil) <= limit
	}
	withOmitted := func(i int, s string) string {
		if i == 0 {
			return s
		}
		return omittedLine + "\n\n" + s
	}
	last := len(old) - 1
	for i := 0; i <= last; i++ {
		if s := withOmitted(i, renderConversation(old[i:])); fits(s) {
			return s
		}
	}
	if last < 0 {
		return ""
	}
	// Même seul, le dernier message ne tient pas : son rendu est tronqué au
	// nombre de runes restant (estimate arrondit à l'entier inférieur).
	r := []rune(withOmitted(last, renderConversation(old[last:])))
	keep := 4*limit + 3 - utf8.RuneCountInString(summaryPrompt) - utf8.RuneCountInString(truncatedMark)
	if keep < 0 {
		keep = 0
	}
	if keep < len(r) {
		r = r[:keep]
	}
	return string(r) + truncatedMark
}

// compact compacte l'historique si son estimation dépasse le seuil, ou sans
// condition si force est vrai (le fournisseur a renvoyé ErrContextLength).
// reason, si non vide, termine le tour : reasonTooLarge, reasonBlocked
// (juridiction refusée) ou reasonCancelled (err est alors l'erreur du
// contexte). Une err seule est fatale pour le tour.
func (t *turn) compact(info llm.ProviderInfo, defs []llm.ToolDef, force bool) (reason string, err error) {
	d := t.e.d
	limit := int(d.CompactionThreshold * float64(d.ContextWindow))
	h := *t.h
	before := estimate(h, defs)
	if !force && before <= limit {
		return "", nil
	}
	// Après un refus du fournisseur, la taille rejetée borne la fenêtre réelle.
	win := d.ContextWindow
	if force && before < win {
		win = before
	}
	// h[0] est le prompt système, jamais compacté.
	conv := h[1:]
	start := tailStart(conv, int(tailFraction*float64(win)))
	// Trop volumineux : rien à résumer, une queue qui ne tient pas sous le
	// seuil avec le prompt système et les outils, ou seulement un résumé
	// précédent à résumer de nouveau (aucun gain possible).
	if start <= 0 || estimate(h[:1], defs)+estimate(conv[start:], nil) > limit ||
		(start == 1 && isSummary(conv[0])) {
		return t.tooLarge()
	}

	// PreCompact (tour principal seulement) : avant la requête de résumé.
	if t.parent == "" {
		if _, err := t.runHooks(hooks.PreCompact, "", map[string]any{"trigger": "auto"}, "auto"); err != nil {
			return t.compactHookErr(err)
		}
		// continue:false : pas de requête de résumé ; la boucle arrête le tour.
		if t.stopReason != "" {
			return "", nil
		}
	}

	// Premier passage par le contrôle sortant : les arguments d'appels
	// d'outils sont masqués par RedactJSON avant d'être rendus en texte,
	// comme sur le chemin normal. La requête entière y repasse ensuite.
	old, ok, err := t.outbound(info, conv[:start])
	if err != nil {
		return "", err
	}
	if !ok {
		return reasonBlocked, nil
	}
	// La requête de résumé tient elle aussi sous le seuil de la fenêtre
	// (réduite à la taille rejetée après un refus du fournisseur).
	sumLimit := limit
	if l := int(d.CompactionThreshold * float64(win)); l < sumLimit {
		sumLimit = l
	}
	msgs, ok, err := t.outbound(info, []llm.Message{
		{Role: llm.System, Content: summaryPrompt},
		{Role: llm.User, Content: summaryInput(old, sumLimit)},
	})
	if err != nil {
		return "", err
	}
	if !ok {
		return reasonBlocked, nil
	}
	summary, _, provErr, err := t.stream(llm.Request{Model: info.Model, Messages: msgs}, false)
	if err != nil {
		return "", err
	}
	if provErr != nil {
		if cerr := t.ctx.Err(); cerr != nil {
			return reasonCancelled, cerr
		}
		// Le fournisseur refuse même la requête de résumé réduite : le
		// contexte ne peut pas être compacté.
		if errors.Is(provErr, provider.ErrContextLength) {
			return t.tooLarge()
		}
		if err := t.emit(event.Error, event.ErrorPayload{Code: "provider", Message: provErr.Error()}); err != nil {
			return "", err
		}
		return "", provErr
	}

	// SessionStart (compact, tour principal seulement) : son contexte est
	// joint au message de résumé et enregistré dans ContextCompacted.
	var hc string
	if t.parent == "" {
		out, err := t.runHooks(hooks.SessionStart, "", map[string]any{"source": "compact"}, "compact")
		if err != nil {
			return t.compactHookErr(err)
		}
		hc = out.Context
	}

	nh := make([]llm.Message, 0, 2+len(conv)-start)
	nh = append(nh, h[0], summaryMessage(summary, hc))
	nh = append(nh, conv[start:]...)
	if err := t.emit(event.ContextCompacted, event.ContextCompactedPayload{
		Summary: summary, Replaced: start, TokensBefore: before, TokensAfter: estimate(nh, defs),
		HookContext: hc,
	}); err != nil {
		return "", err
	}
	*t.h = nh
	return "", nil
}

// compactHookErr traite l'erreur d'un hook de compaction : après une annulation,
// reasonCancelled (le tour est clos par l'appelant).
func (t *turn) compactHookErr(err error) (string, error) {
	if t.ctx.Err() != nil {
		return reasonCancelled, err
	}
	return "", err
}

// tooLarge émet Error{context_too_large} et renvoie reasonTooLarge.
func (t *turn) tooLarge() (string, error) {
	if err := t.emit(event.Error, event.ErrorPayload{
		Code:    reasonTooLarge,
		Message: "context too large: cannot compact it below the model window",
	}); err != nil {
		return "", err
	}
	return reasonTooLarge, nil
}
