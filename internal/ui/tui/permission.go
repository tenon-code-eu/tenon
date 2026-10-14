package tui

// Ce fichier gère les demandes de permission : file d'attente, réponses au
// clavier et panneau (outil, commande ou chemins, aperçu du diff) affiché à la
// place de la zone de saisie.

import (
	"encoding/json"
	"path/filepath"
	"strings"

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
	p     event.PermissionRequestedPayload
	lines []diff.Line
	more  int
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
}

// endTurn écarte les demandes et appels restants : l'engine les a oubliés.
func (m *Model) endTurn() {
	m.prompts = nil
	m.calls = nil
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
		if lines, more, err := diff.Preview(p.Tool, args, m.opts.ProjectRoot); err == nil {
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
// de réponse de la langue courante, Échap et Ctrl+C agissent.
func (m Model) handlePromptKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
	}
	if msg.Type != tea.KeyRunes || msg.Alt || len(msg.Runes) != 1 {
		return m, nil
	}
	k := answerKeys[m.lang]
	switch msg.Runes[0] {
	case k.once:
		m.answer(engine.Reply{Allow: true})
	case k.session:
		if m.prompts[0].p.Kind == "tool" {
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
	if err := m.eng.Respond(p.p.PermissionID, r); err != nil {
		m.tr.notice(i18n.T(m.lang, "tui.perm.expired", err))
	}
}

// promptLines rend le panneau de la demande affichée ; il tient dans la
// hauteur disponible et les touches restent toujours visibles.
func (m Model) promptLines() []string {
	p := m.prompts[0]
	split := func(s string) []string { return strings.Split(s, "\n") }

	head := split(m.st.perm.Render(m.wrap(i18n.T(m.lang, "tui.perm.title"))))
	keysKey := "tui.perm.keys_tool"
	if p.p.Kind == "tool" {
		text := "⏺ " + p.p.Tool
		if p.p.Command != "" {
			text += " " + p.p.Command
		}
		head = append(head, split(m.st.tool.Render(m.wrap(text)))...)
		for _, path := range p.p.Paths {
			// Un chemin est un seul « mot » : coupé à la largeur, sans repli
			// sur les espaces.
			head = append(head, split(ansi.Hardwrap(sanitize("  "+path), m.width, true))...)
		}
	} else {
		keysKey = "tui.perm.keys_jurisdiction"
		var prov llm.ProviderInfo
		if p.p.Provider != nil {
			prov = *p.p.Provider
		}
		text := i18n.T(m.lang, "tui.perm.jurisdiction", headless.ProviderLabel(prov), prov.Country,
			headless.JurisdictionLabel(m.lang, prov.Jurisdiction))
		head = append(head, split(m.wrap(text))...)
	}

	var body []string
	if len(p.lines) == 1 && p.lines[0].Kind == diff.Same && p.lines[0].Text == diff.BinaryText {
		body = append(body, m.st.notice.Render(m.truncate(i18n.T(m.lang, "tui.perm.binary"))))
	} else {
		for _, l := range p.lines {
			switch l.Kind {
			case diff.Add:
				body = append(body, m.st.add.Render(m.truncate("+ "+l.Text)))
			case diff.Del:
				body = append(body, m.st.del.Render(m.truncate("- "+l.Text)))
			default:
				body = append(body, m.truncate("  "+l.Text))
			}
		}
	}
	keyLines := split(m.st.perm.Render(m.wrap(i18n.T(m.lang, keysKey))))

	avail := max(1, m.height-statusHeight-minConvHeight)
	hidden := p.more
	moreLine := func() []string {
		if hidden == 0 {
			return nil
		}
		return []string{m.st.notice.Render(m.truncate(i18n.T(m.lang, "tui.perm.more_lines", hidden)))}
	}
	if len(head)+len(body)+len(moreLine())+len(keyLines) > avail {
		// Le diff est raccourci en premier ; la ligne « … » compte pour une.
		room := avail - len(head) - len(keyLines) - 1
		if room < 0 {
			room = 0
		}
		if room < len(body) {
			hidden += len(body) - room
			body = body[:room]
		}
	}
	out := append(append(head, body...), moreLine()...)
	out = append(out, keyLines...)
	if len(out) > avail {
		// L'en-tête cède ensuite : les touches de réponse restent visibles.
		out = out[len(out)-avail:]
	}
	return out
}

// truncate nettoie s et le coupe à la largeur (lignes de diff non repliées).
func (m Model) truncate(s string) string {
	return ansi.Truncate(sanitize(s), m.width, "…")
}
