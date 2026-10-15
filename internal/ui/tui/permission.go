package tui

// Ce fichier gère les demandes de permission : file d'attente, réponses au
// clavier et panneau (outil, commande ou chemins, aperçu du diff) affiché à la
// place de la zone de saisie.

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

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
// du diff est calculé une seule fois, hors de Update (lecture du fichier
// courant) ; tant qu'il n'est pas prêt, seul le refus agit.
type permPrompt struct {
	p event.PermissionRequestedPayload
	// tool, command et paths sont le nom de l'outil (venu du modèle), la
	// commande et les chemins masqués, affichés ; p garde les valeurs brutes
	// pour la logique (outil shell, aperçu, correspondance de l'appel).
	tool    string
	command string
	paths   []string
	ready   bool
	lines   []diff.Line
	more    int
	binary  bool
	// noChange : l'écriture ne change rien ; unavailable : pas d'aperçu
	// (appel non retrouvé, arguments illisibles).
	noChange    bool
	unavailable bool
}

// previewMsg porte l'aperçu calculé pour la demande pr.
type previewMsg struct {
	pr          *permPrompt
	lines       []diff.Line
	more        int
	binary      bool
	noChange    bool
	unavailable bool
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

// approveDelay est la période de calme exigée avant une acceptation : les
// touches d'acceptation n'agissent que si ce délai s'est écoulé depuis
// l'affichage de la demande ET depuis la touche précédente, quelle qu'elle
// soit (ignorée comprise). Une demande arrivée pendant la frappe n'est donc
// jamais acceptée par une touche destinée à autre chose, même si la frappe
// continue. Les refus agissent immédiatement.
const approveDelay = 500 * time.Millisecond

// minApproveWidth est la largeur minimale pour accepter une demande : en deçà,
// la première ligne de l'outil ne montre pas assez de la commande ou du chemin.
const minApproveWidth = 40

// syncShown réarme le délai chaque fois que la demande affichée change.
func (m *Model) syncShown() {
	if len(m.prompts) == 0 {
		m.shown = nil
		return
	}
	if m.prompts[0] != m.shown {
		m.shown = m.prompts[0]
		m.armedAt = m.now()
	}
}

// rearmIf réarme le délai si la demande affichée vient de devenir acceptable
// (par exemple après un agrandissement du terminal) : before est l'état
// d'avant le changement.
func (m *Model) rearmIf(before bool) {
	if m.prompting() && !before && m.approvable() {
		m.armedAt = m.now()
	}
}

// prompting indique qu'une demande de permission est affichée.
func (m Model) prompting() bool { return len(m.prompts) > 0 }

// trackEvent tient à jour les appels en attente et la file des demandes ;
// la commande renvoyée calcule l'aperçu d'une nouvelle demande.
func (m *Model) trackEvent(ev event.Event) tea.Cmd {
	var cmd tea.Cmd
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
			return nil
		}
		var pr *permPrompt
		pr, cmd = m.newPrompt(p)
		m.prompts = append(m.prompts, pr)
	case event.PermissionResolved:
		var p event.PermissionResolvedPayload
		if json.Unmarshal(ev.Payload, &p) == nil {
			m.dropPrompt(p.PermissionID)
		}
	case event.TurnCompleted:
		m.endTurn()
	}
	m.syncShown()
	return cmd
}

