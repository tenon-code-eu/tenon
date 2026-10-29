package tui

import (
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tenon-code-eu/tenon/internal/agents"
	"github.com/tenon-code-eu/tenon/internal/engine"
	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/hooks"
	"github.com/tenon-code-eu/tenon/internal/i18n"
	"github.com/tenon-code-eu/tenon/internal/llm"
	"github.com/tenon-code-eu/tenon/internal/memory"
	"github.com/tenon-code-eu/tenon/internal/session"
	"github.com/tenon-code-eu/tenon/internal/skills"
	"github.com/tenon-code-eu/tenon/internal/ui/headless"
)

// Options configure la TUI.
type Options struct {
	Lang i18n.Lang
	// Theme est le thème de la TUI ([ui] theme ; "" ou inconnu : tenon).
	Theme string
	// Animation active l'animation d'ouverture ([ui] animation).
	Animation     bool
	SessionID     string
	Provider      llm.ProviderInfo
	ContextWindow int
	// SessionTokens : total de jetons déjà compté (session reprise).
	SessionTokens int64
	ProjectRoot   string
	// Profile est le profil de conformité actif, affiché dans la barre
	// d'état ("" : aucun profil).
	Profile string
	// Policy est la ligne de la politique d'organisation, déjà traduite
	// (« policy: acme-dev v3 (key …) »), affichée sous celle du profil
	// ("" : aucune politique, aucune ligne).
	Policy string
	// Local est le fournisseur local du routage ("" : celui de
	// SessionStarted, ou le fournisseur courant s'il est local).
	Local string
	// Version est la version affichée dans l'accueil ("dev" si vide).
	Version string
	// Redact masque tout texte venu des événements avant affichage (même
	// fonction que le mode non interactif) ; nil : aucun masquage.
	Redact func(string) string
	// DescribeMCP renvoie le serveur MCP (identifiant, juridiction, pays) d'un
	// outil ; nil : registre du paquet mcp.
	DescribeMCP func(tool string) (event.MCPServerInfo, bool)
	// Notices sont des avis du démarrage (avertissements MCP) placés en tête
	// de la conversation, nettoyés et masqués par Redact.
	Notices []string
	// Skills est le catalogue des skills (/<nom>, /skills, complétion) ;
	// nil : aucun skill.
	Skills *skills.Catalog
	// Agents est le catalogue des agents (/agents) ; nil : aucun agent.
	Agents *agents.Catalog
	// Hooks est l'ensemble des hooks chargés (/hooks) ; nil : aucun hook.
	Hooks *hooks.Set
	// Memory est le store mémoire (/memory) ; nil : mémoire coupée, /memory
	// et /remember répondent « memory: disabled by <MemoryDisabledBy> ».
	Memory *memory.Store
	// MemoryDisabledBy : "policy" ou "config" ("" : config).
	MemoryDisabledBy string
	// MemoryWriteSeqs : séquences des MemoryWritten de la session reprise
	// (--resume), pour l'avis de /rewind.
	MemoryWriteSeqs []uint64
	// Resumed indique une session reprise (--resume) : le fil s'affiche
	// directement, sans l'accueil.
	Resumed bool
	// Sandbox est l'état d'isolation des commandes shell, pour la pastille de
	// la barre d'état : SandboxOn, SandboxOff, ou "" (inconnu : aucune pastille).
	Sandbox string
	// Providers sont les fournisseurs configurés, par identifiant : pays et
	// juridiction des envois du panneau latéral et des badges (Usage ne porte
	// pas de pays) ; nil : pays inconnus.
	Providers map[string]llm.ProviderInfo
	// History sont les événements de la session reprise (--resume) : ils
	// restaurent le panneau latéral seul (titre, envois, fichiers, jetons,
	// jauge), jamais la transcription.
	History []event.Event
	// Sessions sont les sessions passées du dossier (palette Ctrl+P), la plus
	// récente d'abord ; la session courante en est exclue à l'affichage.
	Sessions []session.Summary
	// Now remplace l'horloge (délai d'armement des acceptations, double
	// Ctrl+C). Réservé aux tests ; nil : time.Now.
	Now func() time.Time
}

