package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"codeberg.org/tenon/tenon/internal/engine"
	"codeberg.org/tenon/tenon/internal/event"
	"codeberg.org/tenon/tenon/internal/i18n"
	"codeberg.org/tenon/tenon/internal/llm"
)

// describeMCP simule le registre des outils MCP.
func fakeMCP(servers map[string]event.MCPServerInfo) func(string) (event.MCPServerInfo, bool) {
	return func(tool string) (event.MCPServerInfo, bool) {
		s, ok := servers[tool]
		return s, ok
	}
}

func mcpPerm(id, tool, cmd string) event.PermissionRequestedPayload {
	return event.PermissionRequestedPayload{PermissionID: id, Kind: "tool", Tool: tool, Command: cmd}
}

func TestTUIPromptShowsMCPJurisdiction(t *testing.T) {
	servers := map[string]event.MCPServerInfo{
		"mcp__gh__issue": {ID: "gh", Jurisdiction: llm.NonEU, Country: "US"},
		"mcp__fr__doc":   {ID: "fr", Jurisdiction: llm.EU, Country: "FR‮"},
	}
	f := newFake()
	h := newHarness(t, f, Options{Lang: i18n.FR, DescribeMCP: fakeMCP(servers)})
	h.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	askPerm(t, h, f, mcpPerm("p1", "mcp__gh__issue", `mcp gh.issue {"title":"x"}`))
	v := h.m.View()
	for _, want := range []string{"serveur MCP gh · US · juridiction hors UE", "les arguments seront envoyés hors de l'UE"} {
		if !strings.Contains(v, want) {
			t.Fatalf("vue sans %q : %q", want, v)
		}
	}
	h.send(runeKey('o'))
	wantReply(t, f, "p1", engine.Reply{Allow: true})

	// Serveur UE : pas d'avertissement hors UE ; pays échappé sans ambiguïté.
	askPerm(t, h, f, mcpPerm("p2", "mcp__fr__doc", `mcp fr.doc {}`))
	v = h.m.View()
	if !strings.Contains(v, "serveur MCP fr · FR[U+202E] · juridiction UE") || strings.Contains(v, "hors de l'UE") {
		t.Fatalf("vue UE : %q", v)
	}
	h.send(runeKey('n'))

	// EN.
	f = newFake()
	h = newHarness(t, f, Options{Lang: i18n.EN, DescribeMCP: fakeMCP(servers)})
	h.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	askPerm(t, h, f, mcpPerm("p1", "mcp__gh__issue", `mcp gh.issue {}`))
	v = h.m.View()
	for _, want := range []string{"MCP server gh · US · jurisdiction non-EU", "arguments will be sent outside the EU"} {
		if !strings.Contains(v, want) {
			t.Fatalf("vue EN sans %q : %q", want, v)
		}
	}
}

// Les lignes MCP font partie de l'en-tête : une hauteur où la demande sans
// elles serait acceptable ne suffit pas.
func TestTUIMCPHeadCountsForApproval(t *testing.T) {
	servers := map[string]event.MCPServerInfo{"mcp__gh__issue": {ID: "gh", Jurisdiction: llm.NonEU, Country: "US"}}
	// Sans lignes MCP : outil hors préfixe mcp__, même commande.
	approvableAt := func(describe bool, height int) bool {
		f := newFake()
		tool := "plain_issue"
		if describe {
			tool = "mcp__gh__issue"
		}
		h := newHarness(t, f, Options{Lang: i18n.FR, DescribeMCP: fakeMCP(servers)})
		h.send(tea.WindowSizeMsg{Width: 80, Height: height})
		askPerm(t, h, f, mcpPerm("p1", tool, `mcp gh.issue {}`))
		return h.m.approvable()
	}
	plain := 0
	for hgt := 3; hgt < 40; hgt++ {
		if approvableAt(false, hgt) {
			plain = hgt
			break
		}
	}
	if plain == 0 {
		t.Fatal("jamais acceptable sans lignes MCP")
	}
	if approvableAt(true, plain) {
		t.Fatalf("hauteur %d : acceptable sans place pour les lignes MCP", plain)
	}
	f := newFake()
	h := newHarness(t, f, Options{Lang: i18n.FR, DescribeMCP: fakeMCP(servers)})
	h.send(tea.WindowSizeMsg{Width: 80, Height: plain})
	askPerm(t, h, f, mcpPerm("p1", "mcp__gh__issue", `mcp gh.issue {}`))
	h.send(runeKey('o'))
	if got := f.answered(); len(got) != 0 {
		t.Fatalf("acceptation avec l'en-tête MCP caché : %v", got)
	}
	if !approvableAt(true, plain+2) {
		t.Fatalf("hauteur %d : toujours pas acceptable", plain+2)
	}
}

