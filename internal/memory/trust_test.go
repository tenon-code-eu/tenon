package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTrustOwnWrite(t *testing.T) {
	s, _, _ := newStore(t, true)
	e := put(t, s, Project, "a")
	if !s.Trusted(e) {
		t.Fatal("écriture locale non fiable")
	}
	got, _ := s.Get(Project, "a")
	if !s.Trusted(got) {
		t.Fatal("relecture non fiable")
	}
}

func TestTrustForeign(t *testing.T) {
	s, root, _ := newStore(t, true)
	e := put(t, s, Project, "a")
	// Fichier ajouté à la main : jamais enregistré.
	p := filepath.Join(root, ".tenon", "memory", "b.md")
	data, _ := os.ReadFile(filepath.Join(root, ".tenon", "memory", "a.md"))
	os.WriteFile(p, []byte(strings.Replace(string(data), "id: a", "id: b", 1)), 0o644)
	b := mustGet(t, s, Project, "b")
	if s.Trusted(b) {
		t.Fatal("fichier étranger fiable")
	}
	if err := s.Trust(b); err != nil {
		t.Fatal(err)
	}
	if !s.Trusted(mustGet(t, s, Project, "b")) {
		t.Fatal("Trust sans effet")
	}
	// Modification à la main d'une entrée fiable.
	pa := filepath.Join(root, ".tenon", "memory", "a.md")
	os.WriteFile(pa, append(data, []byte(" edited")...), 0o644)
	if s.Trusted(mustGet(t, s, Project, "a")) {
		t.Fatal("entrée éditée fiable")
	}
	_ = e
}

func mustGet(t *testing.T, s *Store, sc Scope, id string) Entry {
	t.Helper()
	e, err := s.Get(sc, id)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestTrustUserAlways(t *testing.T) {
	s, _, _ := newStore(t, true)
	for _, sc := range []Scope{User, Global} {
		if !s.Trusted(Entry{Scope: sc, ID: "x"}) {
			t.Fatal(sc)
		}
	}
}

func TestTrustSeparatedPerProject(t *testing.T) {
	s, _, data := newStore(t, true)
	e := put(t, s, Project, "a")
	other := Open(Options{Root: t.TempDir(), DataDir: data, Project: true})
	if other.Trusted(e) {
		t.Fatal("confiance partagée entre projets")
	}
}

func TestTrustRefusals(t *testing.T) {
	s, _, data := newStore(t, true)
	if err := s.Trust(Entry{Scope: User, ID: "x", SHA256: "ab"}); err == nil {
		t.Fatal("user accepté")
	}
	if err := s.Trust(Entry{Scope: Project, ID: "x"}); err == nil {
		t.Fatal("hash vide accepté")
	}
	if _, err := os.Stat(filepath.Join(data, "memory", "trust")); err == nil {
		t.Fatal("ligne écrite")
	}
}

func TestForgetProjectDropsTrust(t *testing.T) {
	s, _, _ := newStore(t, true)
	put(t, s, Project, "a")
	if err := s.Forget(Project, "a"); err != nil {
		t.Fatal(err)
	}
	if len(s.loadTrust()) != 0 {
		t.Fatal("ligne de confiance restante")
	}
}
