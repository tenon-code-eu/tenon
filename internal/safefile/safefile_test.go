package safefile

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"
)

func TestReadRegular(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "a")
	os.WriteFile(p, []byte("hello"), 0o644)
	data, tr, ok, err := ReadRegular(p, 100)
	if err != nil || !ok || tr || string(data) != "hello" {
		t.Fatalf("%q %v %v %v", data, tr, ok, err)
	}
	if _, _, ok, err := ReadRegular(filepath.Join(d, "none"), 10); ok || err != nil {
		t.Fatal("absent", ok, err)
	}
}

func TestReadRegularTruncatesOnRune(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "a")
	os.WriteFile(p, []byte(strings.Repeat("é", 100)), 0o644)
	data, tr, ok, err := ReadRegular(p, 9)
	if err != nil || !ok || !tr || len(data) != 8 || !utf8.Valid(data) {
		t.Fatalf("%q %v %v %v", data, tr, ok, err)
	}
}

func TestReadRegularRefusesUnsafe(t *testing.T) {
	d := t.TempDir()
	target := filepath.Join(d, "t")
	os.WriteFile(target, []byte("x"), 0o644)
	link := filepath.Join(d, "l")
	os.Symlink(target, link)
	fifo := filepath.Join(d, "f")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Fatal(err)
	}
	done := make(chan bool, 1)
	go func() {
		_, _, ok1, _ := ReadRegular(link, 10)
		_, _, ok2, _ := ReadRegular(fifo, 10)
		done <- ok1 || ok2
	}()
	select {
	case bad := <-done:
		if bad {
			t.Fatal("symlink or fifo accepted")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("blocked")
	}
}
