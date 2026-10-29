package tui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/i18n"
)

func TestRenderMarkdownPlain(t *testing.T) {
	r := lipgloss.NewRenderer(nil) // sans couleur : le texte seul est vérifié
	st := mdStyles{bold: r.NewStyle(), code: r.NewStyle(), heading: r.NewStyle()}
	in := "## Résumé\nLe fichier **main.go** contient `add`.\n- point **un**\n  * sous-point\n```go\nx := a ** b\n```\n**non refermé\n2 * 3 = 6"
	want := "Résumé\nLe fichier main.go contient add.\n• point un\n  • sous-point\n  x := a ** b\n**non refermé\n2 * 3 = 6"
	if got := ansi.Strip(renderMarkdown(in, st)); got != want {
		t.Fatalf("rendu :\n%s\nattendu :\n%s", got, want)
	}
}

func TestRenderMarkdownStyles(t *testing.T) {
	r := lipgloss.NewRenderer(nil)
	r.SetColorProfile(2) // ANSI 256
	st := mdStyles{bold: r.NewStyle().Bold(true), code: r.NewStyle().Bold(true), heading: r.NewStyle().Bold(true)}
	if got := renderMarkdown("a **b** c", st); !strings.Contains(got, "\x1b[1mb\x1b[0m") {
		t.Fatalf("gras absent : %q", got)
	}
}

func TestConversationSpacing(t *testing.T) {
	m := New(newFake(), Options{Lang: i18n.FR, SessionID: "s"})
	m.resize(80, 40)
	apply := func(typ event.Type, p any) { m.tr.Apply(ev(t, typ, p)) }
	apply(event.UserMessage, event.UserMessagePayload{Text: "corrige"})
	apply(event.AssistantMessage, event.AssistantMessagePayload{Text: "\n\n"})
	apply(event.ToolCallRequested, event.ToolCallRequestedPayload{CallID: "1", Tool: "read_file", Args: json.RawMessage(`{"path":"main.go"}`)})
	apply(event.AssistantMessage, event.AssistantMessagePayload{Text: ""})
	apply(event.ToolCallRequested, event.ToolCallRequestedPayload{CallID: "2", Tool: "shell", Args: json.RawMessage(`{"command":"go run ."}`)})
	apply(event.AssistantMessage, event.AssistantMessagePayload{Text: "\nLe fichier **main.go** est corrigé.\n"})
	var got []string
	for _, l := range m.conversationLines() {
		got = append(got, strings.TrimRight(ansi.Strip(l), " "))
	}
	want := []string{
		"› corrige",
		"",
		"⏺ read_file main.go [en attente]",
		"⏺ shell go run . [en attente]",
		"",
		"Le fichier main.go est corrigé.",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("lignes :\n%s\nattendu :\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
