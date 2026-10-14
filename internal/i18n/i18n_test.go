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
	for in, want := range map[string]Lang{"en_US.UTF-8": EN, "fr_FR": FR, "": FR, "de": FR, "en": EN} {
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
