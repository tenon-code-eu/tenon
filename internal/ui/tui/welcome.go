package tui

import (
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/tenon-code-eu/tenon/internal/i18n"
	"github.com/tenon-code-eu/tenon/internal/ui/headless"
)

// welcomeShieldWidth est la largeur de la colonne du bouclier ; le slogan et la
// version s'alignent sous le mot TENON, juste après.
const welcomeShieldWidth = 20

// welcomeArt est l'art large : bouclier à gauche, mot TENON à droite.
var welcomeArt = [6]string{
	"      ╱▔▔▔▔▔▔▔╲     ████████ ███████ ███    ██  ██████  ███    ██",
	"    ╱  ━━━┳━━━  ╲      ██    ██      ████   ██ ██    ██ ████   ██",
	"   ▕  ◦   ┃   ◦  ▏     ██    █████   ██ ██  ██ ██    ██ ██ ██  ██",
	"   ▕ ┃┃   ◉   ┃┃ ▏     ██    ██      ██  ██ ██ ██    ██ ██  ██ ██",
	"    ╲ ┗┛  ┃  ┗┛ ╱      ██    ███████ ██   ████  ██████  ██   ████",
	"      ╲▁▁▁┻▁▁▁╱     ",
}

// welcomeGradient va du bleu au cyan, une teinte par ligne de l'art.
var welcomeGradient = [6]string{"#1e3a8a", "#1d4ed8", "#2563eb", "#0ea5e9", "#06b6d4", "#22d3ee"}

const (
	welcomeBorderColor = "6"
	welcomeTaglineGray = "250"
	// welcomeFrameExtra : bordures et marges intérieures du cadre.
	welcomeFrameExtra = 4
	// welcomeMinFrame est la largeur sous laquelle le cadre est abandonné.
	welcomeMinFrame = 8
	// welcomeMinWide est la largeur de terminal minimale de l'art.
	welcomeMinWide = 70
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

// welcomeLines rend le cadre de bienvenue, replié à la largeur courante ; nil
// si la largeur ne permet aucun cadre utile.
func (m Model) welcomeLines() []string {
	version := welcomeVersion(m.opts.Version)
	tagline := i18n.T(m.lang, "tui.welcome.tagline")
	art := welcomeArt
	slogan := tagline + "  " + version
	wide := welcomeShieldWidth + utf8.RuneCountInString(slogan)
	for _, l := range art[:5] {
		wide = max(wide, utf8.RuneCountInString(l))
	}
	useArt := wide+welcomeFrameExtra <= m.width && m.width >= welcomeMinWide

	inner := wide
	if !useArt {
		inner = m.width - welcomeFrameExtra
	}
	framed := inner >= welcomeMinFrame-welcomeFrameExtra && m.width >= welcomeMinFrame
	if !framed {
		inner = m.width
	}

	colored := !noColor()
	paint := func(s, color string) string {
		if !colored {
			return s
		}
		return m.st.r.NewStyle().Foreground(lipgloss.Color(color)).Render(s)
	}

	var body []string
	if useArt {
		for i, l := range art[:5] {
			body = append(body, paint(l, welcomeGradient[i]))
		}
		body = append(body, paint(art[5], welcomeGradient[5])+paint(tagline+"  "+version, welcomeTaglineGray))
	} else {
		body = append(body, paint("⬢ TENON", welcomeGradient[3])+"  "+paint(version, welcomeTaglineGray), paint(tagline, welcomeTaglineGray))
	}
	body = append(body, "")

	p := m.opts.Provider
	var parts []string
	for _, s := range []string{p.Model, headless.ProviderLabel(p), p.Country} {
		if s = m.text(s); s != "" {
			parts = append(parts, s)
		}
	}
	jur := i18n.T(m.lang, "banner.prefix", headless.JurisdictionLabel(m.lang, p.Jurisdiction))
	jurStyled := jur
	if colored {
		jurStyled = m.jurisdictionStyle(p.Jurisdiction).Render(jur)
	}
	info := strings.Join(append(parts, jurStyled), " · ")
	body = append(body, info)

	label := i18n.T(m.lang, "tui.welcome.project", "")
	root := truncMiddle(m.text(shortenHome(m.opts.ProjectRoot)), max(1, inner-ansi.StringWidth(label)))
	body = append(body, label+root, i18n.T(m.lang, "tui.welcome.hints"))

	// Chaque ligne est tronquée à la largeur du cadre.
	lines := make([]string, len(body))
	for i, l := range body {
		lines[i] = ansi.Truncate(l, inner, "…")
	}

	if !framed {
		return lines
	}
	border := m.st.r.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
	if colored {
		border = border.BorderForeground(lipgloss.Color(welcomeBorderColor))
	}
	return strings.Split(border.Render(strings.Join(lines, "\n")), "\n")
}
