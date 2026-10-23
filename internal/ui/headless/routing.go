package headless

import (
	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/i18n"
	"github.com/tenon-code-eu/tenon/internal/llm"
)

// Routing suit le routage d'une session pour l'affichage : fournisseur
// local, fournisseur effectif et état sensible. Partagé avec la TUI.
type Routing struct {
	// Local est le fournisseur local ("" : inconnu ou absent).
	Local string
	// Current est le dernier fournisseur retenu.
	Current string
	// Sensitive est vrai après SessionClassified.
	Sensitive bool
}

// Started intègre SessionStarted : fournisseur courant, et fournisseur local
// (routing.local, sinon le courant s'il est local) s'il n'est pas déjà connu.
func (r *Routing) Started(p event.SessionStartedPayload) {
	if r.Current == "" {
		r.Current = p.Provider.ID
	}
	switch {
	case p.Routing != nil && p.Routing.Local != "":
		r.Local = p.Routing.Local
	case r.Local == "" && p.Provider.Jurisdiction == llm.Local:
		r.Local = p.Provider.ID
	}
}

// Classified intègre SessionClassified et renvoie son avis.
func (r *Routing) Classified(l i18n.Lang, p event.SessionClassifiedPayload) string {
	r.Sensitive = true
	if r.Local == "" {
		return i18n.T(l, "classification.sensitive_no_local", OneLine(p.Reason))
	}
	return i18n.T(l, "classification.sensitive", OneLine(p.Reason), OneLine(r.Local))
}

// Selected intègre ProviderSelected et renvoie l'avis d'une bascule ("" pour
// un autre motif).
func (r *Routing) Selected(l i18n.Lang, p event.ProviderSelectedPayload) string {
	from := r.Current
	r.Current = p.Provider.ID
	if p.Reason != "fallback" {
		return ""
	}
	if from == "" {
		from = "?"
	}
	return i18n.T(l, "routing.fallback", OneLine(from), OneLine(p.Error), OneLine(p.Provider.ID))
}
