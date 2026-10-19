package checkpoint

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func open(t *testing.T) (*Store, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "checkpoints")
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return s, dir
}

func TestMaxBytes(t *testing.T) {
	if MaxBytes != 10<<20 {
		t.Fatalf("MaxBytes = %d", MaxBytes)
	}
}

func TestDir(t *testing.T) {
	g := func(k string) string {
		if k == "XDG_DATA_HOME" {
			return "/x"
		}
		return ""
	}
	if got := Dir(g); got != filepath.Join("/x", "tenon", "checkpoints") {
		t.Fatalf("Dir = %q", got)
	}
}

func TestSaveLoad(t *testing.T) {
	s, dir := open(t)
	data := []byte("bonjour")
	h, err := s.Save(data)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Load(h)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("Load = %q, %v", got, err)
	}
	if !s.Has(h) {
		t.Fatal("Has = false")
	}
	p := filepath.Join(dir, h[:2], h)
	if len(h) != 64 {
		t.Fatalf("hash = %q", h)
	}
	fi, err := os.Stat(p)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("fichier : %v, %v", fi, err)
	}
	for _, d := range []string{dir, filepath.Join(dir, h[:2])} {
		di, err := os.Stat(d)
		if err != nil || di.Mode().Perm() != 0o700 {
			t.Fatalf("dir %s : %v, %v", d, di, err)
		}
	}
}

func TestSaveDedup(t *testing.T) {
	s, dir := open(t)
	h, _ := s.Save([]byte("a"))
	p := filepath.Join(dir, h[:2], h)
	old := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save([]byte("a")); err != nil {
		t.Fatal(err)
	}
	// Une copie de nouveau référencée est rafraîchie : Prune ne la supprime
	// pas entre sa déduplication et l'écriture du ToolResult.
	fi, _ := os.Stat(p)
	if time.Since(fi.ModTime()) > time.Hour {
		t.Fatalf("mtime non rafraîchi : %v", fi.ModTime())
	}
}

func TestSaveTooLarge(t *testing.T) {
	s, dir := open(t)
	_, err := s.Save(make([]byte, MaxBytes+1))
	if err == nil || !strings.Contains(err.Error(), "file too large") {
		t.Fatalf("err = %v", err)
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 0 {
		t.Fatalf("écrit malgré l'erreur : %v", ents)
	}
	if _, err := s.Save(make([]byte, MaxBytes)); err != nil {
		t.Fatal(err)
	}
}

func TestLoadCorrupted(t *testing.T) {
	s, dir := open(t)
	h, _ := s.Save([]byte("a"))
	if err := os.WriteFile(filepath.Join(dir, h[:2], h), []byte("b"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(h); !errors.Is(err, ErrCorrupted) {
		t.Fatalf("err = %v", err)
	}
	if s.Has(h) {
		t.Fatal("Has = true sur copie corrompue")
	}
}

func TestLoadMissing(t *testing.T) {
	s, _ := open(t)
	h := strings.Repeat("ab", 32)
	if _, err := s.Load(h); !errors.Is(err, ErrMissing) {
		t.Fatalf("err = %v", err)
	}
	if s.Has(h) {
		t.Fatal("Has = true")
	}
	// Hachage mal formé : jamais un chemin.
	if _, err := s.Load("../../etc/passwd"); !errors.Is(err, ErrMissing) {
		t.Fatalf("err = %v", err)
	}
}

func TestSaveAtomic(t *testing.T) {
	s, dir := open(t)
	h, _ := s.Save([]byte("a"))
	ents, _ := os.ReadDir(filepath.Join(dir, h[:2]))
	if len(ents) != 1 || ents[0].Name() != h {
		t.Fatalf("contenu : %v", ents)
	}
}

func TestPrune(t *testing.T) {
	s, dir := open(t)
	now := time.Now()
	old := now.Add(-40 * 24 * time.Hour)
	age := func(h string, mt time.Time) {
		if err := os.Chtimes(filepath.Join(dir, h[:2], h), mt, mt); err != nil {
			t.Fatal(err)
		}
	}
	hOld, _ := s.Save([]byte("old"))
	hRef, _ := s.Save([]byte("ref"))
	hNew, _ := s.Save([]byte("new"))
	age(hOld, old)
	age(hRef, old)
	tmp := filepath.Join(dir, hOld[:2], ".tmp-123")
	if err := os.WriteFile(tmp, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(tmp, old, old)
	n, err := s.Prune(30*24*time.Hour, map[string]bool{hRef: true}, now)
	if err != nil || n != 2 {
		t.Fatalf("removed = %d, err = %v", n, err)
	}
	if s.Has(hOld) || !s.Has(hRef) || !s.Has(hNew) {
		t.Fatalf("old=%v ref=%v new=%v", s.Has(hOld), s.Has(hRef), s.Has(hNew))
	}
	if _, err := os.Stat(tmp); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("temp ancien conservé")
	}
}