func TestTUIServerDownNotice(t *testing.T) {
	for lang, want := range map[i18n.Lang]string{
		i18n.FR: "serveur MCP gh indisponible : boom",
		i18n.EN: "MCP server gh unavailable: boom",
	} {
		tr := NewTranscript(lang)
		tr.Apply(ev(t, event.McpServerDown, event.McpServerDownPayload{Server: "gh", Error: "boom"}))
		it := tr.Items()
		if len(it) != 1 || it[0].Kind != ItemNotice || it[0].Text != want {
			t.Fatalf("%s : items %+v", lang, it)
		}
	}
	tr := NewTranscript(i18n.FR)
	tr.SetRedact(func(s string) string { return strings.ReplaceAll(s, "Zx8kq", "[SECRET_1]") })
	tr.Apply(ev(t, event.McpServerDown, event.McpServerDownPayload{Server: "gh", Error: "\x1b[31mjeton Zx8kq‮"}))
	if s := tr.Items()[0].Text; strings.Contains(s, "Zx8kq") || strings.ContainsAny(s, "\x1b‮") {
		t.Fatalf("avis %q", s)
	}
}

func TestBannerListsMCP(t *testing.T) {
	tr := NewTranscript(i18n.FR)
	tr.Apply(ev(t, event.SessionStarted, event.SessionStartedPayload{
		Provider: llm.ProviderInfo{ID: "t", Model: "m", Jurisdiction: llm.EU},
		MCP:      []event.MCPServerInfo{{ID: "gh", Jurisdiction: llm.NonEU, Country: "US"}, {ID: "loc", Jurisdiction: llm.Local}},
	}))
	if s := tr.Items()[0].Text; !strings.Contains(s, "\nMCP : gh (hors UE), loc (locale)") {
		t.Fatalf("bannière %q", s)
	}
	tr = NewTranscript(i18n.EN)
	tr.Apply(ev(t, event.SessionStarted, event.SessionStartedPayload{Provider: llm.ProviderInfo{ID: "t", Model: "m", Jurisdiction: llm.EU}}))
	if s := tr.Items()[0].Text; strings.Contains(s, "MCP") {
		t.Fatalf("bannière %q", s)
	}
}

// Un outil mcp__* absent du registre : en-tête « serveur inconnu » et
// avertissement hors UE (fail-closed), comptés pour l'acceptation.
func TestTUIUnknownMCPServerFailClosed(t *testing.T) {
	for lang, want := range map[i18n.Lang][]string{
		i18n.FR: {"serveur MCP inconnu · juridiction inconnue", "les arguments seront envoyés hors de l'UE"},
		i18n.EN: {"unknown MCP server · unknown jurisdiction", "arguments will be sent outside the EU"},
	} {
		f := newFake()
		h := newHarness(t, f, Options{Lang: lang, DescribeMCP: fakeMCP(nil)})
		h.send(tea.WindowSizeMsg{Width: 100, Height: 30})
		askPerm(t, h, f, mcpPerm("p1", "mcp__ghost__x", `mcp ghost.x {}`))
		v := h.m.View()
		for _, w := range want {
			if !strings.Contains(v, w) {
				t.Fatalf("%s : vue sans %q : %q", lang, w, v)
			}
		}
	}
	// Outil interne : aucune ligne MCP.
	f := newFake()
	h := newHarness(t, f, Options{Lang: i18n.FR, DescribeMCP: fakeMCP(nil)})
	h.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	askPerm(t, h, f, mcpPerm("p1", "shell", `ls`))
	if v := h.m.View(); strings.Contains(v, "MCP") {
		t.Fatalf("lignes MCP pour un outil interne : %q", v)
	}
}

// Les avis de démarrage (avertissements MCP) entrent dans la conversation,
// nettoyés et masqués.
func TestTUIStartupNotices(t *testing.T) {
	f := newFake()
	h := newHarness(t, f, Options{
		Lang:    i18n.FR,
		Notices: []string{"MCP : outil \x1b[31mignoré Zx8kq‮"},
		Redact:  func(s string) string { return strings.ReplaceAll(s, "Zx8kq", "[SECRET_1]") },
	})
	it := h.m.tr.Items()
	if len(it) != 1 || it[0].Kind != ItemNotice || it[0].Text != "MCP : outil ignoré [SECRET_1]" {
		t.Fatalf("items %+v", it)
	}
}
