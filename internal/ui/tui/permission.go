package tui

// Ce fichier gère les demandes de permission : file d'attente, réponses au
// clavier et panneau (outil, commande ou chemins, aperçu du diff) affiché à la
// place de la zone de saisie.

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"codeberg.org/tenon/tenon/internal/engine"
	"codeberg.org/tenon/tenon/internal/event"
	"codeberg.org/tenon/tenon/internal/i18n"
	"codeberg.org/tenon/tenon/internal/llm"
	"codeberg.org/tenon/tenon/internal/ui/headless"
	"codeberg.org/tenon/tenon/internal/ui/tui/diff"
)

// permPrompt est une demande de permission en attente de réponse. L'aperçu
// du diff est calculé une seule fois, à la création.
type permPrompt struct {
	p      event.PermissionRequestedPayload
	lines  []diff.Line
	more   int
	binary bool
}

// pendingCall est un appel d'outil demandé dont le résultat n'est pas arrivé.
type pendingCall struct {
	id, tool string
	args     json.RawMessage
}

// answerKeys associe aux réponses « oui une fois » et « oui pour la session »
// leur touche dans chaque langue ; « non » est n dans les deux langues.
var answerKeys = map[i18n.Lang]struct{ once, session rune }{
	i18n.FR: {once: 'o', session: 's'},
	i18n.EN: {once: 'y', session: 'a'},
}

const noKey = 'n'

// approveDelay est le délai, après l'affichage d'une demande, pendant lequel
// les touches d'acceptation sont ignorées : une demande arrivée pendant la
// frappe ou juste après la réponse à la précédente n'est pas acceptée par une
// touche destinée à autre chose. Les refus agissent immédiatement.
const approveDelay = 500 * time.Millisecond

// minApproveWidth est la largeur minimale pour accepter une demande : en deçà,
// la première ligne de l'outil ne montre pas assez de la commande ou du chemin.
const minApproveWidth = 40

// syncShown note l'heure d'affichage chaque fois que la demande affichée change.
func (m *Model) syncShown() {
	if len(m.prompts) == 0 {
		m.shown = nil
		return
	}
	if m.prompts[0] != m.shown {
		m.shown = m.prompts[0]
		m.shownAt = m.now()
	}
}

// prompting indique qu'une demande de permission est affichée.
func (m Model) prompting() bool { return len(m.prompts) > 0 }

// trackEvent tient à jour les appels en attente et la file des demandes.
func (m *Model) trackEvent(ev event.Event) {
	switch ev.Type {
	case event.ToolCallRequested:
		var p event.ToolCallRequestedPayload
		if json.Unmarshal(ev.Payload, &p) == nil {
			m.calls = append(m.calls, pendingCall{id: p.CallID, tool: p.Tool, args: p.Args})
		}
	case event.ToolResult:
		var p event.ToolResultPayload
		if json.Unmarshal(ev.Payload, &p) == nil {
			m.dropCall(p.CallID)
		}
	case event.PermissionRequested:
		var p event.PermissionRequestedPayload
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			m.tr.decodeFailed(ev)
			return
		}
		m.prompts = append(m.prompts, m.newPrompt(p))
	case event.PermissionResolved:
		var p event.PermissionResolvedPayload
		if json.Unmarshal(ev.Payload, &p) == nil {
			m.dropPrompt(p.PermissionID)
		}
	case event.TurnCompleted:
		m.endTurn()
	}
	m.syncShown()
}

// endTurn écarte les demandes et appels restants : l'engine les a oubliés.
func (m *Model) endTurn() {
	m.prompts = nil
	m.calls = nil
	m.syncShown()
}

func (m *Model) dropCall(id string) {
	for i, c := range m.calls {
		if c.id == id {
			m.calls = append(m.calls[:i:i], m.calls[i+1:]...)
			return
		}
	}
}

func (m *Model) dropPrompt(id string) {
	for i, p := range m.prompts {
		if p.p.PermissionID == id {
			m.prompts = append(m.prompts[:i:i], m.prompts[i+1:]...)
			return
		}
	}
}

