package tui

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/i18n"
	"github.com/tenon-code-eu/tenon/internal/llm"
)

const (
	// sidebarWidth est la largeur du panneau latéral, sans le trait qui le
	// sépare du fil.
	sidebarWidth = 26
	// sidebarAutoWidth est la largeur de terminal à partir de laquelle le
	// panneau s'ouvre de lui-même (sans choix manuel).
	sidebarAutoWidth = 100
	// sidebarMinConv est la largeur minimale du fil à côté du panneau : en
	// dessous, le panneau reste masqué, même ouvert par Ctrl+B.
	sidebarMinConv = 30
	// sidebarMaxFiles est le nombre maximal de fichiers listés (puis « +N »).
	sidebarMaxFiles = 8
	// gaugeCells est le nombre de cellules de la jauge de contexte.
	gaugeCells = 10
	// sidebarSep sépare le fil du panneau.
	sidebarSep = "│"
	// purposeCompaction est l'objet d'un appel de compaction (Usage.Purpose).
	purposeCompaction = "compaction"
)

// fileChange est un fichier modifié pendant la session : Op vaut M (modifié),
// A (créé) ou D (supprimé) ; Add et Del sont les lignes ajoutées et retirées,
// affichées seulement si known.
type fileChange struct {
	Path, Op string
	Add, Del int
	known    bool
}

// sendCount est le nombre d'envois (appels au modèle) à un fournisseur.
type sendCount struct {
	Provider, Country, Jurisdiction string
	N                               int
	id                              string
}

// lineDelta : lignes ajoutées et retirées annoncées par les arguments d'un
// appel d'écriture ; known est faux si les retraits sont inconnus.
type lineDelta struct {
	add, del int
	known    bool
}

// sessionStats agrège les événements de la session pour le panneau latéral.
// Les textes y sont bruts : le rendu les masque et les nettoie (Model.text).
type sessionStats struct {
	Title             string
	LastInput, Window int
	In, Out           int64
	Files             []fileChange
	Sends             []sendCount
	Sandbox, Profile  string
	Policy            string

	// providers : fournisseurs configurés (pays, juridiction) ; root : racine
	// du projet (chemins relatifs) ; window : fenêtre par défaut.
	providers map[string]llm.ProviderInfo
	root      string
	window    int
	// pending : lignes annoncées par les appels d'écriture en attente de
	// résultat, par appel (parent et call_id).
	pending map[string]lineDelta
}

func newSessionStats(o Options) *sessionStats {
	return &sessionStats{
		Window: o.ContextWindow, Sandbox: o.Sandbox, Profile: o.Profile, Policy: o.Policy,
		providers: o.Providers, root: o.ProjectRoot, window: o.ContextWindow,
	}
}

// writeTools sont les outils dont un résultat réussi modifie un fichier.
var writeTools = map[string]bool{"edit_file": true, "write_file": true}

