package tui

import (
	"slices"
	"strings"
	"unicode"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/tenon-code-eu/tenon/internal/i18n"
	"github.com/tenon-code-eu/tenon/internal/skills"
)

// Genres d'entrée de la palette.
const (
	kindCommand = "command"
	kindSkill   = "skill"
	kindSession = "session"
	kindTheme   = "theme"
	kindInfo    = "info"
)

const (
	paletteMaxWidth = 60
	// paletteRows est le nombre maximal d'entrées visibles.
	paletteRows = 8
	// paletteChrome : bordures, titre et champ de recherche.
	paletteChrome = 4
)

// paletteItem est une entrée de la palette. Action est la commande (« /nom »),
// l'identifiant de session ou le nom du thème selon Kind.
type paletteItem struct{ Kind, Label, Detail, Action string }

// paletteUI est la palette ouverte par Ctrl+P ; nil quand elle est fermée.
type paletteUI struct {
	query  string
	cursor int
	items  []paletteItem
}

// isSep indique une frontière de mot.
func isSep(r rune) bool {
	return unicode.IsSpace(r) || strings.ContainsRune("/-_:.·", r)
}

// fuzzyScore calcule la correspondance de sous-séquence, sans tenir compte de
// la casse, de query dans s. Les lettres contiguës et en début de mot
// rapportent des points ; un début de correspondance tardif en retire.
func fuzzyScore(query, s string) (int, bool) {
	q := []rune(strings.ToLower(query))
	if len(q) == 0 {
		return 0, true
	}
	r := []rune(strings.ToLower(s))
	score, qi, prev, first := 0, 0, -2, -1
	for i, c := range r {
		if qi == len(q) {
			break
		}
		if c != q[qi] {
			continue
		}
		score++
		if prev == i-1 {
			score += 4
		}
		if i == 0 || isSep(r[i-1]) {
			score += 6
		}
		if first < 0 {
			first = i
		}
		prev = i
		qi++
	}
	if qi < len(q) {
		return 0, false
	}
	return score - first, true
}

// rankItems filtre items par query et les trie du meilleur au moins bon ; à
// score égal, l'ordre d'entrée est conservé. Le libellé prime sur le détail.
func rankItems(query string, items []paletteItem) []paletteItem {
	type scored struct {
		it    paletteItem
		score int
	}
	var out []scored
	for _, it := range items {
		if s, ok := fuzzyScore(query, it.Label); ok {
			out = append(out, scored{it, s})
		} else if s, ok := fuzzyScore(query, it.Detail); ok {
			out = append(out, scored{it, s - 100})
		}
	}
	slices.SortStableFunc(out, func(a, b scored) int { return b.score - a.score })
	res := make([]paletteItem, len(out))
	for i, s := range out {
		res[i] = s.it
	}
	return res
}

// paletteItems construit les entrées : commandes intégrées, skills, sessions
// passées du dossier (hors la session courante), thèmes, fournisseur actif.
func (m Model) paletteItems() []paletteItem {
	var items []paletteItem
	for _, n := range skills.Builtins {
		items = append(items, paletteItem{kindCommand, "/" + n, i18n.T(m.lang, "tui.help.cmd."+n), "/" + n})
	}
	for _, n := range m.skillNames() {
		desc := ""
		if s, ok := m.opts.Skills.Get(n); ok {
			desc = m.text(s.Description)
		}
		items = append(items, paletteItem{kindSkill, "/" + m.text(n), desc, "/" + cleanToken(n)})
	}
	for _, s := range m.opts.Sessions {
		if s.ID == m.opts.SessionID {
			continue
		}
		title := m.text(s.FirstPrompt)
		if title == "" {
			title = i18n.T(m.lang, "tui.panel.untitled")
		}
		items = append(items, paletteItem{kindSession, title, m.text(s.ID), cleanToken(s.ID)})
	}
	for _, n := range ThemeNames() {
		items = append(items, paletteItem{kindTheme, i18n.T(m.lang, "tui.palette.theme", n), "/theme " + n, n})
	}
	// Fournisseur effectif : le dernier retenu (bascule), comme la barre d'état.
	p := m.opts.Provider.ID
	if m.tr.selected != nil {
		p = m.tr.selected.ID
	}
	items = append(items, paletteItem{kindInfo, i18n.T(m.lang, "tui.palette.provider", m.text(p)), "", ""})
	return items
}

// openPalette ouvre la palette (sans effet pendant une demande ou /rewind).
func (m *Model) openPalette() {
	if m.rw != nil || m.prompting() {
		return
	}
	m.pal = &paletteUI{items: m.paletteItems()}
}

