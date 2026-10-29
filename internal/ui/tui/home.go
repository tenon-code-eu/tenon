package tui

import (
	"os"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/tenon-code-eu/tenon/internal/i18n"
	"github.com/tenon-code-eu/tenon/internal/llm"
	"github.com/tenon-code-eu/tenon/internal/ui/headless"
)

// homeShieldWidth est la largeur de la colonne du bouclier, à gauche du mot
// TENON ; homeShieldHalf la coupe en deux pièces (animation d'ouverture).
const (
	homeShieldWidth = 20
	homeShieldHalf  = 10
)

// homeArt est l'art large : bouclier à gauche, mot TENON à droite.
var homeArt = [6]string{
	"      ╱▔▔▔▔▔▔▔╲     ████████ ███████ ███    ██  ██████  ███    ██",
	"    ╱  ━━━┳━━━  ╲      ██    ██      ████   ██ ██    ██ ████   ██",
	"   ▕  ◦   ┃   ◦  ▏     ██    █████   ██ ██  ██ ██    ██ ██ ██  ██",
	"   ▕ ┃┃   ◉   ┃┃ ▏     ██    ██      ██  ██ ██ ██    ██ ██  ██ ██",
	"    ╲ ┗┛  ┃  ┗┛ ╱      ██    ███████ ██   ████  ██████  ██   ████",
	"      ╲▁▁▁┻▁▁▁╱",
}

const (
	// homeCompactLogo remplace l'art sous homeMinWide colonnes.
	homeCompactLogo = "⬢ TENON"
	// homeMinWide est la largeur de terminal minimale de l'art.
	homeMinWide = 70
	// homeBoxMax et homeBoxMargin : largeur de la zone de saisie,
	// min(homeBoxMax, largeur − homeBoxMargin).
	homeBoxMax    = 72
	homeBoxMargin = 8
	// homeBoxMin est la largeur sous laquelle la zone n'est plus encadrée.
	homeBoxMin = 12
	// homeBoxExtra : bordures et marges intérieures du cadre de saisie.
	homeBoxExtra = 4
	// homeNoticeMin est la réserve minimale des avis sous l'accueil.
	homeNoticeMin = 4
	// homeTips est le nombre d'astuces du jour (clés tui.tip.1 à N).
	homeTips = 11
	// statusProjectMin est la largeur minimale du dossier du projet dans la
	// barre d'état ; en dessous, il disparaît.
	statusProjectMin = 16
	// statusDropRanks est le plus grand rang d'abandon de la barre d'état.
	statusDropRanks = 6
)

// Valeurs de Options.Sandbox.
const (
	// SandboxOn : les commandes shell sont confinées.
	SandboxOn = "sandboxed"
	// SandboxOff : les commandes shell ne sont pas confinées.
	SandboxOff = "not sandboxed"
)

// noColor indique que NO_COLOR demande un rendu sans couleur.
func noColor() bool { return os.Getenv("NO_COLOR") != "" }

// welcomeVersion renvoie la version à afficher : « dev » si vide, préfixée par
// « v » sauf si elle commence déjà par « v » (ou n'est pas un numéro).
func welcomeVersion(v string) string {
	v = headless.OneLine(v)
	if v == "" {
		return "dev"
	}
	if r, _ := utf8.DecodeRuneInString(v); unicode.IsDigit(r) {
		return "v" + v
	}
	return v
}

// shortenHome remplace le dossier personnel par « ~ ».
func shortenHome(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || home == "/" {
		return p
	}
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+string(os.PathSeparator)) {
		return "~" + p[len(home):]
	}
	return p
}

// truncMiddle coupe s au milieu (« … ») pour qu'il tienne en width colonnes.
func truncMiddle(s string, width int) string {
	if width < 1 {
		return ""
	}
	if ansi.StringWidth(s) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	head := (width - 1 + 1) / 2
	tail := width - 1 - head
	return ansi.Truncate(s, head, "") + "…" + ansi.TruncateLeft(s, ansi.StringWidth(s)-tail, "")
}

