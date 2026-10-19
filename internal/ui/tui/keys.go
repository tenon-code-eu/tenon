package tui

import "github.com/charmbracelet/bubbles/key"

// keyMap regroupe les touches de la TUI.
type keyMap struct {
	Submit    key.Binding // envoie la saisie
	Newline   key.Binding // insère un retour à la ligne
	Cancel    key.Binding // annule le tour en cours
	Interrupt key.Binding // annule le tour, ou quitte
	Complete  key.Binding // complète une commande /...
	PageUp    key.Binding
	PageDown  key.Binding
	LineUp    key.Binding // une ligne vers le haut
	LineDown  key.Binding // une ligne vers le bas
	// HalfUp, HalfDown, Top et Bottom ne font défiler que si la saisie est
	// vide : sinon ils gardent leur rôle d'édition (Ctrl+U, Ctrl+D, Début, Fin).
	HalfUp   key.Binding
	HalfDown key.Binding
	Top      key.Binding
	Bottom   key.Binding
}

var keys = keyMap{
	Submit:    key.NewBinding(key.WithKeys("enter")),
	Newline:   key.NewBinding(key.WithKeys("alt+enter", "ctrl+j")),
	Cancel:    key.NewBinding(key.WithKeys("esc")),
	Interrupt: key.NewBinding(key.WithKeys("ctrl+c")),
	Complete:  key.NewBinding(key.WithKeys("tab")),
	PageUp:    key.NewBinding(key.WithKeys("pgup")),
	PageDown:  key.NewBinding(key.WithKeys("pgdown")),
	LineUp:    key.NewBinding(key.WithKeys("shift+up")),
	LineDown:  key.NewBinding(key.WithKeys("shift+down")),
	HalfUp:    key.NewBinding(key.WithKeys("ctrl+u")),
	HalfDown:  key.NewBinding(key.WithKeys("ctrl+d")),
	Top:       key.NewBinding(key.WithKeys("home")),
	Bottom:    key.NewBinding(key.WithKeys("end")),
}
