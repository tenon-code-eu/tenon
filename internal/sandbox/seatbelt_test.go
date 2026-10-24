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
// d'écriture : fichier existant en (literal ...), dossier ou chemin absent en
// (subpath ...) (un dossier créé plus tard est couvert). Chaque dossier
// parent est interdit de suppression et de renommage seulement
// (file-write-unlink) : touch, tar ou rsync sur la racine restent possibles.
func TestSeatbeltDenyWrite(t *testing.T) {
	root := t.TempDir()
	git := filepath.Join(root, ".git")
	if err := os.MkdirAll(git, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(git, "config")
	if err := os.WriteFile(cfg, []byte("[core]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hooks := filepath.Join(git, "hooks")         // absent
	commondir := filepath.Join(git, "commondir") // absent
	p := seatbeltProfile(Spec{
		Dir:       root,
		Write:     []string{root},
		DenyWrite: []string{hooks, cfg, commondir},
		DenyRead:  []string{"/h/.ssh"},
	})
	order := []string{
		"(deny file-write*)",
		"(allow file-write*",
		`(subpath "` + root + `")`,
		"(deny file-write*",
		`(subpath "` + hooks + `")`,
		`(literal "` + cfg + `")`,
		`(subpath "` + commondir + `")`,
		"(deny file-write-unlink",
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
	// Les parents ne sont jamais interdits en file-write* entier.
	for _, line := range strings.Split(p, "\n") {
		if strings.HasPrefix(line, "(deny file-write* (") && strings.Contains(line, `(literal "`+root+`")`) {
			t.Errorf("parent sous file-write* : %s", line)
		}
	}
	if q := seatbeltProfile(Spec{Write: []string{root}}); strings.Contains(q, "(deny file-write* (") || strings.Contains(q, "file-write-unlink") {
		t.Error("règle DenyWrite sans DenyWrite")
	}
}

func TestSbplPathDevices(t *testing.T) {
	if got := sbplPath("/dev/null"); got != `(literal "/dev/null")` {
		t.Errorf("/dev/null: %s", got)
	}
	if got := sbplPath("/dev/shm"); got != `(subpath "/dev/shm")` {
		t.Errorf("/dev/shm doit rester un dossier inscriptible: %s", got)
	}
}
