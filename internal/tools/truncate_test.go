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

// Un alignement sur les lignes qui ne garderait presque rien retombe sur la
// forme en caractères.
func TestTruncateAlignmentKeepsTooLittle(t *testing.T) {
	x := strings.Repeat("x", 10000)
	got := Truncate("{\n"+x+"\n}", 1000)
	want := "{\n" + x[:698] + "\n[… 9004 characters omitted …]\n" + x[:298] + "\n}"
	if got != want {
		t.Fatalf("got %.120q…", got)
	}
}

// numbered renvoie n lignes « ligne NNN » terminées par eol.
func numbered(n int, eol string) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "ligne %03d%s", i, eol)
	}
	return b.String()
}

// checkLines vérifie que got garde des lignes entières de in (terminées par
// eol) et que le marqueur compte exactement les lignes omises.
func checkLines(t *testing.T, got string, n int, eol string) {
	t.Helper()
	kept, omitted := 0, -1
	for _, l := range strings.Split(strings.TrimSuffix(got, "\n"), "\n") {
		if _, err := fmt.Sscanf(l, "[… %d lines omitted …]", &omitted); err == nil {
			continue
		}
		if !strings.HasPrefix(l, "ligne ") || l != strings.TrimSuffix(l, "\r")+strings.TrimSuffix(eol, "\n") {
			t.Fatalf("ligne coupée %q dans %q", l, got)
		}
		kept++
	}
	if omitted <= 0 || kept+omitted != n {
		t.Fatalf("gardées %d, omises %d, attendu %d au total", kept, omitted, n)
	}
}

func TestTruncateTrailingNewline(t *testing.T) {
	got := Truncate(numbered(200, "\n"), 200)
	if !strings.HasPrefix(got, "ligne 001\n") || !strings.HasSuffix(got, "ligne 200\n") {
		t.Fatalf("got %q", got)
	}
	checkLines(t, got, 200, "\n")
}

func TestTruncateCRLF(t *testing.T) {
	got := Truncate(numbered(200, "\r\n"), 220)
	if !strings.HasPrefix(got, "ligne 001\r\n") || !strings.HasSuffix(got, "ligne 200\r\n") {
		t.Fatalf("got %q", got)
	}
	checkLines(t, got, 200, "\r\n")
}

func TestTruncateKeepsFailureOnLastLine(t *testing.T) {
	in := numbered(300, "\n") + "FAIL\tgithub.com/x/y\t0.123s"
	got := Truncate(in, 300)
	if !strings.HasSuffix(got, "\nFAIL\tgithub.com/x/y\t0.123s") || !strings.HasPrefix(got, "ligne 001\n") {
		t.Fatalf("got %q", got)
	}
	if !strings.Contains(got, "lines omitted") {
		t.Fatalf("forme en caractères : %q", got)
	}
}