// handlePaletteKey traite une touche palette ouverte.
func (m Model) handlePaletteKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := m.pal
	list := rankItems(p.query, p.items)
	switch {
	case key.Matches(msg, keys.Cancel), key.Matches(msg, keys.Interrupt), key.Matches(msg, keys.Palette):
		if key.Matches(msg, keys.Palette) {
			// Ctrl+P ouvert : monte, comme dans un sélecteur.
			p.cursor = max(0, p.cursor-1)
			return m, nil
		}
		m.pal = nil
	case msg.Type == tea.KeyUp:
		p.cursor = max(0, p.cursor-1)
	case msg.Type == tea.KeyDown, msg.Type == tea.KeyCtrlN:
		p.cursor = min(max(0, len(list)-1), p.cursor+1)
	case key.Matches(msg, keys.Submit):
		if p.cursor >= len(list) {
			return m, nil
		}
		return m.runPaletteItem(list[p.cursor])
	case msg.Type == tea.KeyBackspace:
		if r := []rune(p.query); len(r) > 0 {
			p.query = string(r[:len(r)-1])
			p.cursor = 0
		}
	case msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace:
		// KeySpace porte déjà son espace dans Runes.
		p.query += cleanToken(string(msg.Runes))
		p.cursor = 0
	}
	return m, nil
}

// runPaletteItem exécute l'entrée choisie.
func (m Model) runPaletteItem(it paletteItem) (tea.Model, tea.Cmd) {
	switch it.Kind {
	case kindCommand, kindSkill:
		m.pal = nil
		// Le brouillon est gardé, après la commande.
		m.input.SetValue(it.Action + " " + m.input.Value())
		m.input.CursorEnd()
	case kindTheme:
		// Comme /theme tapé : refusé pendant un tour (la palette reste ouverte).
		if m.busy() {
			m.tr.notice(i18n.T(m.lang, "tui.palette.busy"))
			m.pal = nil
			return m, nil
		}
		m.pal = nil
		m.homeDone = true
		m.themeCommand(it.Action)
		m.scrollBack, m.unseen = 0, false
	case kindSession:
		m.pal = nil
		m.homeDone = true
		m.tr.notice(i18n.T(m.lang, "tui.palette.resume", it.Action))
		m.scrollBack, m.unseen = 0, false
	}
	return m, nil
}

// paletteWidth est la largeur de la fenêtre : jamais plus que le terminal.
func (m Model) paletteWidth() int {
	return min(paletteMaxWidth, max(1, m.width-4), m.width)
}

// paletteBox rend la fenêtre (bordure comprise), chaque ligne de la largeur
// paletteWidth au plus.
func (m Model) paletteBox() []string {
	p := m.pal
	w := m.paletteWidth()
	inner := max(1, w-2)
	list := rankItems(p.query, p.items)
	rows := max(1, min(paletteRows, m.height-paletteChrome-1))
	fit := func(s string) string { return ansi.Truncate(s, inner, "…") }
	var body []string
	body = append(body, fit(m.st.head.Render(i18n.T(m.lang, "tui.palette.title"))))
	body = append(body, fit("› "+p.query+"▏"))
	lo := max(0, min(p.cursor-rows/2, len(list)-rows))
	for i := lo; i < len(list) && i < lo+rows; i++ {
		it := list[i]
		line := it.Label
		if it.Detail != "" {
			line += "  " + m.st.muted.Render(it.Detail)
		}
		line = fit(m.row(line, i == p.cursor, true))
		if i == p.cursor {
			line = m.st.status.Render(ansi.Strip(line))
		}
		body = append(body, line)
	}
	if len(list) == 0 {
		body = append(body, fit(m.st.notice.Render(i18n.T(m.lang, "tui.palette.none"))))
	}
	st := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Width(inner)
	if !noColor() {
		st = st.BorderForeground(m.st.structure)
	}
	return strings.Split(st.Render(strings.Join(body, "\n")), "\n")
}

// overlayPalette dessine la fenêtre au centre de view.
func (m Model) overlayPalette(view string) string {
	lines := strings.Split(view, "\n")
	box := m.paletteBox()
	w := m.paletteWidth()
	top := max(0, (len(lines)-len(box))/2)
	x := max(0, (m.width-w)/2)
	for i, b := range box {
		j := top + i
		if j >= len(lines) {
			break
		}
		l := lines[j]
		left := ansi.Truncate(l, x, "")
		if pad := x - lipgloss.Width(left); pad > 0 {
			left += strings.Repeat(" ", pad)
		}
		right := ansi.TruncateLeft(l, x+lipgloss.Width(b), "")
		lines[j] = left + "\x1b[0m" + b + "\x1b[0m" + right
	}
	return strings.Join(lines, "\n")
}
