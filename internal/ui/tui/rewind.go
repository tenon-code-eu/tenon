package tui

// Ce fichier gère /rewind (et Échap deux fois) : panneau à trois étapes
// affiché à la place de la zone de saisie (points, aperçu et mode, conflits),
// puis bilan en avis et prompt remis dans la saisie.

import (
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/tenon-code-eu/tenon/internal/checkpoint"
	"github.com/tenon-code-eu/tenon/internal/engine"
	"github.com/tenon-code-eu/tenon/internal/i18n"
)

const (
	rewindCommand = "/rewind"
	// escWindow : délai maximal entre deux Échap pour ouvrir /rewind.
	escWindow = time.Second
	// rewindRows borne le nombre de lignes de liste affichées.
	rewindRows = 8
)

// Étapes du panneau.
const (
	stepPoints = iota
	stepMode
	stepConflicts
	stepRunning
)

// rewindMode est un choix de mode ; ok faux : grisé, non sélectionnable.
type rewindMode struct {
	mode engine.RewindMode
	key  string
	ok   bool
}

// rewindUI est l'état du panneau ; nil dans le modèle quand il est fermé.
type rewindUI struct {
	step   int
	points []checkpoint.Point
	cursor int

	plan   checkpoint.Plan
	modes  []rewindMode
	mcur   int
	mode   engine.RewindMode
	confl  []string // chemins en conflit, à interroger un par un
	ci     int
	forced []string // chemins à écraser
}

// rewindDone est le résultat de Rewind.
type rewindDone struct {
	res  engine.RewindResult
	err  error
	mode engine.RewindMode
}

// openRewind ouvre le panneau, ou signale qu'il n'y a rien à revenir.
func (m *Model) openRewind() {
	pts := m.eng.RewindPoints(m.opts.SessionID)
	if len(pts) == 0 {
		m.tr.notice(i18n.T(m.lang, "tui.rewind.nothing"))
		m.scrollBack, m.unseen = 0, false
		return
	}
	m.rw = &rewindUI{points: pts}
	m.scrollBack, m.unseen = 0, false
}

// clean nettoie et masque un texte venu de la session ou du disque.
func (m Model) clean(s string) string {
	return strings.Join(strings.Fields(spaceClean(m.redact(s))), " ")
}

// relPath affiche p relatif à la racine du projet quand il y est.
func (m Model) relPath(p string) string {
	if root := m.opts.ProjectRoot; root != "" && filepath.IsAbs(p) {
		if r, err := filepath.Rel(root, p); err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			p = r
		}
	}
	return m.clean(p)
}

// closeRewind ferme le panneau avec un avis.
func (m *Model) closeRewind(notice string) {
	m.rw = nil
	if notice != "" {
		m.tr.notice(notice)
	}
	m.scrollBack, m.unseen = 0, false
}

func (m Model) handleRewindKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	rw := *m.rw
	m.rw = &rw
	if key.Matches(msg, keys.Interrupt) {
		return m.quit()
	}
	if rw.step == stepRunning {
		return m, nil
	}
	up, down := msg.String() == "up", msg.String() == "down"
	back := key.Matches(msg, keys.Cancel)
	enter := key.Matches(msg, keys.Submit)
	switch rw.step {
	case stepPoints:
		switch {
		case back:
			m.closeRewind("")
		case up:
			rw.cursor = max(0, rw.cursor-1)
		case down:
			rw.cursor = min(len(rw.points)-1, rw.cursor+1)
		case enter:
			m.pickPoint()
		}
	case stepMode:
		switch {
		case back:
			rw.step = stepPoints
		case up:
			rw.mcur = rw.neighbour(-1)
		case down:
			rw.mcur = rw.neighbour(1)
		case enter:
			return m.pickMode()
		}
	case stepConflicts:
		switch {
		case back:
			rw.step, rw.forced, rw.ci = stepMode, nil, 0
		case msg.String() == "y" || msg.String() == "Y":
			rw.forced = append(rw.forced, rw.confl[rw.ci])
			return m.nextConflict()
		case msg.String() == "n" || msg.String() == "N" || enter:
			return m.nextConflict()
		}
	}
	return m, nil
}