// newPrompt crée la demande et, pour un outil d'écriture, calcule l'aperçu.
func (m Model) newPrompt(p event.PermissionRequestedPayload) *permPrompt {
	pr := &permPrompt{p: p}
	if p.Kind != "tool" {
		return pr
	}
	if args := m.matchArgs(p); args != nil {
		lines, more, err := diff.Preview(p.Tool, args, m.opts.ProjectRoot)
		switch {
		case errors.Is(err, diff.ErrBinary):
			pr.binary = true
		case err == nil:
			pr.lines, pr.more = lines, more
		}
	}
	return pr
}

// matchArgs renvoie les arguments de l'appel en attente le plus récent dont
// l'outil et la cible (commande, chemin) correspondent à la demande : le
// payload de la demande ne porte pas d'identifiant d'appel.
func (m Model) matchArgs(p event.PermissionRequestedPayload) json.RawMessage {
	for i := len(m.calls) - 1; i >= 0; i-- {
		c := m.calls[i]
		if c.tool == p.Tool && m.targetMatches(c.args, p) {
			return c.args
		}
	}
	return nil
}

func (m Model) targetMatches(args json.RawMessage, p event.PermissionRequestedPayload) bool {
	var a struct {
		Command string `json:"command"`
		Path    string `json:"path"`
	}
	if json.Unmarshal(args, &a) != nil || a.Command != p.Command {
		return false
	}
	if a.Path == "" {
		return len(p.Paths) == 0
	}
	path := a.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(m.opts.ProjectRoot, path)
	}
	return len(p.Paths) == 1 && filepath.Clean(p.Paths[0]) == filepath.Clean(path)
}

// handlePromptKey traite une touche pendant une demande : seules les touches
// de réponse de la langue courante, Échap et Ctrl+C agissent ; PgPréc/PgSuiv
// font défiler la conversation. Un collage n'est jamais une réponse.
func (m Model) handlePromptKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Paste {
		return m, nil
	}
	switch {
	case key.Matches(msg, keys.Interrupt):
		// Refus d'abord, puis le comportement habituel de Ctrl+C (annulation
		// du tour, ou sortie).
		for len(m.prompts) > 0 {
			m.answer(engine.Reply{Allow: false})
		}
		return m.handleKey(msg)
	case key.Matches(msg, keys.Cancel):
		m.answer(engine.Reply{Allow: false})
		return m, nil
	case key.Matches(msg, keys.PageUp):
		m.scroll(m.convHeight())
		return m, nil
	case key.Matches(msg, keys.PageDown):
		m.scroll(-m.convHeight())
		return m, nil
	}
	if msg.Type != tea.KeyRunes || msg.Alt || len(msg.Runes) != 1 {
		return m, nil
	}
	k := answerKeys[m.lang]
	settled := m.now().Sub(m.shownAt) >= approveDelay
	switch msg.Runes[0] {
	case k.once:
		if settled && m.promptHeadVisible() {
			m.answer(engine.Reply{Allow: true})
		}
	case k.session:
		if settled && m.promptHeadVisible() && m.prompts[0].p.Kind == "tool" {
			m.answer(engine.Reply{Allow: true, Remember: true})
		}
	case noKey:
		m.answer(engine.Reply{Allow: false})
	}
	return m, nil
}

// answer répond à la demande affichée et passe à la suivante. Une réponse
// refusée par l'engine (tour annulé entre-temps) écarte la demande avec un avis.
func (m *Model) answer(r engine.Reply) {
	p := m.prompts[0]
	m.prompts = m.prompts[1:]
	m.syncShown()
	if err := m.eng.Respond(p.p.PermissionID, r); err != nil {
		m.tr.notice(i18n.T(m.lang, "tui.perm.expired", err))
	}
}

// promptHeadVisible indique que l'image rendue pour la taille courante montre
// l'en-tête de la demande (titre et première ligne de l'outil, de la commande
// ou du chemin) : le panneau complet tient sous la conversation et la largeur
// suffit. Sinon les touches d'acceptation sont ignorées ; les refus agissent.
// View et le traitement des touches s'appuient tous deux sur ce calcul.
func (m Model) promptHeadVisible() bool {
	return m.width >= minApproveWidth && len(m.fullPromptLines())+statusHeight <= m.height
}

