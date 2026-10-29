package tui

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/tenon-code-eu/tenon/internal/i18n"
)

// Theme est la palette de rôles d'un thème de la TUI ; tout rendu coloré passe
// par ces rôles (jamais par une couleur en dur).
type Theme struct {
	Name string
	// Structure : fond (badges) et, sur fond clair, texte (cadres, logo).
	Structure lipgloss.Color
	// StructureDark remplace Structure comme couleur de texte sur fond sombre
	// ("" : Structure), pour garder un contraste d'au moins 4,5:1.
	StructureDark lipgloss.Color
	// Accent : titres, outils, fin du dégradé du logo.
	Accent lipgloss.Color
	// OK : réussite, juridiction UE, ajouts.
	OK lipgloss.Color
	// Alert : avertissement (ambre : hors UE, juridiction inconnue).
	Alert lipgloss.Color
	// Error : lignes d'erreur et suppressions d'un diff (rouge).
	Error lipgloss.Color
	// Muted : texte atténué (avis, aides).
	Muted lipgloss.Color
	// Text : texte courant ("" : couleur du terminal).
	Text lipgloss.Color
	// Selection : fond de l'entrée sélectionnée (palette, listes).
	Selection lipgloss.Color
}

// themes, dans l'ordre de ThemeNames ; le premier est le thème par défaut.
var themes = []Theme{
	// tenon : bleu et or du drapeau européen (#003399 et #FFCC00, couleurs
	// officielles du drapeau de l'UE ; StructureDark : bleu plus clair, 5,4:1 sur
	// #1e1e1e) ; rouge #F85149 (danger Primer dark) ; vert, ambre et gris propres à tenon.
	{Name: "tenon", Structure: "#003399", StructureDark: "#6C8CFF", Accent: "#FFCC00", OK: "#2EA043", Alert: "#FFB000", Error: "#F85149", Muted: "#8B949E", Text: "", Selection: "#1B3A8C"},
	// Tokyo Night (variante night) : https://github.com/folke/tokyonight.nvim
	// (extras/lua/tokyonight_night.lua) : blue #7aa2f7, yellow #e0af68,
	// red #f7768e, green #9ece6a, orange #ff9e64, comment #565f89, fg #c0caf5, bg_highlight #292e42.
	{Name: "tokyonight", Structure: "#7aa2f7", Accent: "#e0af68", OK: "#9ece6a", Alert: "#ff9e64", Error: "#f7768e", Muted: "#565f89", Text: "#c0caf5", Selection: "#292e42"},
	// Catppuccin Mocha : https://catppuccin.com/palette (flavor Mocha) :
	// red #f38ba8, blue #89b4fa, yellow #f9e2af, green #a6e3a1, peach #fab387, overlay0 #6c7086,
	// text #cdd6f4, surface1 #45475a.
	{Name: "catppuccin", Structure: "#89b4fa", Accent: "#f9e2af", OK: "#a6e3a1", Alert: "#fab387", Error: "#f38ba8", Muted: "#6c7086", Text: "#cdd6f4", Selection: "#45475a"},
	// Gruvbox dark : https://github.com/morhetz/gruvbox#palette : bright blue
	// #83a598, bright red #fb4934, bright yellow #fabd2f, bright green #b8bb26, bright orange #fe8019,
	// gray #928374, fg1 #ebdbb2, bg2 #504945.
	{Name: "gruvbox", Structure: "#83a598", Accent: "#fabd2f", OK: "#b8bb26", Alert: "#fe8019", Error: "#fb4934", Muted: "#928374", Text: "#ebdbb2", Selection: "#504945"},
	// light, pour fond clair : GitHub Primer (light) :
	// https://primer.style/foundations/color : accent #0969da, attention #9a6700,
	// success #1a7f37, danger #cf222e, severe #bc4c00, muted #57606a, fg #1f2328, accent-subtle #ddf4ff.
	{Name: "light", Structure: "#0969da", Accent: "#9a6700", OK: "#1a7f37", Alert: "#bc4c00", Error: "#cf222e", Muted: "#57606a", Text: "#1f2328", Selection: "#ddf4ff"},
}

// ThemeNames renvoie les noms des thèmes, le thème par défaut en premier.
func ThemeNames() []string {
	out := make([]string, len(themes))
	for i, t := range themes {
		out[i] = t.Name
	}
	return out
}

// ThemeByName renvoie le thème de ce nom.
func ThemeByName(name string) (Theme, bool) {
	for _, t := range themes {
		if t.Name == name {
			return t, true
		}
	}
	return Theme{}, false
}

// defaultTheme est le thème « tenon ».
func defaultTheme() Theme { return themes[0] }

// StructureFg renvoie la couleur de texte de Structure : la variante claire
// sur fond sombre (dark), Structure sinon.
func (t Theme) StructureFg(dark bool) lipgloss.Color {
	if dark && t.StructureDark != "" {
		return t.StructureDark
	}
	return t.Structure
}

// onFill renvoie le texte (noir ou blanc) le plus lisible sur le fond c.
func onFill(c lipgloss.Color) lipgloss.Color {
	r, g, b, ok := parseHex(c)
	if !ok {
		return "#FFFFFF"
	}
	lin := func(v int) float64 {
		x := float64(v) / 255
		if x <= 0.03928 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	l := 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
	if 1.05/(l+0.05) >= (l+0.05)/0.05 {
		return "#FFFFFF"
	}
	return "#000000"
}

// gradient renvoie n teintes allant de Structure à Accent (logo de bienvenue).
func (t Theme) gradient(n int, from lipgloss.Color) []lipgloss.Color {
	out := make([]lipgloss.Color, n)
	for i := range out {
		out[i] = lerpColor(from, t.Accent, i, n-1)
	}
	return out
}

// lerpColor interpole entre deux couleurs « #rrggbb » (pas i sur steps) ; une
// couleur illisible donne la première.
func lerpColor(a, b lipgloss.Color, i, steps int) lipgloss.Color {
	ar, ag, ab, ok1 := parseHex(a)
	br, bg, bb, ok2 := parseHex(b)
	if !ok1 || !ok2 || steps <= 0 {
		return a
	}
	mix := func(x, y int) int { return x + (y-x)*i/steps }
	return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", mix(ar, br), mix(ag, bg), mix(ab, bb)))
}

func parseHex(c lipgloss.Color) (r, g, b int, ok bool) {
	s := string(c)
	if len(s) != 7 || s[0] != '#' {
		return 0, 0, 0, false
	}
	v, err := strconv.ParseUint(s[1:], 16, 32)
	if err != nil {
		return 0, 0, 0, false
	}
	return int(v >> 16), int(v >> 8 & 0xff), int(v & 0xff), true
}

// setTheme change le thème actif et reconstruit les styles (même renderer).
func (m *Model) setTheme(th Theme) {
	m.theme = th
	m.st = newStyles(m.st.r, th)
}

// themeCommand traite « /theme » (liste) et « /theme <nom> » (changement pour
// la session).
func (m *Model) themeCommand(arg string) {
	names := strings.Join(ThemeNames(), ", ")
	if arg == "" {
		m.tr.notice(i18n.T(m.lang, "tui.theme.list", names, m.theme.Name))
		return
	}
	th, ok := ThemeByName(arg)
	if !ok {
		m.tr.add(Item{Kind: ItemError, Text: i18n.T(m.lang, "tui.theme.unknown", cleanToken(arg), names)})
		return
	}
	m.setTheme(th)
	m.tr.notice(i18n.T(m.lang, "tui.theme.set", th.Name))
}
