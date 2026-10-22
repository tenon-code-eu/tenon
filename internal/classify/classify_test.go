package classify

import (
	"os"
	"path/filepath"
	"testing"
)

func tmpRoot(t *testing.T) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestMatchPath(t *testing.T) {
	root := tmpRoot(t)
	if err := os.MkdirAll(filepath.Join(root, "rh"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "rh"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, pat, path string
		want            bool
	}{
		{"dir glob", "rh/**", "rh/a/b.txt", true},
		{"dir glob abs path", "rh/**", filepath.Join(root, "rh/a/b.txt"), true},
		{"prefix sibling", "rh/**", "rhx/a", false},
		{"star in segment", "*.dr.md", "n.dr.md", true},
		{"star not across", "*.dr.md", "x/n.dr.md", false},
		{"globstar", "**/*.dr.md", "x/n.dr.md", true},
		{"absolute", "/srv/secret/**", "/srv/secret/a", true},
		{"relative vs outside", "etc/**", "/etc/passwd", false},
		{"relative vs outside2", "**", "/etc/passwd", false},
		{"symlink", "rh/**", "link/f", true},
		{"dotdot", "rh/**", "sub/../rh/f", true},
		{"dotdot escape", "rh/**", "../rh/f", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Rules{Paths: []string{tt.pat}}.MatchPath(root, tt.path)
			if ok != tt.want {
				t.Fatalf("MatchPath(%q,%q)=%q,%v want %v", tt.pat, tt.path, got, ok, tt.want)
			}
			if ok && got != tt.pat {
				t.Fatalf("pattern=%q want %q", got, tt.pat)
			}
		})
	}
}

func TestFindMarker(t *testing.T) {
	user := Rules{Markers: []string{"C2 - Interne"}}
	tests := []struct {
		name, text, want string
		r                Rules
	}{
		{"case", "voir … diffusion restreinte …", "DIFFUSION RESTREINTE", Rules{}},
		{"accent+spaces", "Spécial  France", "SPÉCIAL FRANCE", Rules{}},
		{"newline", "SPECIAL\nFRANCE", "SPÉCIAL FRANCE", Rules{}},
		{"split lines", "DIFFUSION\n\tRESTREINTE", "DIFFUSION RESTREINTE", Rules{}},
		{"suffix letter", "DIFFUSION RESTREINTES", "", Rules{}},
		{"prefix letter", "XDIFFUSION RESTREINTE", "", Rules{}},
		{"later occurrence", "XDIFFUSION RESTREINTE puis eu restricted.", "EU RESTRICTED", Rules{}},
		{"user", "doc c2 - interne ok", "C2 - Interne", user},
		{"punct boundary", "(RESTREINT UE)", "RESTREINT UE", Rules{}},
		{"none", "rien", "", user},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := tt.r.FindMarker(tt.text)
			if ok != (tt.want != "") || got != tt.want {
				t.Fatalf("FindMarker(%q)=%q,%v want %q", tt.text, got, ok, tt.want)
			}
		})
	}
}

func TestMatchCommand(t *testing.T) {
	root := tmpRoot(t)
	r := Rules{Paths: []string{"rh/**", "contrats/**"}}
	tests := []struct {
		cmd  string
		want string
	}{
		{"cat rh/paie.csv", "rh/**"},
		{"grep -r x ./rh", "rh/**"},
		{"echo rh", ""},
		{`cp a "contrats/x y.pdf" b`, "contrats/**"},
		{"cat x>rh/out.txt", "rh/**"},
		{"cat a;cat rh/b", "rh/**"},
		{"ls src", ""},
	}
	for _, tt := range tests {
		got, ok := r.MatchCommand(root, tt.cmd)
		if ok != (tt.want != "") || got != tt.want {
			t.Errorf("MatchCommand(%q)=%q,%v want %q", tt.cmd, got, ok, tt.want)
		}
	}
}

func TestEmpty(t *testing.T) {
	if !(Rules{}).Empty() {
		t.Fatal("empty")
	}
	if (Rules{Markers: []string{"x"}}).Empty() || (Rules{Paths: []string{"x"}}).Empty() {
		t.Fatal("not empty")
	}
}

func TestFoldCombiningMarks(t *testing.T) {
	r := Rules{}
	for _, in := range []string{"Spécial France", "dIFFUSION  restreinte", "Diffusion Restreinte", "RESTREINT UE"} {
		if _, ok := r.FindMarker(in); !ok {
			t.Errorf("no match for %q", in)
		}
	}
	if m, _ := r.FindMarker("Spécial France"); m != "SPÉCIAL FRANCE" {
		t.Errorf("got %q", m)
	}
	if _, ok := (Rules{Markers: []string{"Résumé interne"}}).FindMarker("résumé interne"); !ok {
		t.Error("NFD user marker")
	}
}

func TestMatchPathFoldAndLexical(t *testing.T) {
	root := tmpRoot(t)
	if _, ok := (Rules{Paths: []string{"rh/**"}}).MatchPath(root, "RH/paie.csv"); !ok {
		t.Error("case")
	}
	if _, ok := (Rules{Paths: []string{"Dossiers/Résumé/**"}}).MatchPath(root, "dossiers/résumé/f"); !ok {
		t.Error("NFD path vs NFC pattern")
	}
	if _, ok := (Rules{Paths: []string{"Dossiers/Résumé/**"}}).MatchPath(root, "dossiers/résumé/f"); !ok {
		t.Error("NFC path vs NFD pattern")
	}
	out := tmpRoot(t)
	if err := os.Symlink(out, filepath.Join(root, "rh")); err != nil {
		t.Fatal(err)
	}
	if _, ok := (Rules{Paths: []string{"rh/**"}}).MatchPath(root, filepath.Join(root, "rh", "f")); !ok {
		t.Error("symlink to outside")
	}
	if _, ok := (Rules{Paths: []string{"rh/**"}}).MatchPath(root, "rh/f"); !ok {
		t.Error("symlink to outside, relative")
	}
}
