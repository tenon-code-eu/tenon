package agents

import (
	"fmt"
	"strings"
	"testing"

	"github.com/tenon-code-eu/tenon/internal/i18n"
)

func TestPromptSection(t *testing.T) {
	r := t.TempDir()
	writeAgent(t, r, "review", valid("review"), "")
	c, _ := Discover(i18n.EN, []Dir{{r, User}})
	s, dropped := c.PromptSection(i18n.EN, 4000)
	want := i18n.T(i18n.EN, "agents.prompt_header") + "\n- general-purpose: " + i18n.T(i18n.EN, "agents.general_purpose") + "\n- review: desc review"
	if s != want || dropped != 0 {
		t.Fatalf("%q %d", s, dropped)
	}
}

func TestPromptBudget(t *testing.T) {
	r := t.TempDir()
	for i := 0; i < 50; i++ {
		n := fmt.Sprintf("s%02d", i)
		writeAgent(t, r, n, "name: "+n+"\ndescription: "+strings.Repeat("x", 1000), "")
	}
	c, _ := Discover(i18n.EN, []Dir{{r, User}})
	s, dropped := c.PromptSection(i18n.EN, 4000)
	if len(s) > 4000 || dropped == 0 {
		t.Fatalf("len %d dropped %d", len(s), dropped)
	}
	for _, l := range strings.Split(s, "\n")[2:] {
		if len(l) > len("- s00: ")+200 {
			t.Fatalf("ligne trop longue : %d", len(l))
		}
	}
	if strings.Contains(s, "s49") {
		t.Fatal("s49 devrait être retiré")
	}
}

func TestSystemPrompt(t *testing.T) {
	a := Agent{Body: "BODY"}
	got := SystemPrompt(i18n.EN, "BASE", "SKILLS", a, true)
	want := "BASE\n\nSKILLS\n\n" + i18n.T(i18n.EN, "agents.preamble") + "\n\nBODY"
	if got != want {
		t.Fatalf("%q", got)
	}
	got = SystemPrompt(i18n.FR, "BASE", "SKILLS", Agent{}, false)
	if got != "BASE\n\n"+i18n.T(i18n.FR, "agents.preamble") || !strings.Contains(got, "sous-agent") {
		t.Fatalf("%q", got)
	}
}
