package memory

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/tenon-code-eu/tenon/internal/safefile"
)

const maxStateBytes = 4 << 20

func (s *Store) trustPath() string {
	return filepath.Join(s.data, "memory", "trust", ProjectKey(s.root)+".json")
}

// readJSON lit un fichier JSON régulier ; absent, illisible ou corrompu
// donne une table vide, sans erreur.
func readJSON(path string, v any) {
	data, _, ok, err := safefile.ReadRegular(path, maxStateBytes)
	if err != nil || !ok {
		return
	}
	if json.Unmarshal(data, v) != nil {
		// Un fichier corrompu est traité comme vide ; la prochaine
		// écriture le remplace.
		return
	}
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return writeAtomic(path, data, 0o600)
}

func (s *Store) loadTrust() map[string]string {
	m := map[string]string{}
	readJSON(s.trustPath(), &m)
	if m == nil {
		m = map[string]string{}
	}
	return m
}

// Trusted : user et global sont toujours de confiance ; une entrée project
// l'est si le fichier de confiance détient son id avec la même empreinte.
func (s *Store) Trusted(e Entry) bool {
	if e.Scope != Project {
		return true
	}
	if e.SHA256 == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadTrust()[e.ID] == e.SHA256
}

// Trust enregistre l'empreinte d'une entrée project.
func (s *Store) Trust(e Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.loadTrust()
	m[e.ID] = e.SHA256
	return writeJSON(s.trustPath(), m)
}