// text désinfecte et masque un texte variable, sur une seule ligne.
func (m Model) text(s string) string { return sanitize(m.redact(headless.OneLine(s))) }

// tipOfTheDay renvoie une astuce tirée par rnd (rnd(n) dans [0, n)).
func tipOfTheDay(lang i18n.Lang, rnd func(int) int) string {
	i := rnd(homeTips)
	if i < 0 || i >= homeTips {
		i = 0
	}
	return i18n.T(lang, "tui.tip."+strconv.Itoa(i+1))
}

// homeShown indique que l'accueil remplace la conversation : jusqu'au premier
// message (ou à la première réponse d'une commande), jamais pour une session
// reprise ni pendant un panneau.
func (m Model) homeShown() bool {
	return !m.homeDone && len(m.tr.Items()) <= m.seedItems && m.rw == nil && !m.prompting()
}

// homeBoxWidth renvoie la largeur de la zone de saisie de l'accueil, 0 si elle
// n'est pas encadrée (terminal trop étroit).
func (m Model) homeBoxWidth() int {
	w := min(homeBoxMax, m.width-homeBoxMargin)
	if w < homeBoxMin {
		return 0
	}
	return w
}

// inputWidth renvoie la largeur de la saisie : intérieur du cadre de
// l'accueil, ou toute la largeur.
func (m Model) inputWidth() int {
	if m.homeShown() {
		if w := m.homeBoxWidth(); w > 0 {
			return w - homeBoxExtra
		}
	}
	return m.width
}

// syncInput ajuste la largeur de la saisie à la vue courante.
func (m *Model) syncInput() {
	if w := m.inputWidth(); w != m.inputW {
		m.inputW = w
		m.input.SetWidth(w)
	}
}

// paint colore s (rien avec NO_COLOR ou sans couleur).
func (m Model) paint(s string, c lipgloss.Color) string {
	if noColor() || c == "" {
		return s
	}
	return m.st.r.NewStyle().Foreground(c).Render(s)
}

// center centre la ligne l dans la largeur courante (tronquée si besoin).
func (m Model) center(l string) string {
	l = ansi.Truncate(l, m.width, "…")
	if pad := (m.width - ansi.StringWidth(l)) / 2; pad > 0 {
		return strings.Repeat(" ", pad) + l
	}
	return l
}

// artLines rend l'art, centré en bloc ; pendant l'animation d'ouverture, les
// deux moitiés du bouclier sont écartées de off colonnes et le mot est caché.
func (m Model) artLines(off int) []string {
	artW := 0
	for _, l := range homeArt {
		artW = max(artW, utf8.RuneCountInString(l))
	}
	x0 := max(0, (m.width-artW)/2)
	grad := m.theme.gradient(len(homeArt), m.st.structure)
	out := make([]string, len(homeArt))
	for i, l := range homeArt {
		var row string
		if off == 0 {
			row = strings.Repeat(" ", x0) + l
		} else {
			canvas := []rune(strings.Repeat(" ", m.width))
			put := func(s []rune, at int) {
				for j, r := range s {
					if c := at + j; c >= 0 && c < len(canvas) {
						canvas[c] = r
					}
				}
			}
			rs := []rune(l)
			shield := rs[:min(len(rs), homeShieldWidth)]
			for len(shield) < homeShieldWidth {
				shield = append(shield, ' ')
			}
			put(shield[:homeShieldHalf], x0-off)
			put(shield[homeShieldHalf:], x0+homeShieldHalf+off)
			row = string(canvas)
		}
		out[i] = m.paint(ansi.Truncate(strings.TrimRight(row, " "), m.width, ""), grad[i])
	}
	return out
}

// homeLines rend l'accueil seul, à la hauteur que lui laissent les avis du
// démarrage (voir homeView).
func (m Model) homeLines() []string {
	avail := max(0, m.height-statusHeight)
	return m.homeBlock(avail - noticeReserve(avail, m.homeNotices()))
}

