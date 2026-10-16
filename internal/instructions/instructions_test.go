package instructions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadOrder(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "TENON.md", "t")
	write(t, dir, "AGENTS.md", "a")
	fs, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 2 || fs[0].Name != "AGENTS.md" || fs[1].Name != "TENON.md" {
		t.Fatalf("ordre inattendu : %+v", fs)
	}
	if fs[0].Content != "a" || fs[1].Content != "t" {
		t.Fatalf("contenu inattendu : %+v", fs)
	}
}

func TestMissingIsFine(t *testing.T) {
	fs, err := Load(t.TempDir())
	if err != nil || fs != nil {
		t.Fatalf("attendu nil, nil ; obtenu %v, %v", fs, err)
	}
}

func TestSymlinkNotLoaded(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	write(t, outside, "secret.txt", "SECRET")
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(dir, "AGENTS.md")); err != nil {
		t.Skip("symlink indisponible :", err)
	}
	fs, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 0 {
		t.Fatalf("lien symbolique chargé : %+v", fs)
	}
}

func TestTruncated(t *testing.T) {
	dir := t.TempDir()
	// "é" fait 2 octets : 40 000 octets, la coupe à 32 768 tombe entre deux runes
	// si l'on décale d'un octet.
	write(t, dir, "AGENTS.md", "x"+strings.Repeat("é", 19999)+"x")
	fs, err := Load(dir)
	if err != nil || len(fs) != 1 {
		t.Fatalf("%v %v", fs, err)
	}
	f := fs[0]
	if len(f.Content) > MaxBytes || !f.Truncated || !utf8.ValidString(f.Content) {
		t.Fatalf("len=%d truncated=%v valid=%v", len(f.Content), f.Truncated, utf8.ValidString(f.Content))
	}
}

func TestNotTruncatedAtLimit(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "AGENTS.md", strings.Repeat("a", MaxBytes))
	fs, _ := Load(dir)
	if len(fs) != 1 || fs[0].Truncated || len(fs[0].Content) != MaxBytes {
		t.Fatalf("%+v", fs)
	}
}

func TestRender(t *testing.T) {
	if got := Render("base", nil); got != "base" {
		t.Fatalf("base modifié : %q", got)
	}
	got := Render("base", []File{
		{Name: "AGENTS.md", Content: "a"},
		{Name: "TENON.md", Content: "t", Truncated: true},
	})
	want := "base\n\n# Project instructions (AGENTS.md)\n\na" +
		"\n\n# Project instructions (TENON.md)\n\nt\n[… truncated]"
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}
