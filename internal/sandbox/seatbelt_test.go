package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSeatbeltProfile(t *testing.T) {
	s := Spec{
		Dir:      "/proj",
		Write:    []string{"/proj", "/dev/null", `/we"ird\dir`},
		DenyRead: []string{"/h/.ssh"},
	}
	p := seatbeltProfile(s)
	order := []string{
		"(version 1)",
		"(allow default)",
		"(deny file-write*)",
		"(allow file-write*",
		`(subpath "/proj")`,
		`(literal "/dev/null")`,
		`(subpath "/we\"ird\\dir")`,
		"(deny file-read* file-write*",
		`(subpath "/h/.ssh")`,
		"(deny network*)",
	}
	pos := 0
	for _, o := range order {
		i := strings.Index(p[pos:], o)
		if i < 0 {
			t.Fatalf("%q absent ou hors ordre dans:\n%s", o, p)
		}
		pos += i + len(o)
	}

	s.Network = true
	if strings.Contains(seatbeltProfile(s), "(deny network*)") {
		t.Error("réseau ouvert mais (deny network*) présent")
	}
}

// R19 : DenyWrite interdit l'écriture seule, après les autorisations
// d'écriture : dossier en (subpath ...), fichier en (literal ...), et chaque
// dossier parent en (literal ...) pour qu'on ne puisse ni le renommer ni le
// supprimer et contourner l'interdit.
func TestSeatbeltDenyWrite(t *testing.T) {
	root := t.TempDir()
	git := filepath.Join(root, ".git")
	hooks := filepath.Join(git, "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(git, "config")
	if err := os.WriteFile(cfg, []byte("[core]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := seatbeltProfile(Spec{
		Dir:       root,
		Write:     []string{root},
		DenyWrite: []string{hooks, cfg},
		DenyRead:  []string{"/h/.ssh"},
	})
	order := []string{
		"(deny file-write*)",
		"(allow file-write*",
		`(subpath "` + root + `")`,
		"(deny file-write*",
		`(subpath "` + hooks + `")`,
		`(literal "` + cfg + `")`,
		`(literal "` + git + `")`,
		`(literal "` + root + `")`,
		"(deny file-read* file-write*",
		`(subpath "/h/.ssh")`,
	}
	pos := 0
	for _, o := range order {
		i := strings.Index(p[pos:], o)
		if i < 0 {
			t.Fatalf("%q absent ou hors ordre dans:\n%s", o, p)
		}
		pos += i + len(o)
	}
	if strings.Count(p, `(literal "`+git+`")`) != 1 {
		t.Errorf("parent en double dans:\n%s", p)
	}
	if strings.Contains(seatbeltProfile(Spec{Write: []string{root}}), "(deny file-write* (") {
		t.Error("règle DenyWrite sans DenyWrite")
	}
}
