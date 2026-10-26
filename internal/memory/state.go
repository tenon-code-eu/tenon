package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
	"time"
)

const defaultRetentionDays = 90

// projectKeyDir reconnaît un dossier de clé de projet sous <data>/memory.
var projectKeyDir = regexp.MustCompile(`^[0-9a-f]{16}$`)

// row est l'état d'une entrée user ou global.
type row struct {
	LastUsed time.Time `json:"last_used,omitzero"`
	Expires  time.Time `json:"expires"`
}

func (s *Store) statePath() string { return filepath.Join(s.data, "memory", "state.json") }

// locked exécute fn sous le verrou du processus et sous un flock exclusif
// sur <data>/memory/.lock : les lectures-modifications-écritures de
// state.json et des fichiers de confiance ne se perdent pas entre deux
// processus tenon.
func (s *Store) locked(fn func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := filepath.Join(s.data, "memory")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	lk, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("memory lock: %w", err)
	}
	defer lk.Close()
	if err := syscall.Flock(int(lk.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("memory lock: %w", err)
	}
	defer syscall.Flock(int(lk.Fd()), syscall.LOCK_UN)
	return fn()
}

// rowKeyFor : <portée>/<clé projet ou global>/<id>, sans collision entre
// projets ; key est la clé de projet d'une entrée user.
func rowKeyFor(sc Scope, key, id string) string {
	if sc != User {
		key = "global"
	}
	return string(sc) + "/" + key + "/" + id
}

// rowKey : ligne d'état d'une entrée du projet ouvert.
func (s *Store) rowKey(e Entry) string { return rowKeyFor(e.Scope, s.key, e.ID) }

func (s *Store) loadState() map[string]row {
	m := map[string]row{}
	readJSON(s.statePath(), &m)
	if m == nil {
		m = map[string]row{}
	}
	return m
}

func (s *Store) saveState(m map[string]row) error { return writeJSON(s.statePath(), m) }

// expiry : échéance de la ligne k, sinon création + rétention.
func (s *Store) expiry(m map[string]row, k string, e Entry) time.Time {
	if r, ok := m[k]; ok && !r.Expires.IsZero() {
		return r.Expires
	}
	return e.Created.Add(s.retention)
}

// setExpiry pose expires = maintenant + rétention (user et global).
func (s *Store) setExpiry(e Entry) error {
	return s.locked(func() error {
		m := s.loadState()
		r := m[s.rowKey(e)]
		r.Expires = s.now().UTC().Add(s.retention)
		m[s.rowKey(e)] = r
		return s.saveState(m)
	})
}

func (s *Store) dropRow(e Entry) error {
	return s.locked(func() error {
		m := s.loadState()
		if _, ok := m[s.rowKey(e)]; !ok {
			return nil
		}
		delete(m, s.rowKey(e))
		return s.saveState(m)
	})
}

// Touch marque l'entrée utilisée et repousse son échéance ; sans effet pour
// project.
func (s *Store) Touch(e Entry) error {
	if e.Scope == Project {
		return nil
	}
	return s.locked(func() error {
		m := s.loadState()
		now := s.now().UTC()
		m[s.rowKey(e)] = row{LastUsed: now, Expires: now.Add(s.retention)}
		return s.saveState(m)
	})
}

// userKeys renvoie les clés de projet présentes sous <data>/memory : les
// dossiers (liens symboliques exclus) dont le nom est 16 chiffres hex.
func (s *Store) userKeys() []string {
	des, err := os.ReadDir(filepath.Join(s.data, "memory"))
	if err != nil {
		return nil
	}
	var keys []string
	for _, de := range des {
		if de.IsDir() && projectKeyDir.MatchString(de.Name()) {
			keys = append(keys, de.Name())
		}
	}
	return keys
}

// Purge supprime les entrées user (de tous les projets : la rétention vaut
// par personne, pas par projet ouvert) et global échues, avec leurs lignes
// d'état, et les renvoie. Les entrées project ne sont jamais purgées.
func (s *Store) Purge() (gone []Entry, err error) {
	err = s.locked(func() error {
		m := s.loadState()
		now := s.now().UTC()
		changed := false
		// flush enregistre l'état des suppressions déjà faites avant une
		// erreur.
		flush := func(err error) error {
			if changed {
				if serr := s.saveState(m); serr != nil && err == nil {
					err = serr
				}
			}
			return err
		}
		type place struct {
			sc  Scope
			key string
		}
		var places []place
		for _, k := range s.userKeys() {
			places = append(places, place{User, k})
		}
		places = append(places, place{Global, ""})
		for _, p := range places {
			dir := s.dirFor(p.sc, p.key)
			part, _ := s.listDir(p.sc, dir)
			for _, e := range part {
				k := rowKeyFor(p.sc, p.key, e.ID)
				if now.Before(s.expiry(m, k, e)) {
					continue
				}
				err := os.Remove(filepath.Join(dir, e.ID+".md"))
				if err != nil && !os.IsNotExist(err) {
					return flush(err)
				}
				delete(m, k)
				changed = true
				gone = append(gone, e)
			}
		}
		return flush(nil)
	})
	return gone, err
}

// Expires renvoie l'échéance d'une entrée user ou global (ligne d'état,
// sinon création + rétention) ; zéro pour project, qui n'expire pas.
func (s *Store) Expires(e Entry) time.Time {
	if e.Scope == Project {
		return time.Time{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.expiry(s.loadState(), s.rowKey(e), e)
}
