//go:build unix

package diff

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// Un fichier remplacé par une FIFO sans écrivain entre la vérification et
// l'ouverture ne bloque pas la lecture : elle rend la main aussitôt, à vide.
func TestReadCurrentFIFOSwapReturnsPromptly(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(p, []byte("contenu\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	prev := beforeOpen
	beforeOpen = func(path string) {
		if err := os.Remove(path); err != nil {
			t.Error(err)
		}
		if err := syscall.Mkfifo(path, 0o600); err != nil {
			t.Error(err)
		}
	}
	defer func() { beforeOpen = prev }()
	type res struct {
		content string
		huge    bool
	}
	done := make(chan res, 1)
	go func() {
		c, h := readCurrent(p)
		done <- res{c, h}
	}()
	select {
	case r := <-done:
		if r.content != "" || r.huge {
			t.Fatalf("FIFO lue comme %q (huge=%v)", r.content, r.huge)
		}
	case <-time.After(2 * time.Second):
		// Débloque la lecture pour ne pas laisser de goroutine.
		if w, err := os.OpenFile(p, os.O_WRONLY, 0); err == nil {
			w.Close()
		}
		t.Fatal("readCurrent bloque sur une FIFO")
	}
}

// Une FIFO lue directement rend la main aussitôt.
func TestReadCurrentFIFO(t *testing.T) {
	p := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(p, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan string, 1)
	go func() { c, _ := readCurrent(p); done <- c }()
	select {
	case c := <-done:
		if c != "" {
			t.Fatalf("FIFO lue comme %q", c)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("readCurrent bloque sur une FIFO")
	}
}
