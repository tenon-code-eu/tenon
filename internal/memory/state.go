package memory

import (
	"os"
	"path/filepath"
	"time"
)

const defaultRetentionDays = 90

// row est l'état d'une entrée user ou global.
type row struct {
	LastUsed time.Time `json:"last_used,omitzero"`
	Expires  time.Time `json:"expires"`
}

func (s *Store) statePath() string { return filepath.Join(s.data, "memory", "state.json") }

// rowKey : <portée>/<clé projet ou global>/<id>, sans collision entre projets.
func (s *Store) rowKey(e Entry) string {
	k := "global"
	if e.Scope == User {
		k = ProjectKey(s.root)
	}
	return string(e.Scope) + "/" + k + "/" + e.ID
}

func (s *Store) loadState() map[string]row {
	m := map[string]row{}
	readJSON(s.statePath(), &m)
	if m == nil {
		m = map[string]row{}
	}
	return m
}

func (s *Store) saveState(m map[string]row) error { return writeJSON(s.statePath(), m) }

// expiry : échéance de la ligne, sinon création + rétention.
func (s *Store) expiry(m map[string]row, e Entry) time.Time {
	if r, ok := m[s.rowKey(e)]; ok && !r.Expires.IsZero() {
		return r.Expires
	}
	return e.Created.Add(s.retention)
}

// setExpiry pose expires = maintenant + rétention (user et global).
func (s *Store) setExpiry(e Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.loadState()
	r := m[s.rowKey(e)]
	r.Expires = s.now().UTC().Add(s.retention)
	m[s.rowKey(e)] = r
	return s.saveState(m)
}

func (s *Store) dropRow(e Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.loadState()
	if _, ok := m[s.rowKey(e)]; !ok {
		return nil
	}
	delete(m, s.rowKey(e))
	return s.saveState(m)
}

// Touch marque l'entrée utilisée et repousse son échéance ; sans effet pour
// project.
func (s *Store) Touch(e Entry) error {
	if e.Scope == Project {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.loadState()
	now := s.now().UTC()
	m[s.rowKey(e)] = row{LastUsed: now, Expires: now.Add(s.retention)}
	return s.saveState(m)
}

// Purge supprime les entrées user et global échues, avec leurs lignes d'état,
// et les renvoie. Les entrées project ne sont jamais purgées.
func (s *Store) Purge() ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.loadState()
	now := s.now().UTC()
	var gone []Entry
	changed := false
	// flush enregistre l'état des suppressions déjà faites avant une erreur.
	flush := func(err error) ([]Entry, error) {
		if changed {
			if serr := s.saveState(m); serr != nil && err == nil {
				err = serr
			}
		}
		return gone, err
	}
	for _, sc := range []Scope{User, Global} {
		part, _ := s.listScope(sc)
		for _, e := range part {
			if now.Before(s.expiry(m, e)) {
				continue
			}
			err := os.Remove(filepath.Join(s.dir(sc), e.ID+".md"))
			if err != nil && !os.IsNotExist(err) {
				return flush(err)
			}
			delete(m, s.rowKey(e))
			changed = true
			gone = append(gone, e)
		}
	}
	return flush(nil)
}
