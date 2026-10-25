package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tenon-code-eu/tenon/internal/safefile"
)

// Options configure un Store. DataDir est un chemin simple (config.DataDir
// n'est pas importé ici : cycle d'imports).
type Options struct {
	Root, DataDir string
	Project       bool
	Now           func() time.Time
	// RetentionDays : durée de rétention des portées user et global (0 → 90).
	RetentionDays int
}

// Store lit et écrit les entrées des trois portées.
type Store struct {
	root, data string
	project    bool
	now        func() time.Time
	retention  time.Duration
	mu         sync.Mutex // sérialise trust.json et state.json
}

// ProjectKey renvoie les 16 premiers hex du SHA-256 de la racine absolue.
func ProjectKey(root string) string {
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	sum := sha256.Sum256([]byte(filepath.Clean(root)))
	return hex.EncodeToString(sum[:])[:16]
}

// Open construit le store ; aucun accès disque avant usage.
func Open(o Options) *Store {
	now := o.Now
	if now == nil {
		now = time.Now
	}
	root := o.Root
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	days := o.RetentionDays
	if days <= 0 {
		days = defaultRetentionDays
	}
	return &Store{root: filepath.Clean(root), data: o.DataDir, project: o.Project, now: now,
		retention: time.Duration(days) * 24 * time.Hour}
}

func (s *Store) dir(sc Scope) string {
	switch sc {
	case Project:
		return filepath.Join(s.root, ".tenon", "memory")
	case User:
		return filepath.Join(s.data, "memory", ProjectKey(s.root))
	default:
		return filepath.Join(s.data, "memory", "global")
	}
}

// projectDirState vérifie .tenon et .tenon/memory : liens symboliques ou
// sortie de la racine → mémoire projet vide. exists=false sans avis si absent.
func (s *Store) projectDirState() (exists bool, problem string) {
	tenon := filepath.Join(s.root, ".tenon")
	mem := s.dir(Project)
	for _, p := range []string{tenon, mem} {
		fi, err := os.Lstat(p)
		if errors.Is(err, fs.ErrNotExist) {
			return false, ""
		}
		if err != nil {
			return false, err.Error()
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return false, filepath.Base(p) + " is a symbolic link"
		}
		if !fi.IsDir() {
			return false, filepath.Base(p) + " is not a directory"
		}
	}
	realRoot, err1 := filepath.EvalSymlinks(s.root)
	realMem, err2 := filepath.EvalSymlinks(mem)
	if err1 != nil || err2 != nil || !strings.HasPrefix(realMem, realRoot+string(filepath.Separator)) {
		return false, "memory folder resolves outside the project"
	}
	return true, ""
}

// scopeDir renvoie le dossier à lire d'une portée (ok=false : rien à lire).
func (s *Store) scopeDir(sc Scope) (dir string, ok bool, notice string) {
	if sc == Project {
		if !s.project {
			return "", false, ""
		}
		exists, problem := s.projectDirState()
		if problem != "" {
			return "", false, fmt.Sprintf("memory: project/.tenon ignored: %s", problem)
		}
		return s.dir(sc), exists, ""
	}
	return s.dir(sc), true, ""
}

func (s *Store) readFile(sc Scope, dir, id string) (Entry, bool, error) {
	data, truncated, ok, err := safefile.ReadRegular(filepath.Join(dir, id+".md"), maxFileBytes)
	if err != nil {
		return Entry{}, false, err
	}
	if !ok {
		return Entry{}, false, nil
	}
	if truncated {
		return Entry{}, true, invalid("file too large")
	}
	e, err := parseEntry(sc, id, data)
	return e, true, err
}

// listScope renvoie les entrées valides d'une portée et les avis des
// fichiers ignorés.
func (s *Store) listScope(sc Scope) (part []Entry, notices []string) {
	dir, ok, notice := s.scopeDir(sc)
	if notice != "" {
		notices = append(notices, notice)
	}
	if !ok {
		return nil, notices
	}
	des, err := os.ReadDir(dir)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			notices = append(notices, fmt.Sprintf("memory: %s/ ignored: %v", sc, err))
		}
		return nil, notices
	}
	for _, de := range des {
		name := de.Name()
		id, isMD := strings.CutSuffix(name, ".md")
		if !isMD {
			continue
		}
		e, found, err := s.readFile(sc, dir, id)
		switch {
		case err != nil:
			// Le préfixe « memory: » est retiré pour ne pas être doublé.
			reason := strings.TrimPrefix(strings.TrimPrefix(err.Error(), ErrInvalid.Error()+": "), "memory: ")
			notices = append(notices, fmt.Sprintf("memory: %s/%s ignored: %s", sc, name, reason))
		case !found:
			notices = append(notices, fmt.Sprintf("memory: %s/%s ignored: not a regular file", sc, name))
		default:
			part = append(part, e)
		}
	}
	sort.Slice(part, func(i, j int) bool {
		if !part[i].Created.Equal(part[j].Created) {
			return part[i].Created.After(part[j].Created)
		}
		return part[i].ID < part[j].ID
	})
	return part, notices
}