// homeNotices rend les avis du démarrage, un élément par avis.
func (m Model) homeNotices() [][]string {
	var out [][]string
	for _, it := range m.tr.Items() {
		if it.Kind != ItemTool && strings.TrimSpace(it.Text) == "" {
			continue
		}
		out = append(out, strings.Split(m.renderItem(it), "\n"))
	}
	return out
}

// noticeReserve renvoie les lignes réservées aux avis sous l'accueil : tous
// (et une ligne vide) s'ils tiennent dans un tiers de la hauteur, sinon ce
// tiers (au moins homeNoticeMin lignes : « … +N » et le dernier avis, chacun
// précédé d'une ligne vide).
func noticeReserve(avail int, notes [][]string) int {
	if len(notes) == 0 {
		return 0
	}
	n := 0
	for _, l := range notes {
		n += len(l) + 1
	}
	return min(n, max(homeNoticeMin, avail/3))
}

// fitNotices garde les derniers avis qui tiennent en room lignes, chacun
// précédé d'une ligne vide, avec « … +N » en tête quand N avis sont omis.
func (m Model) fitNotices(notes [][]string, room int) []string {
	used, k := 0, 0
	for i := len(notes) - 1; i >= 0; i-- {
		need := len(notes[i]) + 1
		if i > 0 {
			// Des avis plus anciens resteront cachés : place pour « … +N ».
			need += 2
		}
		if used+need > room {
			break
		}
		used += len(notes[i]) + 1
		k++
	}
	var out []string
	if hidden := len(notes) - k; hidden > 0 {
		if room-used < 2 {
			return nil
		}
		out = append(out, "", m.st.notice.Render(ansi.Truncate(i18n.T(m.lang, "tui.home.more_notices", hidden), m.width, "")))
	}
	for _, l := range notes[len(notes)-k:] {
		out = append(append(out, ""), l...)
	}
	return out
}

// homeBlock rend l'accueil : logo, slogan et version, zone de saisie
// encadrée, raccourcis, astuce du jour et liens ; chaque élément est centré et
// aucune ligne ne dépasse la largeur. Les éléments secondaires disparaissent
// quand la hauteur avail manque ; la zone de saisie reste toujours.
func (m Model) homeBlock(avail int) []string {
	gray := m.theme.Muted
	version := welcomeVersion(m.opts.Version)
	wide := m.width >= homeMinWide
	logo := func() []string {
		if wide {
			off := 0
			if m.introActive() {
				off = introOffsets[m.introFrame]
			}
			return m.artLines(off)
		}
		return []string{m.center(m.paint(homeCompactLogo, m.st.structure))}
	}
	slogan := []string{m.center(m.paint(i18n.T(m.lang, "tui.home.slogan")+"  "+version, gray))}
	box := m.homeBox()
	shortcuts := []string{m.center(m.paint(i18n.T(m.lang, "tui.home.shortcuts"), gray))}
	var tip []string
	if m.tip != "" {
		lim := m.width
		if bw := m.homeBoxWidth(); bw > 0 {
			lim = bw
		}
		text := i18n.T(m.lang, "tui.home.tip", m.tip)
		for _, l := range strings.Split(ansi.Hardwrap(ansi.Wrap(text, lim, ""), lim, true), "\n") {
			tip = append(tip, m.center(m.paint(l, gray)))
		}
	}
	link := func(url, key string) string {
		return hyperlink(url, m.paint(i18n.T(m.lang, key), gray), m.links)
	}
	links := []string{m.center(link(linkDocs, "tui.home.docs") + m.paint(" · ", gray) +
		link(linkGitHub, "tui.home.github") + m.paint(" · ", gray) + link(linkBug, "tui.home.bug"))}

	build := func(logo, slogan, shortcuts, tip, links []string) []string {
		var out []string
		out = append(out, logo...)
		out = append(out, slogan...)
		out = append(out, "")
		out = append(out, box...)
		out = append(out, shortcuts...)
		if len(tip) > 0 {
			out = append(append(out, ""), tip...)
		}
		if len(links) > 0 {
			out = append(append(out, ""), links...)
		}
		return out
	}
	lines := build(logo(), slogan, shortcuts, tip, links)
	// Hauteur insuffisante : l'astuce, les liens, l'art, les raccourcis puis
	// le slogan cèdent la place, dans cet ordre.
	if len(lines) > avail {
		tip = nil
		lines = build(logo(), slogan, shortcuts, tip, links)
	}
	if len(lines) > avail {
		links = nil
		lines = build(logo(), slogan, shortcuts, tip, links)
	}
	if len(lines) > avail && wide {
		wide = false
		lines = build(logo(), slogan, shortcuts, tip, links)
	}
	if len(lines) > avail {
		lines = build(logo(), slogan, nil, nil, nil)
	}
	if len(lines) > avail {
		lines = build(logo(), nil, nil, nil, nil)
	}
	return lines
}

