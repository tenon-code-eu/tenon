package tui

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/tenon-code-eu/tenon/internal/i18n"
	"github.com/tenon-code-eu/tenon/internal/llm"
)

const tabSpaces = "    "

// styles regroupe les styles lipgloss de la TUI.
type styles struct {
	banner, user, assistant, tool, notice, err, busy, status lipgloss.Style
	eu, local, nonEU                                         lipgloss.Style
	// muted, alert et ok : texte atténué, ambre et vert ; head : titres du
	// panneau latéral.
	muted, alert, ok, head lipgloss.Style
	perm, add, del         lipgloss.Style
	md                     mdStyles
	// structure est la couleur de texte de Structure (adaptée au fond).
	structure lipgloss.Color
	// r est le renderer qui a produit ces styles (accueil).
	r *lipgloss.Renderer
}

// newStyles construit les styles du thème th ; NO_COLOR retire toute couleur
// (gras, italique et inversion restent).
func newStyles(r *lipgloss.Renderer, th Theme) styles {
	fg := func(c lipgloss.Color) lipgloss.Style {
		s := r.NewStyle()
		if noColor() || c == "" {
			return s
		}
		return s.Foreground(c)
	}
	// Structure en texte : variante claire sur fond sombre.
	structure := th.StructureFg(r.HasDarkBackground())
	local := r.NewStyle().Reverse(true)
	if !noColor() {
		// Structure en fond, avec un texte explicite : jamais le fond du terminal.
		local = r.NewStyle().Background(th.Structure).Foreground(onFill(th.Structure))
	}
	return styles{
		r:         r,
		structure: structure,
		banner:    fg(th.Accent).Bold(true),
		user:      r.NewStyle().Bold(true),
		assistant: fg(th.Text),
		tool:      fg(th.Accent),
		notice:    fg(th.Muted).Italic(true),
		err:       fg(th.Error),
		busy:      r.NewStyle().Faint(true),
		status:    r.NewStyle().Reverse(true),
		eu:        fg(th.OK).Reverse(true),
		local:     local,
		nonEU:     fg(th.Alert).Reverse(true),
		muted:     fg(th.Muted),
		alert:     fg(th.Alert),
		ok:        fg(th.OK),
		head:      fg(structure).Bold(true),
		perm:      fg(th.Accent).Bold(true),
		add:       fg(th.OK),
		del:       fg(th.Error),
		md: mdStyles{
			bold:    r.NewStyle().Bold(true),
			code:    fg(th.Accent),
			heading: r.NewStyle().Bold(true).Underline(true),
		},
	}
}

// View rend la conversation, la saisie et la barre d'état ; aucune ligne ne
// dépasse la largeur courante.
func (m Model) View() string {
	v := m.baseView()
	if m.pal != nil {
		v = m.overlayPalette(v)
	}
	return v
}

// baseView rend l'écran sans la palette.
func (m Model) baseView() string {
	if m.homeShown() {
		return m.homeView()
	}
	conv := m.conversationLines()
	h := m.convHeight()
	end := max(0, len(conv)-m.scrollBack)
	start := max(0, end-h)
	visible := conv[start:end]
	lines := make([]string, 0, m.height+h)
	for i := len(visible); i < h; i++ {
		lines = append(lines, "")
	}
	lines = append(lines, visible...)
	// Le panneau latéral longe la zone du fil, à sa droite.
	if m.sidebarShown() {
		lines = m.withSidebar(lines)
	}
	lines = append(lines, m.bottomLines()...)
	lines = append(lines, m.statusLine())
	if len(lines) > m.height {
		lines = lines[len(lines)-m.height:]
	}
	for i, l := range lines {
		if lipgloss.Width(l) > m.width {
			lines[i] = ansi.Truncate(l, m.width, "")
		}
	}
	return strings.Join(lines, "\n")
}

// conversationLines rend les éléments de la transcription, repliés à la
// largeur du fil (sans le panneau latéral).
func (m Model) conversationLines() []string {
	// Copie locale : tout le rendu du fil (wrap, badges) suit sa largeur.
	m.width = m.convWidth()
	var out []string
	prev := ItemKind(-1)
	for _, it := range m.tr.Items() {
		// Un texte vide (fragment de l'assistant entre deux appels d'outils)
		// n'occupe aucune ligne.
		if it.Kind != ItemTool && strings.TrimSpace(it.Text) == "" {
			continue
		}
		// Les appels d'outils consécutifs se suivent sans ligne vide.
		if len(out) > 0 && !(it.Kind == ItemTool && prev == ItemTool) {
			out = append(out, "")
		}
		out = append(out, strings.Split(m.renderItem(it), "\n")...)
		prev = it.Kind
	}
	if m.busy() {
		out = append(out, "", m.st.busy.Render(m.wrap(i18n.T(m.lang, "tui.busy"))))
	}
	return out
}