const (
	defaultWidth  = 80
	defaultHeight = 24
	inputHeight   = 3
	statusHeight  = 1
	ctrlCWindow   = 2 * time.Second
	// wheelLines est le nombre de lignes défilées par cran de molette.
	wheelLines     = 3
	minConvHeight  = 1
	inputPrompt    = "> "
	quitCommand    = "/quit"
	helpCommand    = "/help"
	skillsCommand  = "/skills"
	agentsCommand  = "/agents"
	hooksCommand   = "/hooks"
	memoryCommand  = "/memory"
	themeCommand   = "/theme"
	tokenRuneRatio = 4
	// turnFinishTimeout borne l'attente de la fin du tour en cours à la
	// sortie de Run.
	turnFinishTimeout = 10 * time.Second
)

// ErrTurnNotFinished signale qu'à la sortie de la TUI le tour annulé n'a pas
// rendu la main dans le délai imparti.
var ErrTurnNotFinished = errors.New("tui: the turn did not finish")

// Messages internes.
type (
	// eventMsg porte un événement lu sur l'abonnement.
	eventMsg event.Event
	// subscriptionClosed signale la fermeture de l'abonnement.
	subscriptionClosed struct{}
	// submitDone signale la fin de Submit.
	submitDone struct{ err error }
)

// turnState est l'état du tour en cours. Il est partagé par pointer entre
// les copies du modèle et Run : après une panique, Bubble Tea ne rend pas de
// modèle final, et Run doit pourtant annuler le tour et l'attendre.
type turnState struct {
	mu sync.Mutex
	// cancel annule le Submit en cours ; nil hors tour.
	cancel context.CancelFunc
	// done est fermé quand le Submit en cours a rendu la main ; nil hors tour.
	done chan struct{}
}

func (t *turnState) start(cancel context.CancelFunc, done chan struct{}) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cancel, t.done = cancel, done
}

// active indique qu'un tour est en cours.
func (t *turnState) active() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cancel != nil
}

// stop annule le tour en cours éventuel, sans l'oublier.
func (t *turnState) stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cancel != nil {
		t.cancel()
	}
}

// finish annule et oublie le tour terminé.
func (t *turnState) finish() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cancel != nil {
		t.cancel()
	}
	t.cancel, t.done = nil, nil
}

// pending renvoie le canal de fin du tour en cours, ou nil.
func (t *turnState) pending() chan struct{} {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.done
}

// Model est le modèle Bubble Tea de la TUI.
type Model struct {
	eng   Engine
	opts  Options
	lang  i18n.Lang
	sub   <-chan event.Event
	tr    *Transcript
	input textarea.Model
	st    styles
	// theme est le thème actif (Options.Theme, ou /theme pour la session).
	theme Theme

	width, height int
	// scrollBack est le nombre de lignes de conversation masquées sous la vue
	// (0 : la vue suit le bas).
	scrollBack int
	// unseen indique qu'un texte est arrivé sous la vue pendant la lecture.
	unseen bool

	// turn est l'état du tour en cours, partagé par toutes les copies du
	// modèle et par Run, qui l'annule et l'attend même sans modèle final.
	turn *turnState
	// sent est le texte du tour en cours, restauré dans la saisie si Submit
	// échoue avant qu'un UserMessage ait été appliqué ; userSeen l'indique.
	sent      string
	userSeen  bool
	lastCtrlC time.Time
	now       func() time.Time
	// lastEsc est l'heure du dernier Échap hors tour (double Échap : /rewind).
	lastEsc time.Time
	// rw est le panneau /rewind ; nil quand il est fermé.
	rw *rewindUI
	// memSeqs : séquences des MemoryWritten vus (avis de /rewind).
	memSeqs []uint64

	// redact masque tout texte venu des événements avant affichage.
	redact func(string) string
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
	// subagents garde les sous-agents lancés pendant le tour, par call_id de
	// l'appel « agent » ; vidé en fin de tour.
	subagents map[string]subagentInfo

	// homeDone indique que l'accueil a cédé la place au fil (premier envoi,
	// session reprise) ; seedItems est le nombre d'éléments de la
	// transcription à l'ouverture (avis du démarrage, affichés sous
	// l'accueil) : tout élément de plus masque l'accueil.
	homeDone  bool
	seedItems int
	// links indique que le terminal gère les hyperliens OSC 8.
	links bool
	// tip est l'astuce du jour de l'accueil.
	tip string
	// introFrame est l'image courante de l'animation d'ouverture (-1 :
	// aucune animation).
	introFrame int
	// inputW est la largeur appliquée à la saisie (cadre de l'accueil, ou
	// toute la largeur).
	inputW int
	// stats agrège la session pour le panneau latéral (partagé par les copies
	// du modèle).
	stats *sessionStats
	// panelSet indique un choix manuel du panneau (Ctrl+B), qui prime sur la
	// largeur jusqu'à la fin de la session ; panelOpen est ce choix.
	panelSet, panelOpen bool
	// pal est la palette Ctrl+P ; nil quand elle est fermée.
	pal *paletteUI
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
	th, ok := ThemeByName(opts.Theme)
	if !ok {
		th = defaultTheme()
	}
	m := Model{
		eng:    eng,
		opts:   opts,
		lang:   l,
		sub:    eng.Subscribe(),
		tr:     seededTranscript(l, opts.SessionTokens),
		redact: opts.Redact,
		input:  in,
		st:     newStyles(lipgloss.DefaultRenderer(), th),
		theme:  th,
		now:    time.Now,
		ctx:    context.Background(),
		turn:   &turnState{},
	}
	// Écritures de mémoire d'avant la reprise : comptées par /rewind.
	m.memSeqs = slices.Clone(opts.MemoryWriteSeqs)
	if opts.Now != nil {
		m.now = opts.Now
	}
	if m.redact == nil {
		m.redact = func(s string) string { return s }
	}
	m.tr.SetRedact(opts.Redact)
	m.tr.routing = headless.Routing{Local: opts.Local, Current: opts.Provider.ID}
	if opts.Local == "" && opts.Provider.Jurisdiction == llm.Local {
		m.tr.routing.Local = opts.Provider.ID
	}
	for _, n := range opts.Notices {
		m.tr.notice(strings.ReplaceAll(m.redact(headless.Clean(n)), "\n", " "))
	}
	m.seedItems = len(m.tr.Items())
	m.homeDone = opts.Resumed
	m.links = supportsHyperlinks(os.Getenv)
	m.tip = tipOfTheDay(l, rand.IntN)
	m.introFrame = -1
	m.stats = newSessionStats(opts)
	for _, ev := range opts.History {
		m.stats.apply(ev)
	}
	m.resize(defaultWidth, defaultHeight)
	return m
}

