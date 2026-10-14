package engine

import (
	"strings"
	"unicode/utf8"

	"codeberg.org/tenon/tenon/internal/event"
	"codeberg.org/tenon/tenon/internal/llm"
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
const summaryPrompt = "Résume fidèlement la conversation suivante pour qu'un agent de code puisse la poursuivre : objectifs, décisions, fichiers modifiés, points en suspens. Réponds uniquement par le résumé."

// summaryPrefix précède le résumé dans le message qui remplace la partie
// compactée.
const summaryPrefix = "Résumé de la conversation précédente :\n\n"

// summaryMessage construit le message qui remplace la partie résumée ; partagé
// par la compaction et par sa relecture dans Restore.
func summaryMessage(summary string) llm.Message {
	return llm.Message{Role: llm.User, Content: summaryPrefix + summary}
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
		b.WriteString(" : ")
		b.WriteString(m.Content)
		for _, c := range m.ToolCalls {
			b.WriteString("\n[appel d'outil ")
			b.WriteString(c.Name)
			b.WriteString(" (")
			b.WriteString(c.ID)
			b.WriteString(") : ")
			b.Write(safeArgs(c.Args))
			b.WriteString("]")
		}
	}
	return b.String()
}

// compact compacte l'historique si son estimation dépasse le seuil, ou sans
// condition si force est vrai (le fournisseur a renvoyé ErrContextLength).
// reason, si non vide, termine le tour : reasonTooLarge, reasonBlocked
// (juridiction refusée) ou reasonCancelled (err est alors l'erreur du
// contexte). Une err seule est fatale pour le tour.
func (t *turn) compact(info llm.ProviderInfo, defs []llm.ToolDef, force bool) (reason string, err error) {
	d := t.e.d
	limit := int(d.CompactionThreshold * float64(d.ContextWindow))
	h := t.s.history
	before := estimate(h, defs)
	if !force && before <= limit {
		return "", nil
	}
	// h[0] est le prompt système, jamais compacté.
	conv := h[1:]
	start := tailStart(conv, int(tailFraction*float64(d.ContextWindow)))
	if start <= 0 || estimate(conv[start:], nil) > limit {
		if err := t.emit(event.Error, event.ErrorPayload{
			Code:    reasonTooLarge,
			Message: "contexte trop volumineux : impossible de le compacter sous la fenêtre du modèle",
		}); err != nil {
			return "", err
		}
		return reasonTooLarge, nil
	}

	msgs, ok, err := t.outbound(info, []llm.Message{
		{Role: llm.System, Content: summaryPrompt},
		{Role: llm.User, Content: renderConversation(conv[:start])},
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
		if err := t.emit(event.Error, event.ErrorPayload{Code: "provider", Message: provErr.Error()}); err != nil {
			return "", err
		}
		return "", provErr
	}

	nh := make([]llm.Message, 0, 2+len(conv)-start)
	nh = append(nh, h[0], summaryMessage(summary))
	nh = append(nh, conv[start:]...)
	if err := t.emit(event.ContextCompacted, event.ContextCompactedPayload{
		Summary: summary, Replaced: start, TokensBefore: before, TokensAfter: estimate(nh, defs),
	}); err != nil {
		return "", err
	}
	t.s.history = nh
	return "", nil
}
