package orgpolicy

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tenon-code-eu/tenon/internal/orgpolicy/orgpolicytest"
)

const loadTOML = "id = \"acme\"\nversion = 2\nprofile = \"eu-only\"\n"

func TestLoadAbsent(t *testing.T) {
	for _, d := range []string{"", t.TempDir()} {
		p, err := Load(d, false)
		if p != nil || err != nil {
			t.Fatalf("Load(%q) = %v, %v", d, p, err)
		}
	}
}

func TestLoadValid(t *testing.T) {
	dir := t.TempDir()
	id := orgpolicytest.Sign(t, dir, loadTOML)
	p, err := Load(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "acme" || p.KeyID != id {
		t.Fatalf("got %+v, want key %s", p, id)
	}
}

func TestLoadBroken(t *testing.T) {
	cases := map[string]func(t *testing.T, dir string){
		"policy.toml.minisig": func(t *testing.T, dir string) { rm(t, dir, "policy.toml.minisig") },
		"policy.pub":          func(t *testing.T, dir string) { rm(t, dir, "policy.pub") },
		"policy.toml": func(t *testing.T, dir string) {
			f, err := os.OpenFile(filepath.Join(dir, "policy.toml"), os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if _, err := f.WriteString("\n"); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			orgpolicytest.Sign(t, dir, loadTOML)
			mutate(t, dir)
			want := filepath.Join(dir, name)
			_, err := Load(dir, false)
			if !errors.Is(err, ErrPolicy) {
				t.Fatalf("err = %v, want ErrPolicy", err)
			}
			if !strings.HasPrefix(err.Error(), want+": ") {
				t.Fatalf("err %q must start with %s", err, want)
			}
		})
	}
}

func rm(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.Remove(filepath.Join(dir, name)); err != nil {
		t.Fatal(err)
	}
}

func TestLoadPerms(t *testing.T) {
	dir := t.TempDir()
	orgpolicytest.Sign(t, dir, loadTOML)
	f := filepath.Join(dir, "policy.toml")
	if err := os.Chmod(f, 0o666); err != nil {
		t.Fatal(err)
	}
	err := checkPerms(f)
	if !errors.Is(err, ErrPolicy) || !strings.Contains(err.Error(), "writable by group or others") {
		t.Fatalf("err = %v", err)
	}
	if !strings.HasPrefix(err.Error(), f+": ") {
		t.Fatalf("err %q must start with path", err)
	}
	if os.Geteuid() == 0 {
		t.Skip("root")
	}
	if err := os.Chmod(f, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = Load(dir, true)
	if !errors.Is(err, ErrPolicy) || !strings.Contains(err.Error(), "not owned by root") {
		t.Fatalf("err = %v", err)
	}
}

func TestResolve(t *testing.T) {
	sys := t.TempDir()
	env := func(string) string { return "/other" }
	none := func(string) string { return "" }
	if d, s := resolve(sys, env); d != "/other" || s {
		t.Fatalf("empty system: %q %v", d, s)
	}
	if d, s := resolve(sys, none); d != "" || s {
		t.Fatalf("neither: %q %v", d, s)
	}
	// Échec fermé : Stat échoue avec ENOTDIR (≠ ErrNotExist).
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if d, s := resolve(file, env); d != file || !s {
		t.Fatalf("fail-closed: %q %v", d, s)
	}
	if err := os.WriteFile(filepath.Join(sys, "policy.toml"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if d, s := resolve(sys, env); d != sys || !s {
		t.Fatalf("system wins: %q %v", d, s)
	}
}
