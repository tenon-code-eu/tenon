package tui

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/tenon-code-eu/tenon/internal/i18n"
	"github.com/tenon-code-eu/tenon/internal/llm"
	"github.com/tenon-code-eu/tenon/internal/ui/headless"
)

const tabSpaces = "    "

// styles regroupe les styles lipgloss de la TUI.
type styles struct {
	banner, user, assistant, tool, notice, err, busy, status lipgloss.Style
	eu, local, nonEU                                         lipgloss.Style
	perm, add, del                                           lipgloss.Style
	md                                                       mdStyles
	// r est le renderer qui a produit ces styles (écran de bienvenue).
	r *lipgloss.Renderer
}

func newStyles(r *lipgloss.Renderer) styles {
	return styles{
		r:         r,
		banner:    r.NewStyle().Bold(true).Foreground(lipgloss.Color("6")),
		user:      r.NewStyle().Bold(true),
		assistant: r.NewStyle(),
		tool:      r.NewStyle().Foreground(lipgloss.Color("5")),
		notice:    r.NewStyle().Italic(true).Foreground(lipgloss.Color("8")),
		err:       r.NewStyle().Foreground(lipgloss.Color("1")),
		busy:      r.NewStyle().Faint(true),
		status:    r.NewStyle().Reverse(true),
		eu:        r.NewStyle().Reverse(true).Foreground(lipgloss.Color("2")),
		local:     r.NewStyle().Reverse(true).Foreground(lipgloss.Color("4")),
		nonEU:     r.NewStyle().Reverse(true).Foreground(lipgloss.Color("208")),
		perm:      r.NewStyle().Bold(true).Foreground(lipgloss.Color("3")),
		add:       r.NewStyle().Foreground(lipgloss.Color("2")),
		del:       r.NewStyle().Foreground(lipgloss.Color("1")),
		md: mdStyles{
			bold:    r.NewStyle().Bold(true),
			code:    r.NewStyle().Foreground(lipgloss.Color("6")),
			heading: r.NewStyle().Bold(true).Underline(true),
		},
	}
}

// View rend la conversation, la saisie et la barre d'état ; aucune ligne ne
// dépasse la largeur courante.
func (m Model) View() string {
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
	lines = append(lines, m.bottomLines()...)
	lines = append(lines, m.statusBar())
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
// largeur courante.
func (m Model) conversationLines() []string {
	// Le cadre de bienvenue est un élément d'affichage local, premier de la
	// conversation : il défile avec elle et n'entre pas dans la transcription.
	out := m.welcomeLines()
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
	return m.st.assistant.Render(m.wrapStyled(renderMarkdown(text, m.st.md)))
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

// statusBar rend la barre d'état :
// tenon · modèle · fournisseur · pays · juridiction · session · ~jetons/fenêtre.
func (m Model) statusBar() string {
	p := m.opts.Provider
	parts := []string{"tenon", p.Model, headless.ProviderLabel(p), p.Country}
	jur := i18n.T(m.lang, "banner.prefix", headless.JurisdictionLabel(m.lang, p.Jurisdiction))
	tail := []string{m.opts.SessionID, i18n.T(m.lang, "tui.status_tokens", m.estimateTokens(), m.opts.ContextWindow)}
	sep := " · "
	var b strings.Builder
	write := func(ss []string) {
		for _, s := range ss {
			if s == "" {
				continue
			}
			if b.Len() > 0 {
				b.WriteString(m.st.status.Render(sep))
			}
			b.WriteString(m.st.status.Render(sanitize(s)))
		}
	}
	write(parts)
	b.WriteString(m.st.status.Render(sep))
	b.WriteString(m.jurisdictionStyle(p.Jurisdiction).Render(jur))
	write(tail)
	line := b.String()
	if pad := m.width - lipgloss.Width(line); pad > 0 {
		line += m.st.status.Render(strings.Repeat(" ", pad))
	}
	return line
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
