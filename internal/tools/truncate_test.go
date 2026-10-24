package tools

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateHeadTail(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 200; i++ {
		fmt.Fprintf(&b, "ligne %03d\n", i)
	}
	in := strings.TrimSuffix(b.String(), "\n")
	got := Truncate(in, 200) // 20 lignes de 10 caractères
	if !strings.HasPrefix(got, "ligne 001\n") || !strings.HasSuffix(got, "ligne 200") {
		t.Fatalf("tête ou queue perdue: %q", got)
	}
	lines := strings.Split(got, "\n")
	var marker string
	kept := 0
	for _, l := range lines {
		if strings.HasPrefix(l, "[…") {
			marker = l
		} else {
			kept++
		}
	}
	want := fmt.Sprintf("[… %d lines omitted …]", 200-kept)
	if marker != want || kept >= 200 {
		t.Fatalf("marqueur %q, attendu %q (gardées %d)", marker, want, kept)
	}
}

func TestTruncateSingleLine(t *testing.T) {
	got := Truncate(strings.Repeat("a", 1000), 100)
	want := strings.Repeat("a", 70) + "\n[… 900 characters omitted …]\n" + strings.Repeat("a", 30)
	if got != want {
		t.Fatalf("got %q", got)
	}
}

func TestTruncateUTF8(t *testing.T) {
	in := strings.Repeat("é", 500) + strings.Repeat("日", 500)
	for _, lim := range []int{10, 33, 101} {
		got := Truncate(in, lim)
		if !utf8.ValidString(got) {
			t.Fatalf("UTF-8 invalide pour limit=%d", lim)
		}
	}
}

func TestTruncateShort(t *testing.T) {
	for _, c := range []struct {
		s     string
		limit int
	}{{"abc", 5}, {"abc", 3}, {"abcdef", 0}, {"abcdef", -1}} {
		if got := Truncate(c.s, c.limit); got != c.s {
			t.Fatalf("%q/%d: %q", c.s, c.limit, got)
		}
	}
}