// applyPreview applique un aperçu calculé, si sa demande est encore en
// attente ; le délai d'acceptation est réarmé si elle est affichée, son
// contenu venant de changer.
func (m *Model) applyPreview(msg previewMsg) {
	for _, p := range m.prompts {
		if p != msg.pr {
			continue
		}
		p.lines, p.more, p.binary, p.ready = msg.lines, msg.more, msg.binary, true
		p.noChange, p.unavailable = msg.noChange, msg.unavailable
		if p == m.prompts[0] {
			m.armedAt = m.now()
		}
		return
	}
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

// newPrompt crée la demande et, pour un outil d'écriture dont l'appel est
// connu, la commande qui calcule l'aperçu hors de Update : la lecture du
// fichier courant peut bloquer (montage figé).
func (m Model) newPrompt(p event.PermissionRequestedPayload) (*permPrompt, tea.Cmd) {
	pr := &permPrompt{p: p, ready: true, tool: m.redact(p.Tool), command: m.redact(p.Command)}
	for _, path := range p.Paths {
		pr.paths = append(pr.paths, m.redact(path))
	}
	if p.Kind != "tool" || (p.Tool != "write_file" && p.Tool != "edit_file") {
		return pr, nil
	}
	args := m.matchArgs(p)
	if args == nil {
		pr.unavailable = true
		return pr, nil
	}
	pr.ready = false
	tool, root, redact := p.Tool, m.opts.ProjectRoot, m.redact
	return pr, func() tea.Msg {
		msg := previewMsg{pr: pr}
		lines, more, err := diff.PreviewRedacted(tool, args, root, redact)
		switch {
		case errors.Is(err, diff.ErrBinary):
			msg.binary = true
		case err != nil:
			msg.unavailable = true
		case len(lines) == 0:
			msg.noChange = true
		default:
			msg.lines, msg.more = lines, more
		}
		return msg
	}
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
// font défiler la conversation. Un collage n'est jamais une réponse. Toute
// touche, même ignorée, réarme le délai d'acceptation.
func (m Model) handlePromptKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	now := m.now()
	settled := now.Sub(m.armedAt) >= approveDelay
	m.armedAt = now
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
	switch msg.Runes[0] {
	case k.once:
		if settled && m.approvable() {
			m.answer(engine.Reply{Allow: true})
		}
	case k.session:
		if settled && m.approvable() && m.prompts[0].p.Kind == "tool" {
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

// approvable indique que la demande affichée peut être acceptée : la largeur
// suffit et l'image rendue pour la taille courante montre la commande
// entière, tous les chemins (ou le texte entier de la juridiction), sans
// regroupement de lignes vides, et l'aperçu du diff est calculé. Seules les
// lignes du diff peuvent être coupées. Sinon les touches d'acceptation sont ignorées ; les refus
// agissent. View et le traitement des touches s'appuient tous deux sur ce
// calcul.
func (m Model) approvable() bool {
	return m.prompting() && m.promptPanel().approvable
}

// promptLines rend le panneau de la demande affichée.
func (m Model) promptLines() []string { return m.promptPanel().lines }

// panel est le panneau rendu pour la taille courante.
type panel struct {
	lines      []string
	approvable bool
}

func (m Model) promptKeysKey() string {
	if m.prompting() && m.prompts[0].p.Kind == "tool" {
		return "tui.perm.keys_tool"
	}
	return "tui.perm.keys_jurisdiction"
}

func splitLines(s string) []string { return strings.Split(s, "\n") }

// promptPanel rend le panneau de la demande affichée dans la hauteur
// disponible. Complet (acceptable) si le titre, l'en-tête entier (commande,
// chemins) et les touches tiennent : seules les lignes du diff sont alors
// coupées, avec une ligne indiquant le nombre de lignes masquées. Sinon le
// panneau ne permet que le refus : l'en-tête est montré avec ses lignes vides
// regroupées (la fin de la commande reste visible), coupé au besoin, suivi
// d'une indication ; si même cela ne tient pas, seuls l'indication
// d'agrandir le terminal et les touches sont affichés.
func (m Model) promptPanel() panel {
	if !m.prompting() {
		return panel{}
	}
	p := m.prompts[0]
	top := splitLines(m.st.perm.Render(m.wrap(i18n.T(m.lang, "tui.perm.title"))))
	keyLines := splitLines(m.st.perm.Render(m.wrap(i18n.T(m.lang, m.promptKeysKey()))))
	avail := max(1, m.height-statusHeight-minConvHeight)

	if m.width >= minApproveWidth {
		if out, ok := m.fullPanel(p, top, keyLines, avail); ok {
			return panel{lines: out, approvable: p.ready}
		}
		hintKey := "tui.perm.too_small"
		if p.command != "" {
			hintKey = "tui.perm.command_too_long"
		}
		hint := splitLines(m.st.notice.Render(m.wrap(i18n.T(m.lang, hintKey))))
		head := m.headLines(p, true)
		fixed := len(top) + len(hint) + len(keyLines)
		out := append([]string(nil), top...)
		switch {
		case fixed+len(head) <= avail:
			out = append(out, head...)
			out = append(out, hint...)
			return panel{lines: append(out, keyLines...)}
		case fixed+2 <= avail:
			budget := avail - fixed - 1
			out = append(out, head[:budget]...)
			out = append(out, m.hiddenLine(len(head)-budget))
			out = append(out, hint...)
			return panel{lines: append(out, keyLines...)}
		}
	}
	out := splitLines(m.st.perm.Render(m.wrap(i18n.T(m.lang, "tui.perm.too_small"))))
	return panel{lines: append(out, keyLines...)}
}

// fullPanel rend le panneau complet si le titre, l'en-tête entier et les
// touches tiennent dans avail lignes, avec au moins une ligne pour le diff
// (ou l'indication de ses lignes masquées) s'il y en a un.
func (m Model) fullPanel(p *permPrompt, top, keyLines []string, avail int) ([]string, bool) {
	head := m.headLines(p, false)
	body := m.bodyLines(p)
	fixed := len(top) + len(head) + len(keyLines)
	if fixed > avail || !within(m.width, top, head) {
		return nil, false
	}
	rest := avail - fixed
	moreLine := func(n int) string {
		return m.st.notice.Render(m.truncate(i18n.T(m.lang, "tui.perm.more_lines", n)))
	}
	out := append(append([]string(nil), top...), head...)
	extra := 0
	if p.more > 0 {
		extra = 1
	}
	switch {
	case len(body)+extra <= rest:
		out = append(out, body...)
		if p.more > 0 {
			out = append(out, moreLine(p.more))
		}
	case rest < 1:
		// Pas même la place d'indiquer que le diff est masqué.
		return nil, false
	default:
		// Une ligne pour l'indication des lignes masquées.
		budget := rest - 1
		showMore := p.more > 0 && budget >= 2
		if showMore {
			budget--
		}
		budget = min(budget, len(body))
		hidden := len(body) - budget
		if p.more > 0 && !showMore {
			hidden += p.more
		}
		out = append(out, body[:budget]...)
		out = append(out, m.hiddenLine(hidden))
		if showMore {
			out = append(out, moreLine(p.more))
		}
	}
	return append(out, keyLines...), true
}

// within indique qu'aucune ligne des groupes ne dépasse la largeur w : une
// ligne plus large serait tronquée par View et un caractère de la commande ou
// d'un chemin serait masqué.
func within(w int, groups ...[]string) bool {
	for _, g := range groups {
		for _, l := range g {
			if ansi.StringWidth(l) > w {
				return false
			}
		}
	}
	return true
}

func (m Model) hiddenLine(n int) string {
	return m.st.notice.Render(m.truncate(i18n.T(m.lang, "tui.perm.hidden_lines", n)))
}

// minBlankRun est la longueur à partir de laquelle une suite de lignes vides
// de la commande est regroupée en une seule ligne « ⏎ × N ».
const minBlankRun = 3

// contMarker commence chaque suite d'une ligne de l'en-tête coupée à la
// largeur : une coupure se distingue d'un vrai saut de ligne et d'une espace.
const contMarker = "↪ "

// markerEscape remplace, dans le texte de l'en-tête (outil, commande,
// chemins), le caractère du marqueur : une ligne de la commande commençant
// par « ↪ » (un vrai saut de ligne, séparateur pour bash) se ferait passer
// pour la suite d'une ligne coupée. Le seul « ↪ » affiché est le marqueur.
const markerEscape = "[U+21AA]"

func escapeMarker(s string) string { return strings.ReplaceAll(s, "↪", markerEscape) }

// headLines rend l'en-tête de la demande : nombre de lignes de la commande
// s'il y en a plusieurs, outil et commande, chemins ; ou le texte de la
// juridiction. collapse regroupe les suites de plus de deux lignes vides de
// la commande.
func (m Model) headLines(p *permPrompt, collapse bool) []string {
	if p.p.Kind != "tool" {
		var prov llm.ProviderInfo
		if p.p.Provider != nil {
			prov = *p.p.Provider
		}
		// Chaque champ est masqué seul : une règle ancrée en fin de ligne ne
		// reconnaîtrait pas un secret suivi du reste du texte.
		text := i18n.T(m.lang, "tui.perm.jurisdiction", m.redact(headless.ProviderLabel(prov)), m.redact(prov.Country),
			m.redact(headless.JurisdictionLabel(m.lang, prov.Jurisdiction)))
		return splitLines(m.wrap(text))
	}
	var out []string
	cmd := sanitize(p.command)
	if n := len(splitLines(cmd)); cmd != "" && n > 1 {
		out = append(out, m.st.notice.Render(m.truncate(i18n.T(m.lang, "tui.perm.command_lines", n))))
	}
	if collapse {
		cmd = collapseBlank(cmd)
	}
	text := "⏺ " + sanitize(p.tool)
	if cmd != "" {
		text += " " + cmd
	}
	for _, l := range splitLines(escapeMarker(text)) {
		for _, piece := range breakLine(l, m.width) {
			out = append(out, m.st.tool.Render(piece))
		}
	}
	if p.p.Tool == "shell" {
		// Risque rappelé par la spécification : pas de bac à sable.
		out = append(out, splitLines(m.st.notice.Render(m.wrap(i18n.T(m.lang, "tui.perm.not_sandbox"))))...)
	}
	for _, path := range p.paths {
		for _, l := range splitLines(escapeMarker(sanitize("  " + path))) {
			out = append(out, breakLine(l, m.width)...)
		}
	}
	return out
}

// breakLine coupe une ligne de l'en-tête (commande, chemin) à la largeur, sans
// repli sur les espaces ni perte d'aucun caractère, blancs compris : chaque
// suite commence par contMarker, compté dans la largeur. Un caractère plus
// large que la place restante est placé seul ; la ligne dépasse alors la
// largeur et la demande n'est pas acceptable (within).
func breakLine(l string, width int) []string {
	var out []string
	prefix := ""
	for {
		piece := ansi.Truncate(l, width-ansi.StringWidth(prefix), "")
		if piece == "" || !strings.HasPrefix(l, piece) {
			_, n := utf8.DecodeRuneInString(l)
			piece = l[:n]
		}
		out = append(out, prefix+piece)
		l = l[len(piece):]
		if l == "" {
			return out
		}
		prefix = contMarker
	}
}

// collapseBlank remplace chaque suite d'au moins minBlankRun lignes vides
// (ou faites d'espaces) par une ligne « ⏎ × N ».
func collapseBlank(s string) string {
	lines := splitLines(s)
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); {
		j := i
		for j < len(lines) && strings.TrimSpace(lines[j]) == "" {
			j++
		}
		switch n := j - i; {
		case n >= minBlankRun:
			out = append(out, fmt.Sprintf("⏎ × %d", n))
			i = j
		case n > 0:
			out = append(out, lines[i:j]...)
			i = j
		default:
			out = append(out, lines[i])
			i++
		}
	}
	return strings.Join(out, "\n")
}

// bodyLines rend le corps de la demande : marqueur binaire et lignes du diff.
func (m Model) bodyLines(p *permPrompt) []string {
	var out []string
	if !p.ready {
		out = append(out, m.st.notice.Render(m.truncate(i18n.T(m.lang, "tui.perm.preview_loading"))))
	}
	for _, k := range []struct {
		on  bool
		key string
	}{{p.binary, "tui.perm.binary"}, {p.noChange, "tui.perm.no_change"}, {p.unavailable, "tui.perm.preview_unavailable"}} {
		if k.on {
			out = append(out, m.st.notice.Render(m.truncate(i18n.T(m.lang, k.key))))
		}
	}
	for _, l := range p.lines {
		switch l.Kind {
		case diff.Add:
			out = append(out, m.st.add.Render(m.truncate("+ "+l.Text)))
		case diff.Del:
			out = append(out, m.st.del.Render(m.truncate("- "+l.Text)))
		default:
			out = append(out, m.truncate("  "+l.Text))
		}
	}
	return out
}

// truncate nettoie s et le coupe à la largeur (lignes de diff non repliées).
func (m Model) truncate(s string) string {
	return ansi.Truncate(sanitize(s), m.width, "…")
}