// homeBox rend la zone de saisie de l'accueil, encadrée et centrée.
func (m Model) homeBox() []string {
	in := m.input.View()
	bw := m.homeBoxWidth()
	if bw == 0 {
		return strings.Split(in, "\n")
	}
	border := m.st.r.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1).Width(bw - 2)
	if !noColor() && m.st.structure != "" {
		border = border.BorderForeground(m.st.structure)
	}
	pad := strings.Repeat(" ", max(0, (m.width-bw)/2))
	var out []string
	for _, l := range strings.Split(border.Render(in), "\n") {
		out = append(out, pad+ansi.Truncate(l, bw, ""))
	}
	return out
}

// homeView rend l'accueil centré verticalement, les avis du démarrage
// dessous, puis la barre d'état.
func (m Model) homeView() string {
	avail := max(0, m.height-statusHeight)
	notes := m.homeNotices()
	content := m.homeBlock(avail - noticeReserve(avail, notes))
	if len(content) > avail {
		// Même réduit, l'accueil dépasse : le haut cède, la zone de saisie
		// (en bas du bloc minimal) reste.
		content = content[len(content)-avail:]
	} else {
		content = append(content, m.fitNotices(notes, avail-len(content))...)
	}
	lines := make([]string, 0, m.height)
	for range (avail - len(content)) / 2 {
		lines = append(lines, "")
	}
	lines = append(lines, content...)
	for len(lines) < avail {
		lines = append(lines, "")
	}
	lines = append(lines, m.statusLine())
	for i, l := range lines {
		if lipgloss.Width(l) > m.width {
			lines[i] = ansi.Truncate(l, m.width, "")
		}
	}
	return strings.Join(lines, "\n")
}

// statusItem est un élément de la barre d'état ; drop est son rang d'abandon
// quand la largeur manque (0 : jamais abandonné, le rang 1 part le premier).
type statusItem struct {
	text  string
	style lipgloss.Style
	drop  int
}