// neighbour renvoie le mode sélectionnable voisin du curseur dans le sens d.
func (r *rewindUI) neighbour(d int) int {
	for i := r.mcur + d; i >= 0 && i < len(r.modes); i += d {
		if r.modes[i].ok {
			return i
		}
	}
	return r.mcur
}

// pickPoint calcule le plan du point choisi et passe à l'aperçu.
func (m *Model) pickPoint() {
	rw := m.rw
	plan, err := m.eng.RewindPlan(m.opts.SessionID, rw.points[rw.cursor].Seq)
	if err != nil {
		m.closeRewind(i18n.T(m.lang, "tui.rewind.error", m.clean(err.Error())))
		return
	}
	acts, unrestorable := false, false
	rw.confl = nil
	for _, f := range plan.Files {
		switch f.Status {
		case checkpoint.StatusOK:
			acts = true
		case checkpoint.StatusConflict:
			acts = true
			rw.confl = append(rw.confl, f.Path)
		case checkpoint.StatusUnrestorable:
			unrestorable = true
		}
	}
	conv := plan.ConversationAvailable
	// Rien à faire : avis, sauf s'il faut montrer les fichiers non
	// restaurables (aperçu, tous les modes grisés).
	if !acts && !conv && !unrestorable {
		m.closeRewind(i18n.T(m.lang, "tui.rewind.nothing"))
		return
	}
	rw.plan = plan
	rw.modes = []rewindMode{
		{engine.RewindBoth, "tui.rewind.mode_both", acts && conv},
		{engine.RewindConversation, "tui.rewind.mode_conv", conv},
		{engine.RewindCode, "tui.rewind.mode_code", acts},
	}
	rw.mcur = 0
	for i, md := range rw.modes {
		if md.ok {
			rw.mcur = i
			break
		}
	}
	rw.step = stepMode
}

// pickMode valide le mode : conflits à interroger, sinon exécution.
func (m Model) pickMode() (tea.Model, tea.Cmd) {
	rw := m.rw
	if !rw.modes[rw.mcur].ok {
		return m, nil
	}
	rw.mode = rw.modes[rw.mcur].mode
	rw.forced, rw.ci = nil, 0
	if rw.mode != engine.RewindConversation && len(rw.confl) > 0 {
		rw.step = stepConflicts
		return m, nil
	}
	return m.execRewind()
}

func (m Model) nextConflict() (tea.Model, tea.Cmd) {
	m.rw.ci++
	if m.rw.ci >= len(m.rw.confl) {
		return m.execRewind()
	}
	return m, nil
}

// execRewind lance Rewind hors de Update.
func (m Model) execRewind() (tea.Model, tea.Cmd) {
	rw := m.rw
	rw.step = stepRunning
	eng, sid, ctx := m.eng, m.opts.SessionID, m.ctx
	seq, mode, over := rw.points[rw.cursor].Seq, rw.mode, slices.Clone(rw.forced)
	// Le plan affiché : le moteur ne touche qu'aux fichiers restés tels que
	// l'utilisateur les a vus.
	preview := rw.plan
	return m, func() tea.Msg {
		res, err := eng.Rewind(ctx, sid, seq, mode, preview, over)
		return rewindDone{res: res, err: err, mode: mode}
	}
}

