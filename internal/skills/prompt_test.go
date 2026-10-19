package skills

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/tenon-code-eu/tenon/internal/i18n"
)

func TestPromptSection(t *testing.T) {
	r := t.TempDir()
	writeSkill(t, r, "review", "name: review\ndescription: Review code", "b")
	writeSkill(t, r, "hidden", "name: hidden\ndescription: Nope\ndisable-model-invocation: true", "b")
	c, _ := Discover([]Dir{{r, User}})
	s, dropped := c.PromptSection(i18n.EN, 8000)
	want := i18n.T(i18n.EN, "skills.prompt_header") + "\n- review: Review code"
	if s != want || dropped != 0 {
		t.Fatalf("%q %d", s, dropped)
	}
	r2 := t.TempDir()
	writeSkill(t, r2, "hidden", "name: hidden\ndescription: Nope\ndisable-model-invocation: true", "b")
	c2, _ := Discover([]Dir{{r2, User}})
	if s, d := c2.PromptSection(i18n.EN, 8000); s != "" || d != 0 {
		t.Fatalf("%q %d", s, d)
	}
}

func TestPromptBudget(t *testing.T) {
	r := t.TempDir()
	for i := 0; i < 50; i++ {
		n := fmt.Sprintf("s%02d", i)
		writeSkill(t, r, n, "name: "+n+"\ndescription: "+strings.Repeat("x", 1000), "b")
	}
	c, _ := Discover([]Dir{{r, User}})
	s, dropped := c.PromptSection(i18n.EN, 8000)
	if len(s) > 8000 || dropped == 0 {
		t.Fatalf("len %d dropped %d", len(s), dropped)
	}
	for _, l := range strings.Split(s, "\n")[2:] {
		if len(l) > len("- s00: ")+200 {
			t.Fatalf("ligne trop longue : %d", len(l))
		}
	}
	// Les derniers sont retirés.
	if strings.Contains(s, "s49") {
		t.Fatal("s49 devrait être retiré")
	}
}

func TestExpand(t *testing.T) {
	r := t.TempDir()
	dir := writeSkill(t, r, "review", "name: review\ndescription: d", "Do it.\n\n")
	writeSkill(t, r, "hid", "name: hid\ndescription: d\ndisable-model-invocation: true", "H")
	c, _ := Discover([]Dir{{r, User}})
	_, sum, _, _ := c.Load("review")
	e, ok, err := c.Expand("/review  main.go ")
	want := "Use the skill \"review\".\n<skill name=\"review\" source=\"user\" dir=\"" + dir + "\">\nDo it.\n</skill>\nUser request: main.go"
	if err != nil || !ok || e.Text != want || e.Skill != "review" || e.SHA256 != sum {
		t.Fatalf("%q %v %v %+v", e.Text, ok, err, e)
	}
	e, ok, err = c.Expand("/review")
	if err != nil || !ok || strings.Contains(e.Text, "User request") {
		t.Fatalf("%q %v %v", e.Text, ok, err)
	}
	if e, ok, err = c.Expand("/hid x"); err != nil || !ok || e.Skill != "hid" {
		t.Fatalf("%+v %v %v", e, ok, err)
	}
	for _, l := range []string{"/nope", "/Foo", "/a/b"} {
		if _, ok, err := c.Expand(l); !ok || !errors.Is(err, ErrUnknownCommand) {
			t.Fatalf("%s: %v %v", l, ok, err)
		}
	}
	for _, l := range []string{"hello", "/ x", "/"} {
		if _, ok, err := c.Expand(l); ok || err != nil {
			t.Fatalf("%s: %v %v", l, ok, err)
		}
	}
	e, ok, err = c.Expand("//etc")
	if err != nil || !ok || e.Text != "/etc" || e.Skill != "" {
		t.Fatalf("%+v %v %v", e, ok, err)
	}
}

func TestExpandTruncated(t *testing.T) {
	r := t.TempDir()
	writeSkill(t, r, "big", valid("big"), strings.Repeat("a", MaxSkillBytes))
	c, _ := Discover([]Dir{{r, User}})
	e, _, err := c.Expand("/big")
	if err != nil || !strings.HasSuffix(e.Text, "\n[… truncated]\n</skill>") {
		t.Fatalf("%v %q", err, e.Text[len(e.Text)-40:])
	}
}

func TestIsBuiltin(t *testing.T) {
	if !IsBuiltin("help") || !IsBuiltin("quit") || !IsBuiltin("skills") || !IsBuiltin("agents") || IsBuiltin("review") {
		t.Fatal("IsBuiltin")
	}
}

func TestPromptSectionFR(t *testing.T) {
	r := t.TempDir()
	writeSkill(t, r, "review", "name: review\ndescription: Review code", "b")
	c, _ := Discover([]Dir{{r, User}})
	s, _ := c.PromptSection(i18n.FR, 8000)
	if !strings.HasPrefix(s, "## Skills disponibles\n") {
		t.Fatalf("%q", s)
	}
}

func TestExpandBuiltinNotExpanded(t *testing.T) {
	r := t.TempDir()
	writeSkill(t, r, "help", "name: help\ndescription: Shadow", "b")
	c, _ := Discover([]Dir{{r, User}})
	if _, _, err := c.Expand("/help x"); !errors.Is(err, ErrUnknownCommand) {
		t.Fatalf("err = %v", err)
	}
}

func TestPromptSectionEmptyWhenNoneListed(t *testing.T) {
	r := t.TempDir()
	writeSkill(t, r, "review", "name: review\ndescription: "+strings.Repeat("d", 300), "b")
	c, _ := Discover([]Dir{{r, User}})
	// Budget plus petit que l'en-tête : plus aucun skill ne tient.
	s, dropped := c.PromptSection(i18n.EN, 5)
	if s != "" || dropped != 1 {
		t.Fatalf("section %q dropped %d, attendu \"\" et 1", s, dropped)
	}
}

func TestBuiltinsSingleSource(t *testing.T) {
	for _, n := range Builtins {
		if !IsBuiltin(n) {
			t.Fatalf("IsBuiltin(%q) faux", n)
		}
	}
	if len(Builtins) != 6 {
		t.Fatalf("Builtins %v", Builtins)
	}
}
