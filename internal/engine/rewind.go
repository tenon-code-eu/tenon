package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/tenon-code-eu/tenon/internal/checkpoint"
	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/tools"
)

// RewindMode : ce qu'un rewind remet en arrière.
type RewindMode string

// Modes de rewind.
const (
	RewindCode         RewindMode = "code"
	RewindConversation RewindMode = "conversation"
	RewindBoth         RewindMode = "both"
)

var (
	// ErrTurnInProgress : un tour tient la session ; le rewind n'attend pas.
	ErrTurnInProgress = errors.New("a turn is in progress")
	// ErrConversationUnavailable : le point précède la dernière compaction.
	ErrConversationUnavailable = errors.New("conversation cannot be rewound past a compaction")
)

// Statuts d'un fichier dans un Rewound.
const (
	rewoundRestored  = "restored"
	rewoundDeleted   = "deleted"
	rewoundKept      = "kept"
	rewoundSkipped   = "skipped"
	rewoundFailed    = "failed"
	rewoundUnchanged = "unchanged"
)

// excerptRunes : longueur maximale de l'extrait d'un prompt.
const excerptRunes = 80

// hashPrefix préfixe les empreintes des ToolResult (tools.hashOf).
const hashPrefix = "sha256:"

// unreadable : empreinte d'un chemin illisible, qui ne correspond à aucune
// empreinte réelle (conflit assuré).
const unreadable = "unreadable"

// RewindResult : bilan d'un rewind.
type RewindResult struct {
	Files []event.RewoundFile
	// Prompt : texte complet du prompt choisi (modes conversation et both).
	Prompt string
	// Note : note jointe au prochain message (mode code seul).
	Note string
}

// rewindState est l'état de rewind d'une session, alimenté en direct et par
// Restore à l'identique. mu protège st et prompts : RewindPoints les lit
// sans attendre la fin d'un tour.
type rewindState struct {
	mu      sync.Mutex
	st      checkpoint.State
	prompts map[uint64]string // seq du point -> texte complet du prompt
}

// addPoint enregistre un UserMessage principal accepté ; histLen est la
// longueur de l'historique juste avant le message.
func (r *rewindState) addPoint(seq uint64, prompt string, histLen int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.prompts == nil {
		r.prompts = map[uint64]string{}
	}
	r.prompts[seq] = prompt
	r.st.Points = append(r.st.Points, checkpoint.Point{
		Seq: seq, Excerpt: excerpt(prompt), HistoryLen: histLen, Conversation: true,
	})
}

// compacted : une compaction de l'agent principal rend les points antérieurs
// non rembobinables pour la conversation.
func (r *rewindState) compacted() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.st.Points {
		r.st.Points[i].Conversation = false
	}
}

// addResult enregistre l'écriture ou l'appel non annulable d'un ToolResult
// (agent principal et sous-agents).
func (r *rewindState) addResult(seq uint64, p event.ToolResultPayload) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p.Path != "" && p.HashAfter != "" {
		r.st.Writes = append(r.st.Writes, checkpoint.Write{
			Seq: seq, Path: p.Path, Before: p.HashBefore, After: p.HashAfter, NoCheckpoint: p.NoCheckpoint,
		})
	}
	if p.Untracked {
		r.st.Untracked = append(r.st.Untracked, checkpoint.Untracked{Seq: seq, Tool: p.Tool})
	}
}

// addRewound enregistre un rewind : chaque fichier restauré ou supprimé
// compte comme une écriture. Un rewind de la conversation rend les points
// postérieurs à to_seq non rembobinables pour la conversation : leur
// longueur d'historique ne désigne plus l'historique courant.
func (r *rewindState) addRewound(seq uint64, p event.RewoundPayload) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, f := range p.Files {
		var after string
		switch f.Status {
		case rewoundRestored:
			after = f.Hash
		case rewoundDeleted:
		default:
			continue
		}
		before := ""
		for i := len(r.st.Writes) - 1; i >= 0; i-- {
			if r.st.Writes[i].Path == f.Path {
				before = r.st.Writes[i].After
				break
			}
		}
		r.st.Writes = append(r.st.Writes, checkpoint.Write{Seq: seq, Path: f.Path, Before: before, After: after})
	}
	if RewindMode(p.Mode) != RewindCode {
		for i := range r.st.Points {
			if r.st.Points[i].Seq > p.ToSeq {
				r.st.Points[i].Conversation = false
			}
		}
	}
}

// snapshot copie l'état (lecture cohérente hors du verrou).
func (r *rewindState) snapshot() checkpoint.State {
	r.mu.Lock()
	defer r.mu.Unlock()
	return checkpoint.State{
		Points:    append([]checkpoint.Point(nil), r.st.Points...),
		Writes:    append([]checkpoint.Write(nil), r.st.Writes...),
		Untracked: append([]checkpoint.Untracked(nil), r.st.Untracked...),
	}
}

func (r *rewindState) prompt(seq uint64) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.prompts[seq]
}