// statusLine rend la barre d'état : dossier du projet (« ~ » abrégé, coupé au
// milieu), modèle, fournisseur, pays, pastilles juridiction et isolation,
// profil, indicateur de session sensible, jetons, session. Les éléments
// secondaires disparaissent quand la largeur manque.
func (m Model) statusLine() string {
	// Fournisseur effectif : le dernier retenu (bascule, session sensible).
	p := m.opts.Provider
	if m.tr.selected != nil {
		p = *m.tr.selected
	}
	st := m.st.status
	items := []statusItem{
		{text: p.Model, style: st},
		{text: headless.ProviderLabel(p), style: st, drop: 6},
		{text: p.Country, style: st, drop: 3},
	}
	pills := []statusItem{{text: " " + m.jurisdictionPill(p.Jurisdiction) + " ", style: m.jurisdictionStyle(p.Jurisdiction)}}
	switch m.opts.Sandbox {
	case SandboxOn:
		pills = append(pills, statusItem{text: " " + i18n.T(m.lang, "tui.pill.sandboxed") + " ", style: m.st.eu})
	case SandboxOff:
		pills = append(pills, statusItem{text: " " + i18n.T(m.lang, "tui.pill.not_sandboxed") + " ", style: m.st.nonEU})
	}
	var tail []statusItem
	if prof := m.opts.Profile; prof != "" {
		tail = append(tail, statusItem{text: i18n.T(m.lang, "tui.status.profile", prof), style: st, drop: 5})
	}
	if r := m.tr.routing; r.Sensitive {
		ind := i18n.T(m.lang, "tui.status_sensitive_no_local")
		if r.Local != "" {
			ind = i18n.T(m.lang, "tui.status_sensitive", r.Local)
		}
		tail = append(tail, statusItem{text: ind, style: st})
	}
	tail = append(tail, statusItem{text: i18n.T(m.lang, "tui.status_tokens", m.estimateTokens(), m.opts.ContextWindow), style: st, drop: 2})
	if t := m.tr; t.usageSeen {
		tail = append(tail, statusItem{text: i18n.T(m.lang, "tui.status_usage", compactCount(m.lang, t.turnIn), compactCount(m.lang, t.turnOut), compactCount(m.lang, t.sessionTokens)), style: st, drop: 4})
	}
	tail = append(tail, statusItem{text: m.opts.SessionID, style: st, drop: 1})

	const sep = " · "
	project := m.text(shortenHome(m.opts.ProjectRoot))
	var prefix string
	if m.unseen {
		prefix = i18n.T(m.lang, "tui.scroll.new")
	}
	// width renvoie la largeur de la barre, le projet sur pw colonnes.
	width := func(pw int) int {
		w := ansi.StringWidth(prefix)
		n := 0
		add := func(s string) {
			if n > 0 {
				w += len([]rune(sep))
			}
			w += ansi.StringWidth(s)
			n++
		}
		if pw > 0 {
			add(strings.Repeat("x", pw))
		}
		for _, it := range items {
			if it.text != "" {
				add(m.text(it.text))
			}
		}
		pillW := 0
		for i, it := range pills {
			pillW += ansi.StringWidth(it.text)
			if i > 0 {
				pillW++
			}
		}
		add(strings.Repeat("x", pillW))
		for _, it := range tail {
			if it.text != "" {
				add(m.text(it.text))
			}
		}
		return w
	}
	projMin := min(statusProjectMin, ansi.StringWidth(project))
	// Abandon des éléments secondaires, du rang le plus élevé au plus bas.
	for rank := 1; rank <= statusDropRanks && width(projMin) > m.width; rank++ {
		for _, list := range []*[]statusItem{&items, &tail} {
			for i := range *list {
				if (*list)[i].drop == rank {
					(*list)[i].text = ""
				}
			}
		}
	}
	pw := 0
	if project != "" {
		if rest := m.width - width(0) - len([]rune(sep)); rest >= projMin && rest > 0 {
			pw = rest
		}
	}

	var b strings.Builder
	n := 0
	write := func(s string, style lipgloss.Style) {
		if n > 0 {
			b.WriteString(st.Render(sep))
		}
		b.WriteString(style.Render(s))
		n++
	}
	if prefix != "" {
		b.WriteString(st.Render(prefix))
	}
	if pw > 0 {
		write(truncMiddle(project, pw), st)
	}
	for _, it := range items {
		if it.text != "" {
			write(m.text(it.text), it.style)
		}
	}
	if n > 0 {
		b.WriteString(st.Render(sep))
	}
	for i, it := range pills {
		if i > 0 {
			b.WriteString(st.Render(" "))
		}
		b.WriteString(it.style.Render(it.text))
	}
	n++
	for _, it := range tail {
		if it.text != "" {
			write(m.text(it.text), it.style)
		}
	}
	line := b.String()
	if lipgloss.Width(line) > m.width {
		line = ansi.Truncate(line, m.width, "")
	}
	if pad := m.width - lipgloss.Width(line); pad > 0 {
		line += st.Render(strings.Repeat(" ", pad))
	}
	return line
}

// jurisdictionPill renvoie le libellé de la pastille de juridiction
// (inconnue : hors UE).
func (m Model) jurisdictionPill(j llm.Jurisdiction) string {
	switch j {
	case llm.EU:
		return i18n.T(m.lang, "tui.pill.eu")
	case llm.Local:
		return i18n.T(m.lang, "tui.pill.local")
	}
	return i18n.T(m.lang, "tui.pill.non_eu")
}
