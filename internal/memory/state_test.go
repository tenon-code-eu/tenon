package memory

import (
	"os"
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