// apply intègre un événement : premier message de l'utilisateur (titre),
// consommation (jetons, envois, jauge de l'agent principal), écritures de
// fichiers.
func (s *sessionStats) apply(ev event.Event) {
	switch ev.Type {
	case event.UserMessage:
		var p event.UserMessagePayload
		if json.Unmarshal(ev.Payload, &p) != nil || p.ParentCallID != "" || p.Hook != "" || s.Title != "" {
			return
		}
		s.Title = p.Text
		if p.Typed != "" {
			s.Title = p.Typed
		}
	case event.Usage:
		var p event.UsagePayload
		if json.Unmarshal(ev.Payload, &p) != nil {
			return
		}
		s.In += int64(p.Input)
		s.Out += int64(p.Output)
		s.addSend(p)
		// Jauge : dernier appel de l'agent principal hors compaction (un
		// sous-agent a son propre contexte ; la compaction résume l'ancien).
		if p.ParentCallID == "" && p.Purpose != purposeCompaction {
			s.LastInput = p.Input
			s.Window = s.window
			if p.Window > 0 {
				s.Window = p.Window
			}
		}
	case event.ToolCallRequested:
		var p event.ToolCallRequestedPayload
		if json.Unmarshal(ev.Payload, &p) != nil || !writeTools[p.Tool] {
			return
		}
		var a struct {
			OldString string `json:"old_string"`
			NewString string `json:"new_string"`
			Content   string `json:"content"`
		}
		if json.Unmarshal(p.Args, &a) != nil {
			return
		}
		d := lineDelta{add: countLines(a.Content), known: false}
		if p.Tool == "edit_file" {
			d = lineDelta{add: countLines(a.NewString), del: countLines(a.OldString), known: true}
		}
		if s.pending == nil {
			s.pending = map[string]lineDelta{}
		}
		s.pending[p.ParentCallID+"\x00"+p.CallID] = d
	case event.ToolResult:
		var p event.ToolResultPayload
		if json.Unmarshal(ev.Payload, &p) != nil {
			return
		}
		key := p.ParentCallID + "\x00" + p.CallID
		d, ok := s.pending[key]
		delete(s.pending, key)
		if !writeTools[p.Tool] || p.IsError || p.Denied != "" || p.Path == "" || p.HashAfter == "" {
			return
		}
		op := "M"
		if p.HashBefore == "" {
			op = "A"
			// Fichier créé : aucune ligne retirée.
			d.known = ok
		}
		s.addFile(s.relPath(p.Path), op, d, ok)
	}
}

// addSend compte un envoi au fournisseur de p.
func (s *sessionStats) addSend(p event.UsagePayload) {
	info := s.providers[p.Provider]
	for i := range s.Sends {
		if s.Sends[i].id == p.Provider {
			s.Sends[i].N++
			return
		}
	}
	name := p.ProviderName
	if name == "" {
		name = info.Name
	}
	if name == "" {
		name = p.Provider
	}
	j := p.Jurisdiction
	if j == "" {
		j = info.Jurisdiction
	}
	s.Sends = append(s.Sends, sendCount{Provider: name, Country: info.Country, Jurisdiction: string(j), N: 1, id: p.Provider})
}

// addFile ajoute ou met à jour le fichier path ; un fichier créé pendant la
// session reste « A ».
func (s *sessionStats) addFile(path, op string, d lineDelta, ok bool) {
	for i := range s.Files {
		f := &s.Files[i]
		if f.Path != path {
			continue
		}
		f.Add += d.add
		f.Del += d.del
		f.known = f.known && ok && d.known
		if f.Op == "D" {
			f.Op = op
		}
		return
	}
	s.Files = append(s.Files, fileChange{Path: path, Op: op, Add: d.add, Del: d.del, known: ok && d.known})
}

// relPath rend path relatif à la racine du projet quand il s'y trouve.
func (s *sessionStats) relPath(path string) string {
	if s.root == "" {
		return path
	}
	rel, err := filepath.Rel(s.root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return path
	}
	return filepath.ToSlash(rel)
}

// countLines compte les lignes d'un texte (une dernière ligne sans saut
// compte).
func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}

// sidebarOpen indique que le panneau est ouvert : choix manuel (Ctrl+B) s'il
// y en a eu un, sinon selon la largeur.
func (m Model) sidebarOpen() bool {
	if m.panelSet {
		return m.panelOpen
	}
	return m.width >= sidebarAutoWidth
}

// sidebarShown indique que le panneau est affiché : ouvert, hors accueil, et
// le fil garde au moins sidebarMinConv colonnes.
func (m Model) sidebarShown() bool {
	return m.sidebarOpen() && !m.homeShown() && m.width-sidebarWidth-1 >= sidebarMinConv
}

// toggleSidebar bascule le panneau ; le choix vaut jusqu'à la fin de la
// session, quelle que soit la largeur.
func (m *Model) toggleSidebar() {
	m.panelOpen = !m.sidebarOpen()
	m.panelSet = true
}