// point renvoie le point seq.
func (r *rewindState) point(seq uint64) (checkpoint.Point, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.st.Points {
		if p.Seq == seq {
			return p, true
		}
	}
	return checkpoint.Point{}, false
}

// excerpt : première ligne du prompt, coupée à 80 runes.
func excerpt(prompt string) string {
	line, _, _ := strings.Cut(prompt, "\n")
	line = strings.TrimSuffix(line, "\r")
	if r := []rune(line); len(r) > excerptRunes {
		line = string(r[:excerptRunes])
	}
	return line
}

// rewindNote : note jointe au prochain message après un rewind code seul.
func rewindNote(excerpt string) string {
	return `[tenon] The user restored files to their state before: "` + excerpt + `"`
}

// isUntracked : un appel exécuté de shell, ou d'un outil MCP de risque
// write ou exec, peut modifier des fichiers sans copie.
func isUntracked(name string, tool tools.Tool) bool {
	if name == "shell" {
		return true
	}
	if !strings.HasPrefix(name, "mcp__") || tool == nil {
		return false
	}
	r := tool.Risk()
	return r == tools.Write || r == tools.Exec
}

// lookup renvoie la session sans la créer.
func (e *Engine) lookup(id string) *session {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.sessions[id]
}

// RewindPoints renvoie les points de la session, du plus récent au plus
// ancien (nil pour une session inconnue). N'attend pas la fin d'un tour.
func (e *Engine) RewindPoints(sessionID string) []checkpoint.Point {
	s := e.lookup(sessionID)
	if s == nil {
		return nil
	}
	pts := s.rw.snapshot().Points
	for i, j := 0, len(pts)-1; i < j; i, j = i+1, j-1 {
		pts[i], pts[j] = pts[j], pts[i]
	}
	return pts
}

// RewindPlan calcule, sans rien modifier, le plan d'un rewind vers le point
// toSeq. ErrTurnInProgress si un tour tient la session.
func (e *Engine) RewindPlan(sessionID string, toSeq uint64) (checkpoint.Plan, error) {
	s := e.lookup(sessionID)
	if s == nil {
		return checkpoint.Plan{}, checkpoint.ErrUnknownPoint
	}
	if !s.turn.TryLock() {
		return checkpoint.Plan{}, ErrTurnInProgress
	}
	defer s.turn.Unlock()
	plan, _, err := e.plan(s, toSeq)
	return plan, err
}

// plan calcule le plan sur l'état disque courant ; seen garde l'état observé
// de chaque chemin, revérifié juste avant d'agir. s.turn tenu.
func (e *Engine) plan(s *session, toSeq uint64) (checkpoint.Plan, map[string]checkpoint.FileState, error) {
	seen := map[string]checkpoint.FileState{}
	current := func(path string) checkpoint.FileState {
		st := fileState(path)
		seen[path] = st
		return st
	}
	plan, err := checkpoint.BuildPlan(s.rw.snapshot(), toSeq, current, e.hasCopy)
	return plan, seen, err
}

// hasCopy : la copie de hash ("sha256:<hex>") existe et est intacte.
func (e *Engine) hasCopy(hash string) bool {
	h, ok := strings.CutPrefix(hash, hashPrefix)
	return ok && e.d.Checkpoints != nil && e.d.Checkpoints.Has(h)
}

// fileState lit l'état d'un chemin sans suivre de lien symbolique.
func fileState(path string) checkpoint.FileState {
	fi, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return checkpoint.FileState{}
	}
	if err != nil {
		return checkpoint.FileState{Exists: true, Hash: unreadable}
	}
	if fi.Mode()&fs.ModeSymlink != 0 {
		return checkpoint.FileState{Exists: true, Symlink: true}
	}
	if !fi.Mode().IsRegular() {
		return checkpoint.FileState{Exists: true, Hash: unreadable}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return checkpoint.FileState{Exists: true, Hash: unreadable}
	}
	sum := sha256.Sum256(b)
	return checkpoint.FileState{Exists: true, Hash: hashPrefix + hex.EncodeToString(sum[:])}
}

