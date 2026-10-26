package memory

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

const day = 24 * time.Hour

func clockStore(t *testing.T, now *time.Time, root, data string) *Store {
	return Open(Options{Root: root, DataDir: data, Project: true, Now: func() time.Time { return *now }})
}

func TestRetentionDefault(t *testing.T) {
	now := t0
	root, data := t.TempDir(), t.TempDir()
	s := clockStore(t, &now, root, data)
	e := put(t, s, User, "a")
	// Sans ligne d'état : création + 90 jours.
	os.Remove(s.statePath())
	now = t0.Add(89 * day)
	if g, _ := s.Purge(); len(g) != 0 {
		t.Fatal("purgée trop tôt")
	}
	now = t0.Add(90*day + time.Second)
	if g, _ := s.Purge(); len(g) != 1 || g[0].ID != e.ID {
		t.Fatalf("non purgée: %v", g)
	}
	if o := Open(Options{Root: root, DataDir: data, RetentionDays: 5}); o.retention != 5*day {
		t.Fatal("retention custom")
	}
}

func TestPurge(t *testing.T) {
	now := t0
	root, data := t.TempDir(), t.TempDir()
	s := clockStore(t, &now, root, data)
	put(t, s, User, "u")
	put(t, s, Global, "g")
	pe := put(t, s, Project, "p")
	now = t0.Add(100 * day)
	gone, err := s.Purge()
	if err != nil || len(gone) != 2 {
		t.Fatalf("%v %v", gone, err)
	}
	if _, err := s.Get(User, "u"); err == nil {
		t.Fatal("u encore là")
	}
	if len(s.loadState()) != 0 {
		t.Fatal("lignes d'état restantes")
	}
	if _, err := s.Get(Project, "p"); err != nil || !s.Trusted(pe) {
		t.Fatal("project purgée")
	}
	// Fichier déjà disparu : la ligne tombe seule.
	put(t, s, User, "v")
	os.Remove(s.dir(User) + "/v.md")
	if _, err := s.Purge(); err != nil {
		t.Fatal(err)
	}
}

func TestTouchExtends(t *testing.T) {
	now := t0
	s := clockStore(t, &now, t.TempDir(), t.TempDir())
	e := put(t, s, User, "a")
	now = t0.Add(80 * day)
	if err := s.Touch(e); err != nil {
		t.Fatal(err)
	}
	now = t0.Add(100 * day)
	if g, _ := s.Purge(); len(g) != 0 {
		t.Fatal("touch sans effet")
	}
	now = t0.Add(171 * day)
	if g, _ := s.Purge(); len(g) != 1 {
		t.Fatal("jamais purgée")
	}
}

func TestTouchProjectNoop(t *testing.T) {
	s, _, _ := newStore(t, true)
	e := put(t, s, Project, "a")
	if err := s.Touch(e); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.statePath()); err == nil {
		t.Fatal("état écrit pour project")
	}
}

func TestStateCorruptAndCollision(t *testing.T) {
	now := t0
	data := t.TempDir()
	s1 := clockStore(t, &now, t.TempDir(), data)
	s2 := clockStore(t, &now, t.TempDir(), data)
	os.MkdirAll(data+"/memory", 0o700)
	os.WriteFile(s1.statePath(), []byte("{not json"), 0o600)
	if len(s1.loadState()) != 0 {
		t.Fatal("corrompu non vide")
	}
	put(t, s1, User, "same")
	e2 := put(t, s2, User, "same")
	if len(s1.loadState()) != 2 {
		t.Fatalf("collision: %v", s1.loadState())
	}
	now = t0.Add(80 * day)
	s2.Touch(e2)
	now = t0.Add(100 * day)
	g, _ := s1.Purge()
	if len(g) != 1 {
		t.Fatalf("s1 doit purger sa propre entrée seulement: %v", g)
	}
	if _, err := s2.Get(User, "same"); err != nil {
		t.Fatal("entrée de l'autre projet purgée")
	}
}

func TestForgetDropsRow(t *testing.T) {
	s, _, _ := newStore(t, true)
	put(t, s, User, "a")
	if err := s.Forget(User, "a"); err != nil {
		t.Fatal(err)
	}
	if len(s.loadState()) != 0 {
		t.Fatal("ligne restante")
	}
}

