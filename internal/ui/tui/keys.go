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
}

var keys = keyMap{
	Submit:    key.NewBinding(key.WithKeys("enter")),
	Newline:   key.NewBinding(key.WithKeys("alt+enter", "ctrl+j")),
	Cancel:    key.NewBinding(key.WithKeys("esc")),
	Interrupt: key.NewBinding(key.WithKeys("ctrl+c")),
	Complete:  key.NewBinding(key.WithKeys("tab")),
	PageUp:    key.NewBinding(key.WithKeys("pgup")),
	PageDown:  key.NewBinding(key.WithKeys("pgdown")),
}