func (m Model) renderItem(it Item) string {
	switch it.Kind {
	case ItemBanner:
		return m.st.banner.Render(m.wrap(it.Text))
	case ItemUser:
		return m.st.user.Render(m.wrap("› " + it.Text))
	case ItemTool:
		text := "⏺ " + it.Tool
		if it.Parent != "" {
			text = "  ⎿ " + it.Tool
		}
		if it.Detail != "" {
			text += " " + it.Detail
		}
		if it.Status != "" {
			text += " [" + i18n.T(m.lang, "tui.tool."+it.Status) + "]"
		}
		if it.sub && it.Status != "pending" {
			text += " · " + i18n.T(m.lang, "tui.agent.iterations", it.Iterations)
		}
		return m.st.tool.Render(m.wrap(text))
	case ItemNotice:
		return m.st.notice.Render(m.wrap(it.Text))
	case ItemError:
		return m.st.err.Render(m.wrap(it.Text))
	}
	text := strings.Trim(sanitize(it.Text), "\n")
	out := m.st.assistant.Render(m.wrapStyled(renderMarkdown(text, m.st.md)))
	if it.badge != nil {
		// Fournisseur du dernier appel du tour, sous la réponse finale.
		out += "\n" + m.badgeLine(*it.badge)
	}
	return out
}

// wrap nettoie s des caractères de contrôle puis le replie à la largeur. Le
// repli sur les espaces peut laisser une ligne d'une colonne trop large (ligne
// commençant par une espace suivie d'un mot de la largeur) : elle est coupée
// à la largeur exacte, sans perdre de caractère, plutôt que tronquée par View.
func (m Model) wrap(s string) string {
	return m.wrapStyled(sanitize(s))
}

// wrapStyled replie s, déjà nettoyé et éventuellement stylé, à la largeur.
func (m Model) wrapStyled(s string) string {
	return ansi.Hardwrap(ansi.Wrap(s, m.width, ""), m.width, true)
}

// sanitize remplace les tabulations et retire les caractères de contrôle
// (séquences d'échappement comprises) sauf le saut de ligne, ainsi que les
// caractères de format (contrôles bidirectionnels, largeur nulle, BOM) ; les
// séparateurs de ligne et de paragraphe Unicode deviennent des espaces. Le
// texte vient du modèle ou des outils et ne doit ni piloter le terminal ni
// masquer son contenu.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n':
			return r
		case r == '\u2028', r == '\u2029':
			return ' '
		case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f, r == utf8.RuneError,
			unicode.Is(unicode.Cf, r):
			return -1
		}
		return r
	}, strings.ReplaceAll(s, "\t", tabSpaces))
}

func (m Model) jurisdictionStyle(j llm.Jurisdiction) lipgloss.Style {
	switch j {
	case llm.EU:
		return m.st.eu
	case llm.Local:
		return m.st.local
	}
	return m.st.nonEU
}

// estimateTokens estime les jetons de la conversation : runes / 4.
func (m Model) estimateTokens() int {
	n := 0
	for _, it := range m.tr.Items() {
		n += utf8.RuneCountInString(it.Text)
	}
	return n / tokenRuneRatio
}

// compactCount écrit n en forme courte : entier sous 1000, puis milliers
// (« 12,4 k » en français, « 12.4k » en anglais), sans décimale dès 100 k.
func compactCount(l i18n.Lang, n int64) string {
	if n < 1000 {
		return strconv.FormatInt(n, 10)
	}
	k := float64(n) / 1000
	s := strconv.FormatFloat(k, 'f', 1, 64)
	if k >= 100 {
		s = strconv.FormatFloat(k, 'f', 0, 64)
	}
	s = strings.TrimSuffix(s, ".0")
	if l == i18n.EN {
		return s + "k"
	}
	return strings.Replace(s, ".", ",", 1) + " k"
}