// convWidth renvoie la largeur du fil : toute la largeur, moins le panneau
// et son trait quand il est affiché.
func (m Model) convWidth() int {
	if m.sidebarShown() {
		return m.width - sidebarWidth - 1
	}
	return m.width
}

// sidebarLines rend le panneau sur height lignes de sidebarWidth colonnes
// exactement : titre et session, contexte (jauge, jetons), fichiers
// modifiés, souveraineté (envois, isolation, profil, politique), et les
// décomptes des skills, agents et hooks sur la dernière ligne. Tout texte
// variable passe par Model.text (masqué, nettoyé, sur une ligne).
func (m Model) sidebarLines(height int) []string {
	if height < 1 {
		return nil
	}
	s := m.stats
	w := sidebarWidth - 1 // une colonne de marge après le trait
	muted := m.st.muted
	head := m.st.head
	fit := func(t string) string { return ansi.Truncate(t, w, "…") }
	var body []string
	add := func(style lipgloss.Style, t string) { body = append(body, style.Render(fit(t))) }

	title := m.text(s.Title)
	if title == "" {
		title = i18n.T(m.lang, "tui.panel.untitled")
	}
	add(m.st.user, title)
	if id := m.text(m.opts.SessionID); id != "" {
		body = append(body, muted.Render(truncMiddle(i18n.T(m.lang, "tui.panel.session", id), w)))
	}

	body = append(body, "")
	add(head, i18n.T(m.lang, "tui.panel.context"))
	add(m.st.assistant, m.gauge(s.LastInput, s.Window))
	add(muted, i18n.T(m.lang, "tui.panel.tokens", compactCount(m.lang, s.In), compactCount(m.lang, s.Out)))

	body = append(body, "")
	add(head, i18n.T(m.lang, "tui.panel.files"))
	if len(s.Files) == 0 {
		add(muted, i18n.T(m.lang, "tui.panel.no_files"))
	}
	for i, f := range s.Files {
		if i == sidebarMaxFiles {
			add(muted, "+"+strconv.Itoa(len(s.Files)-sidebarMaxFiles))
			break
		}
		counts := ""
		if f.known {
			counts = " +" + strconv.Itoa(f.Add)
			if f.Del > 0 {
				counts += " −" + strconv.Itoa(f.Del)
			}
		}
		path := truncMiddle(m.text(f.Path), max(1, w-2-ansi.StringWidth(counts)))
		style := m.st.ok
		switch f.Op {
		case "M":
			style = m.st.tool
		case "D":
			style = m.st.del
		}
		body = append(body, style.Render(f.Op)+" "+m.st.assistant.Render(path)+muted.Render(counts))
	}

	body = append(body, "")
	add(head, i18n.T(m.lang, "tui.panel.sovereignty"))
	if len(s.Sends) == 0 {
		add(muted, i18n.T(m.lang, "tui.panel.no_sends"))
	}
	for _, c := range s.Sends {
		t := "● " + strconv.Itoa(c.N) + " → " + m.text(c.Provider)
		if country := m.text(c.Country); country != "" {
			t += " · " + country
		}
		switch llm.Jurisdiction(c.Jurisdiction) {
		case llm.EU, llm.Local:
			add(m.st.assistant, t)
		default:
			// Hors UE ou juridiction inconnue : en ambre.
			add(m.st.alert, t)
		}
	}
	switch s.Sandbox {
	case SandboxOn:
		add(m.st.ok, i18n.T(m.lang, "tui.pill.sandboxed"))
	case SandboxOff:
		add(m.st.alert, i18n.T(m.lang, "tui.pill.not_sandboxed"))
	}
	if prof := m.text(s.Profile); prof != "" {
		add(muted, i18n.T(m.lang, "tui.status.profile", prof))
	}
	// La politique (nom, version, clé) est longue : repliée sur le panneau.
	if pol := m.text(s.Policy); pol != "" {
		for _, l := range strings.Split(ansi.Hardwrap(ansi.Wrap(pol, w, ""), w, true), "\n") {
			add(muted, l)
		}
	}

	// Décomptes en bas du panneau, coupés aux « · » s'ils dépassent.
	var counts []string
	for _, l := range wrapAtSep(m.countsLine(), w) {
		counts = append(counts, muted.Render(fit(l)))
	}
	if len(counts) > height {
		counts = counts[len(counts)-height:]
	}
	room := height - len(counts)
	if len(body) > room {
		body = body[:room]
	}
	for len(body) < room {
		body = append(body, "")
	}
	body = append(body, counts...)
	for i, l := range body {
		body[i] = " " + l + strings.Repeat(" ", max(0, w-lipgloss.Width(l)))
	}
	return body
}

