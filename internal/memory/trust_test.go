package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// foreignProject dépose à la main une entrée project d'id id (non fiable).
func foreignProject(t *testing.T, s *Store, root, id string) {
	t.Helper()
	dir := filepath.Join(root, ".tenon", "memory")
	os.MkdirAll(dir, 0o755)
	body := "---\nid: " + id + "\ntitle: from the repo\nsensitive: false\nauthor: model\ncreated: 2026-10-01T00:00:00Z\n---\nrepo text\n"
	if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// R17 : deux stores qui approuvent en même temps ne perdent aucune ligne.
func TestTrustConcurrentStores(t *testing.T) {
	data, root := t.TempDir(), t.TempDir()
	a := Open(Options{Root: root, DataDir: data, Project: true})
	b := Open(Options{Root: root, DataDir: data, Project: true})
	const n = 40
	var wg sync.WaitGroup
	for i, s := range []*Store{a, b} {
		for j := range n {
			wg.Add(1)
			go func() {
				defer wg.Done()
				e := Entry{Scope: Project, ID: fmt.Sprintf("s%d-%d", i, j), SHA256: "ab"}
				if err := s.Trust(e); err != nil {
					t.Error(err)
				}
			}()
		}
	}
	wg.Wait()
	if got := len(a.loadTrust()); got != 2*n {
		t.Fatalf("lignes de confiance = %d, attendu %d", got, 2*n)
	}
}

// TrustedFunc lit le fichier de confiance une fois : l'instantané ne voit
// pas une approbation ultérieure.
func TestTrustedFuncSnapshot(t *testing.T) {
	s, root, _ := newStore(t, true)
	put(t, s, Project, "mine")
	foreignProject(t, s, root, "repo")
	trusted := s.TrustedFunc()
	mine, repo := mustGet(t, s, Project, "mine"), mustGet(t, s, Project, "repo")
	if !trusted(mine) || trusted(repo) || !trusted(Entry{Scope: User, ID: "u"}) {
		t.Fatal("instantané faux")
	}
	if err := s.Trust(repo); err != nil {
		t.Fatal(err)
	}
	if trusted(repo) {
		t.Fatal("l'instantané relit le fichier de confiance")
	}
	if !s.TrustedFunc()(repo) {
		t.Fatal("nouvel instantané sans l'approbation")
	}
}
