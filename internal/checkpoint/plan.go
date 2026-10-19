package checkpoint

import (
	"errors"
	"sort"
)

// ErrUnknownPoint : aucun point de contrôle pour ce seq.
var ErrUnknownPoint = errors.New("unknown checkpoint")

// Actions et états d'un fichier du plan.
const (
	ActionRestore = "restore"
	ActionDelete  = "delete"

	StatusOK           = "ok"
	StatusConflict     = "conflict"
	StatusUnrestorable = "unrestorable"
	StatusNoop         = "noop"
)

// Point : un message utilisateur auquel on peut revenir.
type Point struct {
	Seq          uint64
	Excerpt      string
	HistoryLen   int
	Conversation bool
}

// Write : une écriture de fichier enregistrée. After vide = fichier absent
// après l'événement (suppression par un rewind enregistré).
type Write struct {
	Seq          uint64
	Path         string
	Before       string
	After        string
	NoCheckpoint bool
}

// Untracked : un appel non annulable (shell, MCP...).
type Untracked struct {
	Seq  uint64
	Tool string
}

// State : l'état en mémoire d'une session, dans l'ordre des seq.
type State struct {
	Points    []Point
	Writes    []Write
	Untracked []Untracked
}

// FileState : l'état courant d'un chemin sur le disque.
type FileState struct {
	Exists  bool
	Symlink bool
	Hash    string
}

// Unreadable : empreinte d'un chemin illisible ou non ordinaire, qui ne
// correspond à aucune empreinte réelle (conflit assuré).
const Unreadable = "unreadable"

// symlinkKey : clé d'état d'un lien symbolique.
const symlinkKey = "symlink"

// Key résume l'état : "" absent, "symlink" pour un lien symbolique,
// "unreadable" pour un chemin illisible, sinon l'empreinte "sha256:<hex>".
func (s FileState) Key() string {
	switch {
	case s.Symlink:
		return symlinkKey
	case !s.Exists:
		return ""
	}
	return s.Hash
}

// FilePlan : ce que /rewind ferait pour un fichier.
type FilePlan struct {
	Path     string
	Action   string
	Status   string
	Target   string
	Expected string
	// Current : état observé au calcul du plan (FileState.Key) ; Rewind
	// refuse d'agir si le fichier a changé depuis l'aperçu.
	Current string
}

// Plan : résultat de BuildPlan.
type Plan struct {
	Point                 Point
	Files                 []FilePlan
	UntrackedCalls        int
	ConversationAvailable bool
}

// BuildPlan calcule, sans rien modifier, les actions pour revenir au point
// toSeq. current et has sont injectés : aucune lecture disque ici.
func BuildPlan(st State, toSeq uint64, current func(path string) FileState, has func(hash string) bool) (Plan, error) {
	var plan Plan
	found := false
	for _, p := range st.Points {
		if p.Seq == toSeq {
			plan.Point, found = p, true
			break
		}
	}
	if !found {
		return Plan{}, ErrUnknownPoint
	}
	plan.ConversationAvailable = plan.Point.Conversation
	for _, u := range st.Untracked {
		if u.Seq > toSeq {
			plan.UntrackedCalls++
		}
	}

	type acc struct {
		target, expected string
		noCheckpoint     bool
		touched          bool
	}
	byPath := map[string]*acc{}
	for _, w := range st.Writes {
		a := byPath[w.Path]
		if a == nil {
			a = &acc{}
			byPath[w.Path] = a
		}
		// Attendu : After de la dernière écriture, tous seq confondus.
		a.expected = w.After
		if w.Seq > toSeq {
			if !a.touched {
				a.target = w.Before
				a.touched = true
			}
			if w.NoCheckpoint {
				a.noCheckpoint = true
			}
		}
	}

	for path, a := range byPath {
		if !a.touched {
			continue
		}
		fp := FilePlan{Path: path, Target: a.target, Expected: a.expected, Action: ActionRestore}
		if a.target == "" {
			fp.Action = ActionDelete
		}
		cur := current(path)
		fp.Current = cur.Key()
		switch {
		case matches(cur, a.target):
			fp.Status = StatusNoop
		case cur.Symlink || !matches(cur, a.expected):
			fp.Status = StatusConflict
		case fp.Action == ActionRestore && (a.noCheckpoint || !has(a.target)):
			fp.Status = StatusUnrestorable
		default:
			fp.Status = StatusOK
		}
		plan.Files = append(plan.Files, fp)
	}
	sort.Slice(plan.Files, func(i, j int) bool { return plan.Files[i].Path < plan.Files[j].Path })
	return plan, nil
}

// matches : l'état courant correspond-il au hash (vide = absent) ? Un lien
// symbolique ne correspond jamais : on ne le traite pas comme un fichier.
func matches(cur FileState, hash string) bool {
	if cur.Symlink {
		return false
	}
	if hash == "" {
		return !cur.Exists
	}
	return cur.Exists && cur.Hash == hash
}
