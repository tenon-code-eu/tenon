package tui

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"codeberg.org/tenon/tenon/internal/engine"
	"codeberg.org/tenon/tenon/internal/event"
	"codeberg.org/tenon/tenon/internal/i18n"
	"codeberg.org/tenon/tenon/internal/llm"
)

// Options configure la TUI.
type Options struct {
	Lang          i18n.Lang
	SessionID     string
	Provider      llm.ProviderInfo
	ContextWindow int
	ProjectRoot   string
	// Now remplace l'horloge (délai d'armement des acceptations, double
	// Ctrl+C). Réservé aux tests ; nil : time.Now.
	Now func() time.Time
}

const (
	defaultWidth   = 80
	defaultHeight  = 24
	inputHeight    = 3
	statusHeight   = 1
	ctrlCWindow    = 2 * time.Second
	minConvHeight  = 1
	inputPrompt    = "> "
	quitCommand    = "/quit"
	helpCommand    = "/help"
	tokenRuneRatio = 4
	// turnFinishTimeout borne l'attente de la fin du tour en cours à la
	// sortie de Run.
	turnFinishTimeout = 10 * time.Second
)

// ErrTurnNotFinished signale qu'à la sortie de la TUI le tour annulé n'a pas
// rendu la main dans le délai imparti.
var ErrTurnNotFinished = errors.New("tui: le tour ne s'est pas terminé")

// Messages internes.
type (
	// eventMsg porte un événement lu sur l'abonnement.
	eventMsg event.Event
	// subscriptionClosed signale la fermeture de l'abonnement.
	subscriptionClosed struct{}
	// submitDone signale la fin de Submit.
	submitDone struct{ err error }
)

// Model est le modèle Bubble Tea de la TUI.
type Model struct {
	eng   Engine
	opts  Options
	lang  i18n.Lang
	sub   <-chan event.Event
	tr    *Transcript
	input textarea.Model
	st    styles

	width, height int
	// scrollBack est le nombre de lignes de conversation masquées sous la vue
	// (0 : la vue suit le bas).
	scrollBack int

	// cancel annule le Submit en cours ; nil hors tour.
	cancel context.CancelFunc
	// done est fermé quand le Submit en cours a rendu la main ; nil hors tour.
	done chan struct{}
	// sent est le texte du tour en cours, restauré dans la saisie si Submit
	// échoue avant qu'un UserMessage ait été appliqué ; userSeen l'indique.
	sent      string
	userSeen  bool
	lastCtrlC time.Time
	now       func() time.Time

	// ctx est le contexte de Run, parent du contexte de chaque tour.
	ctx context.Context
	// prompts est la file des demandes de permission, la première affichée.
	prompts []*permPrompt
	// shown est la demande affichée lors du dernier changement. armedAt est
	// le début de la période de calme exigée avant une acceptation :
	// affichage de la demande, dernière touche reçue pendant une demande, ou
	// passage de la demande à l'état acceptable (horloge now).
	shown   *permPrompt
	armedAt time.Time
	// calls garde les appels d'outil en attente de résultat, dans l'ordre.
	calls []pendingCall
}

// New crée le modèle et s'abonne aux événements de eng.
func New(eng Engine, opts Options) Model {
	l := opts.Lang
	if l != i18n.EN {
		l = i18n.FR
	}
	opts.Lang = l
	in := textarea.New()
	in.Placeholder = i18n.T(l, "tui.placeholder")
	in.Prompt = inputPrompt
	in.ShowLineNumbers = false
	in.CharLimit = 0
	in.KeyMap.InsertNewline = keys.Newline
	in.SetHeight(inputHeight)
	in.Focus()
	m := Model{
		eng:   eng,
		opts:  opts,
		lang:  l,
		sub:   eng.Subscribe(),
		tr:    NewTranscript(l),
		input: in,
		st:    newStyles(lipgloss.DefaultRenderer()),
		now:   time.Now,
		ctx:   context.Background(),
	}
	if opts.Now != nil {
		m.now = opts.Now
	}
	m.resize(defaultWidth, defaultHeight)
	return m
}

// Run lance la TUI en plein écran jusqu'à ce que l'utilisateur quitte.
func Run(ctx context.Context, eng Engine, opts Options, in io.Reader, out io.Writer) error {
	m := New(eng, opts)
	// Le tour dérive du contexte de Run : une sortie anormale du programme
	// (panique comprise) l'annule aussi.
	m.ctx = ctx
	m.st = newStyles(lipgloss.NewRenderer(out))
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx), tea.WithInput(in), tea.WithOutput(out))
	final, err := p.Run()
	if fm, ok := final.(Model); ok {
		if serr := fm.shutdown(turnFinishTimeout); serr != nil {
			return serr
		}
	}
	if errors.Is(err, tea.ErrProgramKilled) && ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

// shutdown annule le tour en cours éventuel et attend que Submit rende la
// main, au plus timeout, en vidant l'abonnement pour que l'engine ne bloque
// pas sur un tampon plein et puisse journaliser la fin du tour.
func (m Model) shutdown(timeout time.Duration) error {
	if m.done == nil {
		return nil
	}
	m.cancel()
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case <-m.sub:
			case <-stop:
				return
			}
		}
	}()
	select {
	case <-m.done:
		return nil
	case <-time.After(timeout):
		return ErrTurnNotFinished
	}
}