// finishRewind ferme le panneau, affiche le bilan et remet le prompt.
func (m *Model) finishRewind(d rewindDone) {
	m.rw = nil
	m.scrollBack, m.unseen = 0, false
	if d.err != nil {
		m.tr.notice(i18n.T(m.lang, "tui.rewind.error", m.clean(d.err.Error())))
		return
	}
	var restored, deleted, kept, skipped, failed int
	for _, f := range d.res.Files {
		switch f.Status {
		case "restored":
			restored++
		case "deleted":
			deleted++
		case "kept":
			kept++
		case "skipped":
			skipped++
		case "failed":
			failed++
		}
	}
	m.tr.notice(i18n.T(m.lang, "tui.rewind.result", restored, deleted, kept, skipped, failed))
	for _, f := range d.res.Files {
		if f.Status == "failed" {
			m.tr.notice(i18n.T(m.lang, "tui.rewind.failed_line", m.relPath(f.Path), m.clean(f.Error)))
		}
	}
	if d.mode != engine.RewindCode && d.res.Prompt != "" {
		m.input.SetValue(d.res.Prompt)
		m.input.CursorEnd()
	}
}

// rewindLines rend le panneau.
func (m Model) rewindLines() []string {
	rw := m.rw
	var out []string
	title := func(k string) { out = append(out, m.st.perm.Render(m.wrap(i18n.T(m.lang, k)))) }
	switch rw.step {
	case stepPoints:
		title("tui.rewind.title")
		lo := max(0, min(rw.cursor-rewindRows/2, len(rw.points)-rewindRows))
		for i := lo; i < len(rw.points) && i < lo+rewindRows; i++ {
			p := rw.points[i]
			// Numéro depuis le plus ancien (#1), comme tenon rewind --list.
			line := "#" + strconv.Itoa(len(rw.points)-i) + "  " + m.clean(p.Excerpt)
			if !p.Conversation {
				line += " " + i18n.T(m.lang, "rewind.code_only")
			}
			out = append(out, m.row(line, i == rw.cursor, true))
		}
		out = append(out, m.st.notice.Render(i18n.T(m.lang, "tui.rewind.keys_points")))
	case stepMode:
		title("tui.rewind.preview")
		for i, f := range rw.plan.Files {
			if i == rewindRows {
				out = append(out, "  "+i18n.T(m.lang, "tui.rewind.more", len(rw.plan.Files)-rewindRows))
				break
			}
			out = append(out, "  "+m.fileLine(f))
		}
		if n := rw.plan.UntrackedCalls; n > 0 {
			out = append(out, m.st.notice.Render(i18n.T(m.lang, "tui.rewind.untracked", n)))
		}
		for i, md := range rw.modes {
			out = append(out, m.row(i18n.T(m.lang, md.key), i == rw.mcur, md.ok))
		}
		out = append(out, m.st.notice.Render(i18n.T(m.lang, "tui.rewind.keys_mode")))
	case stepConflicts:
		title("tui.rewind.conflicts")
		out = append(out, m.wrap(i18n.T(m.lang, "tui.rewind.overwrite", m.relPath(rw.confl[rw.ci]))))
		out = append(out, m.st.notice.Render(i18n.T(m.lang, "tui.rewind.keys_conflict")))
	default:
		out = append(out, m.st.busy.Render(i18n.T(m.lang, "tui.rewind.running")))
	}
	return out
}

// row rend une ligne de liste ; ok faux : grisée.
func (m Model) row(text string, sel, ok bool) string {
	prefix := "  "
	if sel {
		prefix = "> "
	}
	if !ok {
		return m.st.busy.Render(prefix + text)
	}
	return prefix + text
}

// fileLine rend la ligne d'aperçu d'un fichier.
func (m Model) fileLine(f checkpoint.FilePlan) string {
	p := m.relPath(f.Path)
	switch f.Status {
	case checkpoint.StatusConflict:
		return i18n.T(m.lang, "tui.rewind.conflict", p)
	case checkpoint.StatusUnrestorable:
		return i18n.T(m.lang, "tui.rewind.unrestorable", p)
	case checkpoint.StatusNoop:
		return i18n.T(m.lang, "tui.rewind.unchanged", p)
	}
	if f.Action == checkpoint.ActionDelete {
		return i18n.T(m.lang, "tui.rewind.delete", p)
	}
	return i18n.T(m.lang, "tui.rewind.restore", p)
}
