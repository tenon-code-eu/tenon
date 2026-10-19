package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tenon-code-eu/tenon/internal/agents"
	"github.com/tenon-code-eu/tenon/internal/engine"
	"github.com/tenon-code-eu/tenon/internal/i18n"
	"github.com/tenon-code-eu/tenon/internal/skills"
)

func skillCatalog(t *testing.T, names ...string) *skills.Catalog {
	t.Helper()
	r := t.TempDir()
	for _, n := range names {
		d := filepath.Join(r, n)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		md := "---\nname: " + n + "\ndescription: Desc " + n + "\n---\ncorps " + n + "\n"
		if err := os.WriteFile(filepath.Join(d, "SKILL.md"), []byte(md), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	c, _ := skills.Discover([]skills.Dir{{Path: r, Source: skills.User}})
	return c
}

func tab(h *harness) { h.send(tea.KeyMsg{Type: tea.KeyTab}) }

func notices(h *harness) string {
	var out []string
	for _, it := range h.m.tr.Items() {
		if it.Kind == ItemNotice {
			out = append(out, it.Text)
		}
	}
	return strings.Join(out, "\n")
}

func TestSubmitSkill(t *testing.T) {
	f := newFake()
	h := newHarness(t, f, Options{Lang: i18n.EN, Skills: skillCatalog(t, "review")})
	typeText(h, "/review main.go")
	enter(h)
	h.until("SubmitInput appelé", func() bool { return len(f.submitted()) == 1 })
	f.mu.Lock()
	in := f.full[0]
	f.mu.Unlock()
	if in.Skill != "review" || in.Typed != "/review main.go" || in.SkillSHA256 == "" || !strings.Contains(in.Text, "User request: main.go") {
		t.Fatalf("Input %+v", in)
	}
}

func TestSubmitDoubleSlash(t *testing.T) {
	f := newFake()
	h := newHarness(t, f, Options{Lang: i18n.EN, Skills: skillCatalog(t, "review")})
	typeText(h, "//review x")
	enter(h)
	h.until("SubmitInput appelé", func() bool { return len(f.submitted()) == 1 })
	f.mu.Lock()
	in := f.full[0]
	f.mu.Unlock()
	if in != (engine.Input{Text: "/review x"}) {
		t.Fatalf("Input %+v", in)
	}
}

func TestSubmitUnknown(t *testing.T) {
	for lang, want := range map[i18n.Lang]string{
		i18n.EN: "unknown skill or command: /nope",
		i18n.FR: "skill ou commande inconnu : /nope",
	} {
		f := newFake()
		h := newHarness(t, f, Options{Lang: lang, Skills: skillCatalog(t, "review")})
		typeText(h, "/nope arg")
		enter(h)
		if n := notices(h); n != want {
			t.Fatalf("avis %q, attendu %q", n, want)
		}
		if v := h.m.input.Value(); v != "/nope arg" {
			t.Fatalf("saisie %q", v)
		}
		time.Sleep(10 * time.Millisecond)
		if len(f.submitted()) != 0 {
			t.Fatal("rien ne devait être envoyé")
		}
	}
}

func TestSkillsCommand(t *testing.T) {
	f := newFake()
	h := newHarness(t, f, Options{Lang: i18n.EN, Skills: skillCatalog(t, "review")})
	typeText(h, "/skills")
	enter(h)
	if n := notices(h); n != "/review (user) — Desc review" {
		t.Fatalf("avis %q", n)
	}
	h2 := newHarness(t, newFake(), Options{Lang: i18n.EN})
	typeText(h2, "/skills")
	enter(h2)
	if n := notices(h2); n != "no skills" {
		t.Fatalf("avis %q", n)
	}
}

func TestBuiltinsWin(t *testing.T) {
	f := newFake()
	h := newHarness(t, f, Options{Lang: i18n.EN, Skills: skillCatalog(t, "help")})
	typeText(h, "/help")
	enter(h)
	if n := notices(h); !strings.Contains(n, "/help shows this help") {
		t.Fatalf("avis %q", n)
	}
	time.Sleep(10 * time.Millisecond)
	if len(f.submitted()) != 0 {
		t.Fatal("rien ne devait être envoyé")
	}
}

func TestHelpListsSkills(t *testing.T) {
	h := newHarness(t, newFake(), Options{Lang: i18n.EN, Skills: skillCatalog(t, "review", "deploy")})
	typeText(h, "/help")
	enter(h)
	if n := notices(h); !strings.Contains(n, "/review") || !strings.Contains(n, "/deploy") {
		t.Fatalf("avis %q", n)
	}
	h2 := newHarness(t, newFake(), Options{Lang: i18n.EN})
	typeText(h2, "/help")
	enter(h2)
	if n := notices(h2); strings.Contains(n, "skills:") {
		t.Fatalf("avis %q", n)
	}
}

func TestTabCompletion(t *testing.T) {
	c := skillCatalog(t, "review", "release", "deploy")
	cases := []struct{ in, want string }{
		{"/rev", "/review "},
		{"/re", "/re"}, // review/release : préfixe commun = "re"
		{"/rel", "/release "},
		{"/x", "/x"},
		{"/he", "/help "},
		{"/s", "/skills "},
		{"/", "/"},
		{"/review a", "/review a"},
		{"rev", "rev"},
	}
	for _, tc := range cases {
		h := newHarness(t, newFake(), Options{Lang: i18n.EN, Skills: c})
		typeText(h, tc.in)
		tab(h)
		if v := h.m.input.Value(); v != tc.want {
			t.Errorf("%q + Tab = %q, attendu %q", tc.in, v, tc.want)
		}
	}
	// Curseur pas en fin de texte : inchangé.
	h := newHarness(t, newFake(), Options{Lang: i18n.EN, Skills: c})
	typeText(h, "/rev")
	h.send(tea.KeyMsg{Type: tea.KeyLeft})
	tab(h)
	if v := h.m.input.Value(); v != "/rev" {
		t.Errorf("curseur au milieu : %q", v)
	}
}

func TestNilSkills(t *testing.T) {
	f := newFake()
	h := newHarness(t, f, Options{Lang: i18n.EN})
	typeText(h, "/nope")
	enter(h)
	h.until("envoyé tel quel", func() bool { return len(f.submitted()) == 1 })
	if f.submitted()[0] != "/nope" {
		t.Fatalf("%q", f.submitted()[0])
	}
}

func TestBuiltinWithArgsNotSkill(t *testing.T) {
	f := newFake()
	h := newHarness(t, f, Options{Lang: i18n.EN, Skills: skillCatalog(t, "help")})
	typeText(h, "/help foo")
	enter(h)
	if n := notices(h); n != "unknown skill or command: /help" {
		t.Fatalf("avis %q", n)
	}
	if v := h.m.input.Value(); v != "/help foo" {
		t.Fatalf("saisie %q", v)
	}
	time.Sleep(10 * time.Millisecond)
	if len(f.submitted()) != 0 {
		t.Fatal("rien ne devait être envoyé")
	}
}

func TestSubmitUnknownNewline(t *testing.T) {
	h := newHarness(t, newFake(), Options{Lang: i18n.EN, Skills: skillCatalog(t, "review")})
	h.m.input.SetValue("/nope\nfoo")
	enter(h)
	if n := notices(h); n != "unknown skill or command: /nope" {
		t.Fatalf("avis %q", n)
	}
}

func TestAgentsCommand(t *testing.T) {
	cat, _ := agents.Discover(i18n.EN, nil)
	h := newHarness(t, newFake(), Options{Lang: i18n.EN, Agents: cat})
	typeText(h, "/agents")
	enter(h)
	if n := notices(h); !strings.Contains(n, "general-purpose  builtin") {
		t.Fatalf("avis %q", n)
	}
}

func TestAgentsCommandControlChars(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "rev.md"), []byte("---\nname: rev\ndescription: \"review\\ncode\"\n---\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cat, _ := agents.Discover(i18n.EN, []agents.Dir{{Path: dir, Source: agents.User}})
	h := newHarness(t, newFake(), Options{Lang: i18n.EN, Agents: cat})
	typeText(h, "/agents")
	enter(h)
	if n := notices(h); !strings.Contains(n, "rev  user  review code") {
		t.Fatalf("avis %q", n)
	}
}
