package tui

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/i18n"
	"github.com/tenon-code-eu/tenon/internal/policy"
)

const (
	secretVal  = "Zx8kq2LmNp4Rt6Vw9YbC1dEf"
	secretLine = "MISTRAL_API_KEY=" + secretVal
)

func redactOpts(t *testing.T, root string) Options {
	t.Helper()
	red, err := policy.NewRedactor(nil)
	if err != nil {
		t.Fatal(err)
	}
	return Options{Lang: i18n.FR, ProjectRoot: root, Redact: func(s string) string { out, _ := red.Redact(s); return out }}
}

func assertNoSecret(t *testing.T, where, v string) {
	t.Helper()
	if strings.Contains(v, "Zx8kq") {
		t.Fatalf("%s : secret affiché :\n%s", where, v)
	}
}

// Comme en mode non interactif, tout texte venant des événements est masqué
// avant affichage.
func TestToolResultRedacted(t *testing.T) {
	f := newFake()
	h := newHarness(t, f, redactOpts(t, t.TempDir()))
	h.send(tea.WindowSizeMsg{Width: 120, Height: 40})
	args, _ := json.Marshal(map[string]string{"path": ".env"})
	f.emit(t, event.ToolCallRequested, event.ToolCallRequestedPayload{CallID: "c1", Tool: "read_file", Args: args})
	f.emit(t, event.ToolResult, event.ToolResultPayload{CallID: "c1", Content: secretLine + "\nautre\n"})
	h.until("résultat", func() bool { return strings.Contains(h.m.View(), "[ok]") })
	assertNoSecret(t, "résultat d'outil", h.m.View())
}

func TestEveryDisplayedTextRedacted(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte("a\n"+secretLine+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := newFake()
	h := newHarness(t, f, redactOpts(t, root))
	h.send(tea.WindowSizeMsg{Width: 120, Height: 60})

	// Texte de l'assistant en streaming, secret coupé entre deux deltas.
	f.emit(t, event.AssistantDelta, event.AssistantDeltaPayload{Text: "voici\nMISTRAL_API_KEY=Zx8kq"})
	h.until("premier delta", func() bool { return len(h.m.tr.Items()) == 1 })
	if v := h.m.View(); !strings.Contains(v, "voici") {
		t.Fatalf("ligne complète non affichée :\n%s", v)
	}
	assertNoSecret(t, "début du secret en streaming", h.m.View())
	f.emit(t, event.AssistantDelta, event.AssistantDeltaPayload{Text: "2LmNp4Rt6Vw9YbC1dEf\nfin"})
	h.until("deltas", func() bool { return len(h.m.tr.Items()) == 1 && strings.Contains(h.m.tr.items[0].raw, "fin") })
	assertNoSecret(t, "streaming", h.m.View())
	f.emit(t, event.AssistantMessage, event.AssistantMessagePayload{Text: "voici\n" + secretLine + "\nfin"})
	f.emit(t, event.Error, event.ErrorPayload{Code: "provider", Message: "échec " + secretLine})
	f.emit(t, event.UserMessage, event.UserMessagePayload{Text: "garde " + secretLine})
	h.until("événements", func() bool { return len(h.m.tr.Items()) == 3 })
	assertNoSecret(t, "assistant, erreur, utilisateur", h.m.View())

	// Demande : commande.
	askPerm(t, h, f, event.PermissionRequestedPayload{PermissionID: "p1", Kind: "tool", Tool: "shell", Command: "echo " + secretLine})
	v := h.m.View()
	if !strings.Contains(v, "echo MISTRAL_API_KEY=") {
		t.Fatalf("commande absente :\n%s", v)
	}
	assertNoSecret(t, "commande", v)
	h.send(runeKey('n'))

	// Demande : chemin et diff (contenu courant et nouveau contenu).
	args, _ := json.Marshal(map[string]string{"path": "f.txt", "content": "b\n" + secretLine + "\nTOKEN=Ab3dEf6hIj9kLm2nOp\n"})
	f.emit(t, event.ToolCallRequested, event.ToolCallRequestedPayload{CallID: "c2", Tool: "write_file", Args: args})
	askPerm(t, h, f, event.PermissionRequestedPayload{
		PermissionID: "p2", Kind: "tool", Tool: "write_file", Paths: []string{filepath.Join(root, "f.txt")},
	})
	v = h.m.View()
	if !strings.Contains(v, "- a") || !strings.Contains(v, "+ b") {
		t.Fatalf("diff absent :\n%s", v)
	}
	assertNoSecret(t, "diff", v)
	if strings.Contains(v, "Ab3dEf6hIj9kLm2nOp") {
		t.Fatalf("diff : secret affiché :\n%s", v)
	}
	h.send(runeKey('n'))

	// Erreur de Submit.
	h.send(submitDone{err: errors.New("refus " + secretLine)})
	assertNoSecret(t, "erreur de Submit", h.m.View())
}