// promptLines rend le panneau : complet si son en-tête est visible, sinon
// l'indication d'agrandir le terminal suivie des touches (refus possible).
func (m Model) promptLines() []string {
	if m.promptHeadVisible() {
		return m.fullPromptLines()
	}
	split := func(s string) []string { return strings.Split(s, "\n") }
	out := split(m.st.perm.Render(m.wrap(i18n.T(m.lang, "tui.perm.too_small"))))
	return append(out, split(m.st.perm.Render(m.wrap(i18n.T(m.lang, m.promptKeysKey()))))...)
}

func (m Model) promptKeysKey() string {
	if m.prompts[0].p.Kind == "tool" {
		return "tui.perm.keys_tool"
	}
	return "tui.perm.keys_jurisdiction"
}

// fullPromptLines rend le panneau de la demande affichée dans la hauteur
// disponible. Le titre, la première ligne de l'outil (outil et début de la
// commande) et les touches restent toujours visibles : un texte trop long est
// coupé au milieu (fin de la commande, chemins, diff) et remplacé par une ligne
// indiquant le nombre de lignes masquées.
func (m Model) fullPromptLines() []string {
	p := m.prompts[0]
	split := func(s string) []string { return strings.Split(s, "\n") }

	top := split(m.st.perm.Render(m.wrap(i18n.T(m.lang, "tui.perm.title"))))
	var middle []string
	if p.p.Kind == "tool" {
		text := "⏺ " + p.p.Tool
		if p.p.Command != "" {
			text += " " + p.p.Command
		}
		middle = append(middle, split(m.st.tool.Render(m.wrap(text)))...)
		for _, path := range p.p.Paths {
			// Un chemin est un seul « mot » : coupé à la largeur, sans repli
			// sur les espaces.
			middle = append(middle, split(ansi.Hardwrap(sanitize("  "+path), m.width, true))...)
		}
	} else {
		var prov llm.ProviderInfo
		if p.p.Provider != nil {
			prov = *p.p.Provider
		}
		text := i18n.T(m.lang, "tui.perm.jurisdiction", headless.ProviderLabel(prov), prov.Country,
			headless.JurisdictionLabel(m.lang, prov.Jurisdiction))
		middle = append(middle, split(m.wrap(text))...)
	}
	if p.binary {
		middle = append(middle, m.st.notice.Render(m.truncate(i18n.T(m.lang, "tui.perm.binary"))))
	}
	for _, l := range p.lines {
		switch l.Kind {
		case diff.Add:
			middle = append(middle, m.st.add.Render(m.truncate("+ "+l.Text)))
		case diff.Del:
			middle = append(middle, m.st.del.Render(m.truncate("- "+l.Text)))
		default:
			middle = append(middle, m.truncate("  "+l.Text))
		}
	}
	keyLines := split(m.st.perm.Render(m.wrap(i18n.T(m.lang, m.promptKeysKey()))))
	moreLine := func(n int) string {
		return m.st.notice.Render(m.truncate(i18n.T(m.lang, "tui.perm.more_lines", n)))
	}

	avail := max(1, m.height-statusHeight-minConvHeight)
	out := append([]string(nil), top...)
	extra := 0
	if p.more > 0 {
		extra = 1
	}
	if len(top)+len(middle)+extra+len(keyLines) <= avail {
		out = append(out, middle...)
		if p.more > 0 {
			out = append(out, moreLine(p.more))
		}
		return append(out, keyLines...)
	}
	// Une ligne pour l'indication des lignes masquées.
	budget := avail - len(top) - len(keyLines) - 1
	showMore := p.more > 0 && budget >= 2
	if showMore {
		budget--
	}
	budget = min(max(1, budget), len(middle))
	hidden := len(middle) - budget
	if p.more > 0 && !showMore {
		hidden += p.more
	}
	out = append(out, middle[:budget]...)
	if hidden > 0 {
		out = append(out, m.st.notice.Render(m.truncate(i18n.T(m.lang, "tui.perm.hidden_lines", hidden))))
	}
	if showMore {
		out = append(out, moreLine(p.more))
	}
	return append(out, keyLines...)
}

// truncate nettoie s et le coupe à la largeur (lignes de diff non repliées).
func (m Model) truncate(s string) string {
	return ansi.Truncate(sanitize(s), m.width, "…")
}
