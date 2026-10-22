package i18n

import (
	"regexp"
	"strings"
	"testing"
)

var verbRe = regexp.MustCompile(`%[-+# 0-9.]*[a-zA-Z%]`)

func verbs(s string) []string { return verbRe.FindAllString(s, -1) }

func TestCatalogComplete(t *testing.T) {
	if len(catalog[FR]) == 0 || len(catalog[EN]) == 0 {
		t.Fatal("catalogue vide")
	}
	for k, fr := range catalog[FR] {
		en, ok := catalog[EN][k]
		if !ok {
			t.Errorf("clé %q absente en EN", k)
			continue
		}
		if strings.Join(verbs(fr), ",") != strings.Join(verbs(en), ",") {
			t.Errorf("clé %q : verbes différents FR %v EN %v", k, verbs(fr), verbs(en))
		}
	}
	for k := range catalog[EN] {
		if _, ok := catalog[FR][k]; !ok {
			t.Errorf("clé %q absente en FR", k)
		}
	}
}

func TestParse(t *testing.T) {
	for in, want := range map[string]Lang{"en_US.UTF-8": EN, "fr_FR": FR, "": EN, "de": EN, "en": EN, "FR": FR} {
		if got := Parse(in); got != want {
			t.Errorf("Parse(%q) = %q, attendu %q", in, got, want)
		}
	}
}

func TestUnknownKey(t *testing.T) {
	if got := T(FR, "nope"); got != "!nope" {
		t.Fatalf("got %q", got)
	}
}

func TestT(t *testing.T) {
	if got := T(EN, "banner.prefix", "EU"); got != "jurisdiction EU" {
		t.Fatalf("got %q", got)
	}
	if got := T(FR, "err.prefix", "x"); got != "tenon : x" {
		t.Fatalf("got %q", got)
	}
}

func TestSystemPromptsDiffer(t *testing.T) {
	fr, en := SystemPrompt(FR), SystemPrompt(EN)
	if !strings.Contains(fr, "Réponds en français") || strings.Contains(fr, "Answer in English") {
		t.Error("prompt FR incorrect")
	}
	if !strings.Contains(en, "Answer in English") || strings.Contains(en, "Réponds en français") {
		t.Error("prompt EN incorrect")
	}
	for _, tool := range []string{"read_file", "write_file", "edit_file", "glob", "grep", "shell"} {
		if !strings.Contains(fr, tool) || !strings.Contains(en, tool) {
			t.Errorf("outil %s manquant", tool)
		}
	}
}

func TestMCPMessages(t *testing.T) {
	for _, c := range []struct {
		l         Lang
		key, want string
		args      []any
	}{
		{FR, "tui.perm.mcp", "serveur MCP gh · US · juridiction hors UE", []any{"gh", "US", "hors UE"}},
		{EN, "tui.perm.mcp", "MCP server gh · US · jurisdiction non-EU", []any{"gh", "US", "non-EU"}},
		{FR, "tui.perm.mcp_non_eu", "les arguments seront envoyés hors de l'UE", nil},
		{EN, "tui.perm.mcp_non_eu", "arguments will be sent outside the EU", nil},
		{FR, "mcp.down", "serveur MCP gh indisponible : x", []any{"gh", "x"}},
		{EN, "mcp.down", "MCP server gh unavailable: x", []any{"gh", "x"}},
		// R22 : « pour la session » garde le réseau accordé par la règle.
		{EN, "tui.perm.keys_tool_rule_network", "y: yes once (with network) · a: always for this session (with network) · n: no", nil},
		{FR, "tui.perm.keys_tool_rule_network", "o : oui une fois (avec réseau) · s : pour la session (avec réseau) · n : non", nil},
		{EN, "mcp.down_confined", `(confined: risk "read"; see [sandbox] write and risk)`, []any{"read"}},
		{FR, "mcp.down_confined", `(confiné : risk "read" ; voir [sandbox] write et risk)`, []any{"read"}},
		{FR, "banner.mcp", "MCP : a", []any{"a"}},
		{EN, "banner.mcp", "MCP: a", []any{"a"}},
	} {
		if got := T(c.l, c.key, c.args...); got != c.want {
			t.Errorf("%s %s = %q, attendu %q", c.l, c.key, got, c.want)
		}
	}
}

// TestUsageListsSubcommands : l'usage cite toutes les sous-commandes.
func TestUsageListsSubcommands(t *testing.T) {
	for _, l := range []Lang{FR, EN} {
		u := T(l, "usage")
		for _, c := range []string{"tenon sessions", "tenon skills", "tenon agents", "tenon hooks", "tenon key", "tenon audit verify", "tenon audit timestamp", "tenon config check", "tenon git"} {
			if !strings.Contains(u, c) {
				t.Errorf("%s : %q absent de l'usage", l, c)
			}
		}
	}
}