func TestPurgeRemoveErrorKeepsProgress(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root")
	}
	now := t0
	s := clockStore(t, &now, t.TempDir(), t.TempDir())
	put(t, s, User, "a")
	put(t, s, Global, "g")
	// Global ne peut pas être vidé : dossier sans droit d'écriture.
	os.Chmod(s.dir(Global), 0o500)
	defer os.Chmod(s.dir(Global), 0o700)
	now = t0.Add(100 * day)
	gone, err := s.Purge()
	if err == nil || len(gone) != 1 || gone[0].ID != "a" {
		t.Fatalf("%v %v", gone, err)
	}
	if _, ok := s.loadState()[s.rowKey(Entry{Scope: User, ID: "a"})]; ok {
		t.Fatal("ligne de l'entrée supprimée conservée")
	}
}

func TestExpiresExported(t *testing.T) {
	now := t0
	s := clockStore(t, &now, t.TempDir(), t.TempDir())
	u := put(t, s, User, "u")
	if got := s.Expires(u); !got.Equal(t0.Add(90 * day)) {
		t.Fatalf("expires user = %v", got)
	}
	now = t0.Add(10 * day)
	if err := s.Touch(u); err != nil {
		t.Fatal(err)
	}
	if got := s.Expires(u); !got.Equal(t0.Add(100 * day)) {
		t.Fatalf("expires après Touch = %v", got)
	}
	p := put(t, s, Project, "p")
	if got := s.Expires(p); !got.IsZero() {
		t.Fatalf("project sans échéance, obtenu %v", got)
	}
}

func TestCheckID(t *testing.T) {
	if CheckID("go-style") != nil {
		t.Fatal("id valide refusé")
	}
	for _, id := range []string{"", "-a", "A", "../x", "a/b"} {
		if CheckID(id) == nil {
			t.Fatalf("id %q accepté", id)
		}
	}
}

// R12 : la rétention vaut par personne, pas par projet ouvert ; une entrée
// user d'un projet A expire quand un projet B purge.
func TestPurgeOtherProjects(t *testing.T) {
	now := t0
	data := t.TempDir()
	a := clockStore(t, &now, t.TempDir(), data)
	b := clockStore(t, &now, t.TempDir(), data)
	put(t, a, User, "old")
	keep := put(t, a, User, "kept")
	now = t0.Add(80 * day)
	if err := a.Touch(keep); err != nil {
		t.Fatal(err)
	}
	// Dossiers voisins qui ne sont pas des clés de projet : ignorés.
	os.MkdirAll(filepath.Join(data, "memory", "not-a-key"), 0o700)
	now = t0.Add(100 * day)
	gone, err := b.Purge()
	if err != nil || len(gone) != 1 || gone[0].ID != "old" || gone[0].Scope != User {
		t.Fatalf("%v %v", gone, err)
	}
	if _, err := a.Get(User, "old"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("entrée du projet A encore là: %v", err)
	}
	if _, err := a.Get(User, "kept"); err != nil {
		t.Fatal("entrée touchée purgée")
	}
	if _, ok := b.loadState()[a.rowKey(Entry{Scope: User, ID: "old"})]; ok {
		t.Fatal("ligne d'état de l'entrée purgée conservée")
	}
}

// R17 : deux stores (deux processus) qui mettent à jour state.json en même
// temps ne perdent aucune ligne.
func TestStateConcurrentStores(t *testing.T) {
	data, root := t.TempDir(), t.TempDir()
	stores := []*Store{
		Open(Options{Root: root, DataDir: data, Project: true}),
		Open(Options{Root: root, DataDir: data, Project: true}),
	}
	const n = 40
	var wg sync.WaitGroup
	for i, s := range stores {
		for j := range n {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := s.Touch(Entry{Scope: User, ID: fmt.Sprintf("s%d-%d", i, j)}); err != nil {
					t.Error(err)
				}
			}()
		}
	}
	wg.Wait()
	if got := len(stores[0].loadState()); got != 2*n {
		t.Fatalf("lignes d'état = %d, attendu %d", got, 2*n)
	}
	fi, err := os.Stat(filepath.Join(data, "memory", ".lock"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("verrou: %v %v", fi, err)
	}
}