// Rewind remet la session au point toSeq selon mode. Seuls les fichiers du
// plan sont touchés : les fichiers ok, et les conflits dont le chemin figure
// dans overwrite. Un échec sur un fichier n'arrête pas les autres. Le
// Rewound est émis en dernier.
func (e *Engine) Rewind(ctx context.Context, sessionID string, toSeq uint64, mode RewindMode, overwrite []string) (RewindResult, error) {
	switch mode {
	case RewindCode, RewindConversation, RewindBoth:
	default:
		return RewindResult{}, fmt.Errorf("unknown rewind mode: %q", mode)
	}
	if err := ctx.Err(); err != nil {
		return RewindResult{}, err
	}
	s := e.lookup(sessionID)
	if s == nil {
		return RewindResult{}, checkpoint.ErrUnknownPoint
	}
	if !s.turn.TryLock() {
		return RewindResult{}, ErrTurnInProgress
	}
	defer s.turn.Unlock()
	if e.isFailed() {
		return RewindResult{}, ErrAuditFailure
	}
	plan, seen, err := e.plan(s, toSeq)
	if err != nil {
		return RewindResult{}, err
	}
	conversation := mode != RewindCode
	if conversation && !plan.ConversationAvailable {
		return RewindResult{}, ErrConversationUnavailable
	}
	if conversation && plan.Point.HistoryLen > len(s.history) {
		return RewindResult{}, fmt.Errorf("checkpoint %d: history length %d out of range", toSeq, plan.Point.HistoryLen)
	}

	var res RewindResult
	if mode != RewindConversation {
		chosen := map[string]bool{}
		for _, p := range overwrite {
			chosen[p] = true
		}
		for _, fp := range plan.Files {
			res.Files = append(res.Files, e.rewindFile(fp, seen[fp.Path], chosen[fp.Path]))
		}
	}
	if conversation {
		res.Prompt = s.rw.prompt(toSeq)
	} else {
		res.Note = rewindNote(plan.Point.Excerpt)
	}

	payload := event.RewoundPayload{
		ToSeq: toSeq, Mode: string(mode), Files: res.Files, UntrackedCalls: plan.UntrackedCalls, Note: res.Note,
	}
	seq, err := e.emitSeq(s, event.Rewound, payload)
	if err != nil {
		return res, err
	}
	s.applyRewound(seq, payload)
	return res, nil
}

// applyRewound applique un Rewound émis ou relu : état de rewind,
// historique tronqué (modes conversation) ou note en attente (code seul).
// La longueur d'historique a été vérifiée par l'appelant.
func (s *session) applyRewound(seq uint64, p event.RewoundPayload) {
	if RewindMode(p.Mode) == RewindCode {
		s.startCtx = joinContext(s.startCtx, p.Note)
	} else if pt, ok := s.rw.point(p.ToSeq); ok {
		s.history = append(s.history[:0:0], s.history[:pt.HistoryLen]...)
	}
	s.rw.addRewound(seq, p)
}

// rewindFile exécute l'action du plan pour un fichier. was est l'état
// observé au calcul du plan ; overwrite : l'utilisateur a choisi d'écraser
// un conflit.
func (e *Engine) rewindFile(fp checkpoint.FilePlan, was checkpoint.FileState, overwrite bool) event.RewoundFile {
	out := event.RewoundFile{Path: fp.Path, Action: fp.Action}
	switch fp.Status {
	case checkpoint.StatusNoop:
		out.Status = rewoundUnchanged
		return out
	case checkpoint.StatusUnrestorable:
		out.Status = rewoundSkipped
		return out
	case checkpoint.StatusConflict:
		if !overwrite {
			out.Status = rewoundKept
			return out
		}
	}
	fail := func(err error) event.RewoundFile {
		out.Status = rewoundFailed
		out.Error = err.Error()
		return out
	}
	// Le fichier a pu changer depuis le calcul du plan : on ne touche qu'à
	// l'état que l'utilisateur a vu (ou accepté d'écraser).
	now := fileState(fp.Path)
	if now.Symlink {
		return fail(errors.New("path became a symbolic link"))
	}
	if now != was {
		return fail(errors.New("file changed during rewind"))
	}
	if fp.Action == checkpoint.ActionDelete {
		if err := removeFile(fp.Path); err != nil {
			return fail(err)
		}
		out.Status = rewoundDeleted
		return out
	}
	if e.d.Checkpoints == nil {
		return fail(checkpoint.ErrMissing)
	}
	data, err := e.d.Checkpoints.Load(strings.TrimPrefix(fp.Target, hashPrefix))
	if err != nil {
		return fail(err)
	}
	if err := restoreFile(fp.Path, data); err != nil {
		return fail(err)
	}
	out.Status = rewoundRestored
	out.Hash = fp.Target
	return out
}

// removeFile supprime un fichier ordinaire ; absent : rien à faire.
func removeFile(path string) error {
	fi, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return errors.New("not a regular file")
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// restoreFile écrit data à path par un fichier temporaire du même dossier
// puis un renommage (jamais à travers un lien symbolique) ; droits du
// fichier existant, sinon 0644 ; dossiers parents créés en 0755.
func restoreFile(path string, data []byte) error {
	mode := fs.FileMode(0o644)
	fi, err := os.Lstat(path)
	switch {
	case err == nil && fi.Mode()&fs.ModeSymlink != 0:
		return errors.New("path became a symbolic link")
	case err == nil && !fi.Mode().IsRegular():
		return errors.New("not a regular file")
	case err == nil:
		mode = fi.Mode().Perm()
	case !errors.Is(err, fs.ErrNotExist):
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".tenon-rewind-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	fail := func(err error) error {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Chmod(mode); err != nil {
		return fail(err)
	}
	if _, err := f.Write(data); err != nil {
		return fail(err)
	}
	if err := f.Sync(); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
