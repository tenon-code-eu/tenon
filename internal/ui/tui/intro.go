package tui

import (
	"io"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"
)

// introOffsets est l'écart, image par image, entre les deux moitiés du
// bouclier qui glissent l'une vers l'autre ; la dernière image (0) est l'art
// de l'accueil.
var introOffsets = [...]int{14, 10, 7, 4, 2, 1, 0}

const (
	// introFrames est le nombre d'images de l'animation d'ouverture.
	introFrames = len(introOffsets)
	// introDelay est la durée d'une image (moins de 700 ms au total).
	introDelay = 80 * time.Millisecond
)

// introTickMsg fait passer l'animation d'ouverture à l'image suivante.
type introTickMsg struct{}

// introTick programme l'image suivante.
func introTick() tea.Cmd {
	return tea.Tick(introDelay, func(time.Time) tea.Msg { return introTickMsg{} })
}

// introEnabled indique si l'animation d'ouverture est jouée : animation
// active, sortie TTY, sans NO_COLOR, au moins homeMinWide colonnes.
func introEnabled(animation, tty bool, width int) bool {
	return animation && tty && !noColor() && width >= homeMinWide
}

// isTTY indique que w est un terminal.
func isTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// introActive indique que l'animation d'ouverture est en cours.
func (m Model) introActive() bool { return m.introFrame >= 0 }

// stopIntro arrête l'animation d'ouverture (l'accueil s'affiche tel quel).
func (m *Model) stopIntro() { m.introFrame = -1 }

// introStep passe à l'image suivante, ou termine l'animation après la
// dernière image, si le terminal rétrécit ou si l'accueil disparaît.
func (m Model) introStep() (Model, tea.Cmd) {
	if !m.introActive() {
		return m, nil
	}
	m.introFrame++
	if m.introFrame >= introFrames || m.width < homeMinWide || !m.homeShown() {
		m.stopIntro()
		return m, nil
	}
	return m, introTick()
}
