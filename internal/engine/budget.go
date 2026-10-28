package engine

import (
	"fmt"
	"slices"
	"strings"

	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/llm"
	"github.com/tenon-code-eu/tenon/internal/routing"
)

// Objets d'un appel au modèle (UsagePayload.Purpose).
const (
	purposeTurn       = "turn"
	purposeCompaction = "compaction"
	purposeSubagent   = "subagent"
)

// reasonBudget termine un tour refusé faute de budget de jetons.
const reasonBudget = "budget_exceeded"

// callPurpose est l'objet des envois du tour hors compaction : itération
// d'un sous-agent, ou du tour (hooks prompt et agent compris).
func (t *turn) callPurpose() string {
	if t.purpose == "" {
		return purposeTurn
	}
	return t.purpose
}

// candidates renvoie l'ordre d'essai d'un envoi de l'objet purpose. Une
// session sensible n'a que le fournisseur local du routeur de la session,
// quoi que disent les routeurs dédiés. Sinon, les candidats du routeur dédié
// (compaction, sous-agents) passent d'abord, suivis de ceux de la session :
// un fournisseur dédié refusé par les garde-fous (l'événement du refus sert
// d'avis) ou indisponible laisse la place au fournisseur de la session.
func (t *turn) candidates(purpose string, sens bool) []routing.Candidate {
	base := t.e.router.Candidates(sens)
	if sens {
		return base
	}
	var ded *routing.Router
	switch purpose {
	case purposeCompaction:
		ded = t.e.d.CompactionRouter
	case purposeSubagent:
		ded = t.e.d.SubagentRouter
	}
	if ded == nil {
		return base
	}
	out := ded.Candidates(false)
	seen := map[string]bool{}
	for _, c := range out {
		seen[c.ID] = true
	}
	for _, c := range base {
		if !seen[c.ID] {
			seen[c.ID] = true
			out = append(out, c)
		}
	}
	return out
}

// windowFor renvoie la fenêtre de contexte du premier candidat d'un envoi de
// l'objet purpose (Deps.ContextWindow si elle est inconnue).
func (t *turn) windowFor(purpose string) int {
	if t.e.router != nil {
		if c := t.candidates(purpose, t.s.isSensitive()); len(c) > 0 && c[0].Window > 0 {
			return c[0].Window
		}
	}
	return t.e.d.ContextWindow
}

// candWindow renvoie la fenêtre de contexte du candidat c
// (Deps.ContextWindow si elle est inconnue).
func (t *turn) candWindow(c routing.Candidate) int {
	if c.Window > 0 {
		return c.Window
	}
	return t.e.d.ContextWindow
}

// sortedDefs renvoie defs triées par nom : une copie si l'ordre change,
// defs elle-même si elles le sont déjà. L'entrée n'est jamais modifiée.
func sortedDefs(defs []llm.ToolDef) []llm.ToolDef {
	byName := func(a, b llm.ToolDef) int { return strings.Compare(a.Name, b.Name) }
	if slices.IsSortedFunc(defs, byName) {
		return defs
	}
	out := slices.Clone(defs)
	slices.SortStableFunc(out, byName)
	return out
}

// usage émet l'événement Usage d'un appel au fournisseur info : consommation
// rapportée par le flux (u), ou à défaut estimée (requête envoyée en entrée,
// texte et arguments d'appels reçus en sortie) dès que le flux a commencé
// ou a livré du texte ou des appels, quelle que soit l'erreur qui l'a
// terminé (annulation, coupure réseau, flux incomplet) : le fournisseur a
// consommé l'entrée. Seul un appel resté sans aucune réponse n'émet rien.
// L'émission ne dépend pas du contexte du tour (elle a lieu aussi après une
// annulation). Le compteur de la session est ensuite mis à jour, et l'avis
// BudgetWarning émis au premier franchissement du seuil. window est la
// fenêtre de contexte du fournisseur de l'appel.
func (t *turn) usage(info llm.ProviderInfo, window int, purpose string, req llm.Request, res tryResult, u *llm.Usage) error {
	p := event.UsagePayload{
		Provider: info.ID, ProviderName: info.Name, Jurisdiction: info.Jurisdiction,
		Model: info.Model, Purpose: purpose, Window: window,
	}
	switch {
	case u != nil:
		p.Input, p.Output, p.Cached = u.Input, u.Output, u.Cached
	case res.started || res.text != "" || len(res.calls) > 0:
		p.Input = estimate(req.Messages, req.Tools)
		p.Output = estimate([]llm.Message{{Content: res.text, ToolCalls: res.calls}}, nil)
		p.Estimated = true
	default:
		return nil
	}
	// Compteur d'abord : il compte l'appel même si l'émission échoue.
	b := t.e.d.Budget
	used, warn := t.s.addUsage(int64(p.Input)+int64(p.Output), b)
	if err := t.emit(event.Usage, p); err != nil {
		return err
	}
	if !warn {
		return nil
	}
	return t.e.emit(t.s, event.BudgetWarning, event.BudgetWarningPayload{Used: used, Max: b.SessionTokens})
}

// addUsage ajoute n au compteur de jetons de la session et renvoie le total ;
// warn est vrai la première fois que le total atteint le seuil d'avis de b
// (une seule fois par session, reprise comprise).
func (s *session) addUsage(n int64, b Budget) (used int64, warn bool) {
	s.classMu.Lock()
	defer s.classMu.Unlock()
	s.used += n
	if b.SessionTokens > 0 && !s.budgetWarned && float64(s.used) >= b.WarnAt*float64(b.SessionTokens) {
		s.budgetWarned = true
		warn = true
	}
	return s.used, warn
}

// SessionTokens renvoie le compteur de jetons (input + output) de la session
// sessionID : 0 si elle est inconnue. Sert à l'affichage du total cumulé,
// reprise comprise.
func (e *Engine) SessionTokens(sessionID string) int64 {
	s := e.lookup(sessionID)
	if s == nil {
		return 0
	}
	s.classMu.Lock()
	defer s.classMu.Unlock()
	return s.used
}

// overBudget indique que le budget b de la session est épuisé.
func (s *session) overBudget(b Budget) (used, max int64, over bool) {
	s.classMu.Lock()
	defer s.classMu.Unlock()
	return s.used, b.SessionTokens, b.SessionTokens > 0 && s.used >= b.SessionTokens
}

// budgetRefused est le résultat d'un appel agent refusé faute de budget.
func budgetRefused(used, max int64) string {
	return fmt.Sprintf("session token budget exceeded: %d of %d tokens used; the sub-agent was not started", used, max)
}