// Run lance la TUI en plein écran jusqu'à ce que l'utilisateur quitte.
func Run(ctx context.Context, eng Engine, opts Options, in io.Reader, out io.Writer) error {
	// Le tour dérive d'un contexte propre à Run, annulé à sa sortie quelle
	// qu'elle soit (panique comprise).
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	m := New(eng, opts)
	m.ctx = runCtx
	m.st = newStyles(lipgloss.NewRenderer(out), m.theme)
	m.maybeStartIntro(isTTY(out))
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithContext(ctx), tea.WithInput(in), tea.WithOutput(out))
	_, err := p.Run()
	// L'état du tour est partagé : annulation, vidage de l'abonnement et
	// attente ont lieu même si Bubble Tea ne rend pas de modèle final.
	cancel()
	if serr := m.shutdown(turnFinishTimeout); serr != nil {
		return serr
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
	done := m.turn.pending()
	if done == nil {
		return nil
	}
	m.turn.stop()
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
	case <-done:
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

// Init lance la lecture de l'abonnement, et l'animation d'ouverture.
func (m Model) Init() tea.Cmd {
	if m.introActive() {
		return tea.Batch(readEvent(m.sub), introTick())
	}
	return readEvent(m.sub)
}

// busy indique qu'un tour est en cours (Submit n'a pas rendu la main).
func (m Model) busy() bool { return m.turn.active() }

// Update applique un message, puis ajuste la saisie à la vue (cadre de
// l'accueil ou toute la largeur).
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.update(msg)
	nm := next.(Model)
	nm.syncInput()
	return nm, cmd
}