// List renvoie toutes les entrées lisibles (project, user, global, puis
// created décroissant, puis id) et les avis des fichiers ignorés.
func (s *Store) List() (entries []Entry, notices []string) {
	for _, sc := range []Scope{Project, User, Global} {
		part, n := s.listScope(sc)
		entries = append(entries, part...)
		notices = append(notices, n...)
	}
	return entries, notices
}

// Get lit une entrée ; scope vide : project, user, global dans l'ordre.
func (s *Store) Get(scope Scope, id string) (Entry, error) {
	if err := validID(id); err != nil {
		return Entry{}, err
	}
	scopes := []Scope{Project, User, Global}
	if scope != "" {
		if _, err := ParseScope(string(scope)); err != nil {
			return Entry{}, err
		}
		scopes = []Scope{scope}
	}
	var firstErr error
	for _, sc := range scopes {
		dir, ok, _ := s.scopeDir(sc)
		if !ok {
			continue
		}
		e, found, err := s.readFile(sc, dir, id)
		if err != nil {
			// Un fichier cassé d'une portée ne cache pas une entrée valide
			// d'une portée suivante.
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if found {
			return e, nil
		}
	}
	if firstErr != nil {
		return Entry{}, firstErr
	}
	return Entry{}, ErrNotFound
}

// Put valide, encode et écrit l'entrée de façon atomique.
func (s *Store) Put(e Entry) (Entry, error) {
	if _, err := ParseScope(string(e.Scope)); err != nil {
		return Entry{}, err
	}
	if e.Scope == Project && !s.project {
		return Entry{}, invalid("project memory is disabled")
	}
	if e.Created.IsZero() {
		e.Created = s.now()
	}
	e.Created = e.Created.UTC().Truncate(time.Second)
	data, err := encodeEntry(e)
	if err != nil {
		return Entry{}, err
	}
	dir := s.dir(e.Scope)
	if e.Scope == Project {
		if _, problem := s.projectDirState(); problem != "" {
			return Entry{}, invalid("project memory unusable: %s", problem)
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Entry{}, err
	}
	target := filepath.Join(dir, e.ID+".md")
	if _, err := os.Lstat(target); errors.Is(err, fs.ErrNotExist) {
		if part, _ := s.listScope(e.Scope); len(part) >= maxEntries {
			return Entry{}, fullError{e.Scope}
		}
	}
	mode := os.FileMode(0o600)
	if e.Scope == Project {
		mode = 0o644
	}
	if err := writeAtomic(target, data, mode); err != nil {
		return Entry{}, err
	}
	sum := sha256.Sum256(data)
	e.SHA256 = hex.EncodeToString(sum[:])
	if e.Scope == Project {
		// Une écriture locale est de confiance : on enregistre l'empreinte.
		if err := s.Trust(e); err != nil {
			return Entry{}, err
		}
	} else if err := s.setExpiry(e); err != nil {
		return Entry{}, err
	}
	return e, nil
}

// Path renvoie le chemin absolu du fichier d'une entrée (sans le lire).
func (s *Store) Path(sc Scope, id string) string {
	return filepath.Join(s.dir(sc), id+".md")
}

// Check vérifie, sans rien écrire, qu'une entrée passerait Put : portée,
// champs, taille du corps, et plafond de la portée pour une nouvelle entrée.
func (s *Store) Check(e Entry) error {
	if _, err := ParseScope(string(e.Scope)); err != nil {
		return err
	}
	if e.Scope == Project && !s.project {
		return invalid("project memory is disabled")
	}
	if err := e.validate(); err != nil {
		return err
	}
	if _, err := os.Lstat(s.Path(e.Scope, e.ID)); errors.Is(err, fs.ErrNotExist) {
		if part, _ := s.listScope(e.Scope); len(part) >= maxEntries {
			return fullError{e.Scope}
		}
	}
	return nil
}

// writeAtomic écrit dans un fichier temporaire du même dossier puis renomme.
func writeAtomic(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	fail := func(err error) error {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(err)
	}
	if err := tmp.Chmod(mode); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

// Forget supprime une entrée (l'effacement reste possible même si la
// portée projet est désactivée, sauf dossier mémoire douteux).
func (s *Store) Forget(scope Scope, id string) error {
	if err := validID(id); err != nil {
		return err
	}
	if _, err := ParseScope(string(scope)); err != nil {
		return err
	}
	if scope == Project {
		if exists, problem := s.projectDirState(); problem != "" || !exists {
			return ErrNotFound
		}
	}
	err := os.Remove(filepath.Join(s.dir(scope), id+".md"))
	if errors.Is(err, fs.ErrNotExist) {
		return ErrNotFound
	}
	if err == nil {
		if scope == Project {
			_ = s.dropTrust(id)
		} else {
			_ = s.dropRow(Entry{Scope: scope, ID: id})
		}
	}
	return err
}
