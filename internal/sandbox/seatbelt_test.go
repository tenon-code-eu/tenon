package sandbox

import (
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