// readEvent lit un seul événement de l'abonnement.
func readEvent(ch <-chan event.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return subscriptionClosed{}
		}
		return eventMsg(ev)
	}
}

// Init lance la lecture de l'abonnement.
func (m Model) Init() tea.Cmd { return readEvent(m.sub) }

// busy indique qu'un tour est en cours (Submit n'a pas rendu la main).
func (m Model) busy() bool { return m.cancel != nil }

// Update applique un message.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case eventMsg:
		if msg.Type == event.UserMessage && m.busy() {
			m.userSeen = true
		}
		m.tr.Apply(event.Event(msg))
		m.trackEvent(event.Event(msg))
		// La lecture n'est jamais suspendue, même pendant une demande de
		// permission : l'engine bloquerait sur un tampon plein.
		return m, readEvent(m.sub)
	case subscriptionClosed:
		return m, nil
	case submitDone:
		if m.cancel != nil {
			m.cancel()
			m.cancel = nil
		}
		m.done = nil
		m.endTurn()
		// Seul l'échec d'audit garantit que le message n'est pas parti ; pour
		// toute autre erreur, son UserMessage peut simplement ne pas être lu
		// encore, et le restaurer pousserait à l'envoyer deux fois.
		if errors.Is(msg.err, engine.ErrAuditFailure) && !m.userSeen {
			m.restoreInput()
		}
		m.sent, m.userSeen = "", false
		if msg.err != nil && !errors.Is(msg.err, context.Canceled) {
			m.tr.add(Item{Kind: ItemError, Text: i18n.T(m.lang, "err.prefix", msg.err)})
		}
		return m, nil
	case tea.WindowSizeMsg:
		before := m.prompting() && m.promptHeadVisible()
		m.resize(msg.Width, msg.Height)
		m.rearmIf(before)
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.prompting() {
		return m.handlePromptKey(msg)
	}
	switch {
	case key.Matches(msg, keys.Interrupt):
		now := m.now()
		if !m.busy() || (!m.lastCtrlC.IsZero() && now.Sub(m.lastCtrlC) < ctrlCWindow) {
			return m.quit()
		}
		m.lastCtrlC = now
		m.cancel()
		return m, nil
	case key.Matches(msg, keys.Cancel):
		if m.busy() {
			m.cancel()
		}
		return m, nil
	case key.Matches(msg, keys.PageUp):
		m.scroll(m.convHeight())
		return m, nil
	case key.Matches(msg, keys.PageDown):
		m.scroll(-m.convHeight())
		return m, nil
	case key.Matches(msg, keys.Submit):
		return m.submit()
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// quit annule le tour en cours éventuel et quitte.
func (m Model) quit() (tea.Model, tea.Cmd) {
	if m.cancel != nil {
		m.cancel()
	}
	return m, tea.Quit
}

// submit traite Entrée : commandes texte, ou envoi de la saisie hors tour.
func (m Model) submit() (tea.Model, tea.Cmd) {
	if m.busy() {
		return m, nil
	}
	text := strings.TrimSpace(m.input.Value())
	switch text {
	case "":
		return m, nil
	case quitCommand:
		return m.quit()
	case helpCommand:
		m.input.Reset()
		m.tr.notice(i18n.T(m.lang, "tui.help"))
		m.scrollBack = 0
		return m, nil
	}
	m.input.Reset()
	m.scrollBack = 0
	ctx, cancel := context.WithCancel(m.ctx)
	done := make(chan struct{})
	m.cancel, m.done = cancel, done
	m.sent, m.userSeen = text, false
	m.lastCtrlC = time.Time{}
	eng, sid := m.eng, m.opts.SessionID
	return m, func() tea.Msg {
		err := eng.Submit(ctx, sid, text)
		close(done)
		return submitDone{err: err}
	}
}

// restoreInput remet le texte envoyé dans la saisie, devant ce qui a pu être
// tapé pendant le tour.
func (m *Model) restoreInput() {
	if m.sent == "" {
		return
	}
	v := m.sent
	if cur := m.input.Value(); cur != "" {
		v += "\n" + cur
	}
	m.input.SetValue(v)
}

func (m *Model) resize(w, h int) {
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	m.width, m.height = w, h
	m.input.SetWidth(w)
	m.input.SetHeight(inputHeight)
}

// bottomLines rend la zone sous la conversation : le panneau de la demande de
// permission affichée, sinon la saisie (conservée telle quelle).
func (m Model) bottomLines() []string {
	if m.prompting() {
		return m.promptLines()
	}
	return strings.Split(m.input.View(), "\n")
}

// convHeight renvoie le nombre de lignes de la zone de conversation.
func (m Model) convHeight() int {
	bottom := inputHeight
	if m.prompting() {
		bottom = len(m.promptLines())
	}
	return max(minConvHeight, m.height-bottom-statusHeight)
}

// scroll déplace la vue de n lignes vers le haut (n < 0 : vers le bas).
func (m *Model) scroll(n int) {
	limit := max(0, len(m.conversationLines())-m.convHeight())
	m.scrollBack = min(max(0, m.scrollBack+n), limit)
}