func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case introTickMsg:
		return m.introStep()
	case eventMsg:
		if msg.Type == event.UserMessage && m.busy() {
			m.userSeen = true
		}
		// Pendant la lecture (vue remontée), la vue reste sur les mêmes
		// lignes : le texte qui arrive s'ajoute dessous, sans la déplacer.
		reading := m.scrollBack > 0
		var before int
		if reading {
			before = len(m.conversationLines())
		}
		m.tr.Apply(event.Event(msg))
		m.stats.apply(event.Event(msg))
		if reading {
			if added := len(m.conversationLines()) - before; added > 0 {
				m.scrollBack += added
				m.unseen = true
			}
		}
		preview := m.trackEvent(event.Event(msg))
		// La lecture n'est jamais suspendue, même pendant une demande de
		// permission : l'engine bloquerait sur un tampon plein.
		return m, tea.Batch(readEvent(m.sub), preview)
	case previewMsg:
		m.applyPreview(msg)
		return m, nil
	case subscriptionClosed:
		return m, nil
	case rewindDone:
		m.finishRewind(msg)
		return m, nil
	case submitDone:
		m.turn.finish()
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
		before := m.prompting() && m.approvable()
		m.resize(msg.Width, msg.Height)
		if m.width < homeMinWide {
			m.stopIntro()
		}
		m.rearmIf(before)
		return m, nil
	case tea.KeyMsg:
		// Toute touche interrompt l'animation d'ouverture, puis est traitée
		// normalement : elle n'est jamais perdue.
		m.stopIntro()
		return m.handleKey(msg)
	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress {
			switch msg.Button {
			case tea.MouseButtonWheelUp:
				m.scroll(wheelLines)
			case tea.MouseButtonWheelDown:
				m.scroll(-wheelLines)
			}
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Le double Échap doit être consécutif : toute autre touche l'annule.
	if !key.Matches(msg, keys.Cancel) {
		m.lastEsc = time.Time{}
	}
	if m.pal != nil {
		return m.handlePaletteKey(msg)
	}
	if m.rw != nil {
		return m.handleRewindKey(msg)
	}
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
		m.turn.stop()
		return m, nil
	case key.Matches(msg, keys.Cancel):
		if m.busy() {
			m.turn.stop()
			m.lastEsc = time.Time{}
			return m, nil
		}
		// Hors tour : un second Échap en moins d'une seconde ouvre /rewind.
		now := m.now()
		if !m.lastEsc.IsZero() && now.Sub(m.lastEsc) < escWindow {
			m.lastEsc = time.Time{}
			m.openRewind()
			return m, nil
		}
		m.lastEsc = now
		return m, nil
	case key.Matches(msg, keys.PageUp):
		m.scroll(m.convHeight())
		return m, nil
	case key.Matches(msg, keys.PageDown):
		m.scroll(-m.convHeight())
		return m, nil
	case key.Matches(msg, keys.LineUp):
		m.scroll(1)
		return m, nil
	case key.Matches(msg, keys.LineDown):
		m.scroll(-1)
		return m, nil
	case m.input.Value() == "" && key.Matches(msg, keys.HalfUp):
		m.scroll(max(1, m.convHeight()/2))
		return m, nil
	case m.input.Value() == "" && key.Matches(msg, keys.HalfDown):
		m.scroll(-max(1, m.convHeight()/2))
		return m, nil
	case m.input.Value() == "" && key.Matches(msg, keys.Top):
		m.scroll(len(m.conversationLines()))
		return m, nil
	case m.input.Value() == "" && key.Matches(msg, keys.Bottom):
		m.scroll(-m.scrollBack)
		return m, nil
	case key.Matches(msg, keys.Submit):
		return m.submit()
	case key.Matches(msg, keys.Complete):
		m.complete()
		return m, nil
	case key.Matches(msg, keys.Palette):
		m.openPalette()
		return m, nil
	case key.Matches(msg, keys.Sidebar):
		// Le fil change de largeur : la position de lecture est bornée.
		m.toggleSidebar()
		m.scroll(0)
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// quit annule le tour en cours éventuel et quitte.
func (m Model) quit() (tea.Model, tea.Cmd) {
	m.turn.stop()
	return m, tea.Quit
}

// submit traite Entrée : commandes texte, ou envoi de la saisie hors tour.
func (m Model) submit() (tea.Model, tea.Cmd) {
	if m.busy() {
		return m, nil
	}
	text := strings.TrimSpace(m.input.Value())
	if text != "" {
		// Premier envoi (message ou commande) : le fil remplace l'accueil.
		m.homeDone = true
	}
	switch text {
	case "":
		return m, nil
	case quitCommand:
		return m.quit()
	case helpCommand:
		m.input.Reset()
		m.tr.notice(i18n.T(m.lang, "tui.help"))
		for _, n := range skills.Builtins {
			m.tr.notice("/" + n + "  " + i18n.T(m.lang, "tui.help.cmd."+n))
		}
		if names := m.skillNames(); len(names) > 0 {
			m.tr.notice(i18n.T(m.lang, "tui.help.skills", "/"+strings.Join(names, ", /")))
		}
		m.scrollBack, m.unseen = 0, false
		return m, nil
	case skillsCommand:
		m.input.Reset()
		m.skillsNotices()
		m.scrollBack, m.unseen = 0, false
		return m, nil
	case agentsCommand:
		m.input.Reset()
		m.agentsNotices()
		m.scrollBack, m.unseen = 0, false
		return m, nil
	case hooksCommand:
		m.input.Reset()
		m.hooksNotices()
		m.scrollBack, m.unseen = 0, false
		return m, nil
	case memoryCommand:
		m.input.Reset()
		m.memoryNotices()
		m.scrollBack, m.unseen = 0, false
		return m, nil
	case rewindCommand:
		m.input.Reset()
		m.openRewind()
		return m, nil
	}
	if text == themeCommand || strings.HasPrefix(text, themeCommand+" ") {
		m.input.Reset()
		m.themeCommand(strings.TrimSpace(strings.TrimPrefix(text, themeCommand)))
		m.scrollBack, m.unseen = 0, false
		return m, nil
	}
	if text == rememberCommand || strings.HasPrefix(text, rememberCommand+" ") {
		m.input.Reset()
		m.remember(strings.TrimPrefix(text, rememberCommand))
		m.scrollBack, m.unseen = 0, false
		return m, nil
	}
	in := engine.Input{Text: text}
	if m.opts.Skills != nil && strings.HasPrefix(text, "/") {
		exp, ok, err := m.opts.Skills.Expand(text)
		switch {
		case errors.Is(err, skills.ErrUnknownCommand):
			// La saisie est conservée pour être corrigée.
			tok, _, _ := strings.Cut(text, " ")
			if f := strings.Fields(text); len(f) > 0 {
				tok = f[0]
			}
			m.tr.notice(i18n.T(m.lang, "skills.unknown", cleanToken(tok)))
			m.scrollBack, m.unseen = 0, false
			return m, nil
		case err != nil:
			m.tr.notice(cleanToken(err.Error()))
			m.scrollBack, m.unseen = 0, false
			return m, nil
		case ok:
			in = engine.Input{Text: exp.Text, Skill: exp.Skill, SkillSHA256: exp.SHA256}
			if exp.Skill != "" {
				in.Typed = text
			}
		}
	}
	m.input.Reset()
	m.scrollBack, m.unseen = 0, false
	ctx, cancel := context.WithCancel(m.ctx)
	done := make(chan struct{})
	m.turn.start(cancel, done)
	m.sent, m.userSeen = text, false
	m.lastCtrlC = time.Time{}
	eng, sid := m.eng, m.opts.SessionID
	return m, func() tea.Msg {
		err := eng.SubmitInput(ctx, sid, in)
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
	m.inputW = -1
	m.syncInput()
	m.input.SetHeight(inputHeight)
}

// bottomLines rend la zone sous la conversation : le panneau de la demande de
// permission affichée, sinon la saisie (conservée telle quelle).
func (m Model) bottomLines() []string {
	if m.rw != nil {
		return m.rewindLines()
	}
	if m.prompting() {
		return m.promptLines()
	}
	return strings.Split(m.input.View(), "\n")
}

// convHeight renvoie le nombre de lignes de la zone de conversation.
func (m Model) convHeight() int {
	bottom := inputHeight
	if m.rw != nil {
		bottom = len(m.rewindLines())
	}
	if m.prompting() {
		bottom = len(m.promptLines())
	}
	return max(minConvHeight, m.height-bottom-statusHeight)
}

// scroll déplace la vue de n lignes vers le haut (n < 0 : vers le bas).
func (m *Model) scroll(n int) {
	limit := max(0, len(m.conversationLines())-m.convHeight())
	m.scrollBack = min(max(0, m.scrollBack+n), limit)
	if m.scrollBack == 0 {
		m.unseen = false
	}
}

// seededTranscript crée la transcription avec le total de jetons déjà compté
// (session reprise) ; la barre d'état l'affiche dès l'ouverture s'il est > 0.
func seededTranscript(l i18n.Lang, tokens int64) *Transcript {
	t := NewTranscript(l)
	t.sessionTokens = tokens
	t.usageSeen = tokens > 0
	return t
}