// wrapAtSep coupe s aux séparateurs « · » pour que chaque ligne tienne en w
// colonnes (une partie trop longue reste seule sur sa ligne).
func wrapAtSep(s string, w int) []string {
	const sep = " · "
	var out []string
	cur := ""
	for _, part := range strings.Split(s, sep) {
		switch {
		case cur == "":
			cur = part
		case ansi.StringWidth(cur+sep+part) <= w:
			cur += sep + part
		default:
			out = append(out, cur)
			cur = part
		}
	}
	return append(out, cur)
}

// gauge rend la jauge du contexte : 10 cellules et le pourcentage du dernier
// appel de l'agent principal dans la fenêtre du fournisseur.
func (m Model) gauge(input, window int) string {
	pct := 0
	if window > 0 {
		pct = min(100, input*100/window)
	}
	filled := pct * gaugeCells / 100
	return strings.Repeat("█", filled) + strings.Repeat("░", gaugeCells-filled) + " " + i18n.T(m.lang, "tui.panel.percent", pct)
}

// countsLine renvoie « skills N · agents N · hooks N » (0 si absent).
func (m Model) countsLine() string {
	var nSkills, nAgents, nHooks int
	if m.opts.Skills != nil {
		nSkills = len(m.opts.Skills.List())
	}
	if m.opts.Agents != nil {
		nAgents = len(m.opts.Agents.List())
	}
	if m.opts.Hooks != nil {
		nHooks = len(m.opts.Hooks.List())
	}
	return i18n.T(m.lang, "tui.panel.counts", nSkills, nAgents, nHooks)
}

// withSidebar place le panneau à droite des lignes du fil (déjà à la
// largeur du fil), séparé par un trait.
func (m Model) withSidebar(conv []string) []string {
	panel := m.sidebarLines(len(conv))
	cw := m.convWidth()
	sep := m.paint(sidebarSep, m.st.structure)
	out := make([]string, len(conv))
	for i, l := range conv {
		if lipgloss.Width(l) > cw {
			l = ansi.Truncate(l, cw, "")
		}
		out[i] = l + strings.Repeat(" ", max(0, cw-lipgloss.Width(l))) + sep + panel[i]
	}
	return out
}

// answerBadge est le fournisseur du dernier appel de l'agent principal d'un
// tour, affiché sous sa réponse.
type answerBadge struct {
	ID, Name     string
	Jurisdiction llm.Jurisdiction
}

// badgeLine rend le badge b : « fournisseur · pays · juridiction » (pays
// omis s'il est inconnu), atténué, ou en ambre hors UE et pour une
// juridiction inconnue.
func (m Model) badgeLine(b answerBadge) string {
	info := m.opts.Providers[b.ID]
	name := b.Name
	if name == "" {
		name = info.Name
	}
	if name == "" {
		name = b.ID
	}
	j := b.Jurisdiction
	if j == "" {
		j = info.Jurisdiction
	}
	parts := []string{m.text(name)}
	if c := m.text(info.Country); c != "" {
		parts = append(parts, c)
	}
	parts = append(parts, m.jurisdictionPill(j))
	t := ansi.Truncate(strings.Join(parts, " · "), m.width, "…")
	if j == llm.EU || j == llm.Local {
		return m.st.muted.Render(t)
	}
	return m.st.alert.Render(t)
}
