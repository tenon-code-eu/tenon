package hooks

import (
	"encoding/json"
	"reflect"
	"testing"
)

// sameJSON compare deux documents JSON sans tenir compte de l'ordre des clés.
func sameJSON(t *testing.T, got, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal([]byte(got), &g); err != nil {
		t.Fatalf("sortie non JSON %q: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("attendu non JSON %q: %v", want, err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestClaudeName(t *testing.T) {
	cases := map[string]string{
		"shell":      "Bash",
		"read_file":  "Read",
		"write_file": "Write",
		"edit_file":  "Edit",
		"grep":       "Grep",
		"glob":       "Glob",
		"skill":      "Skill",
		"agent":      "Task",
		"mcp__s__t":  "mcp__s__t",
		"inconnu":    "inconnu",
	}
	for in, want := range cases {
		if got := ClaudeName(in); got != want {
			t.Errorf("ClaudeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestToClaude(t *testing.T) {
	cases := []struct {
		name, tenon, args, want string
	}{
		{"read_file", "read_file", `{"path":"a","offset":2,"limit":3}`, `{"file_path":"a","offset":2,"limit":3}`},
		{"write_file", "write_file", `{"path":"a","content":"x"}`, `{"file_path":"a","content":"x"}`},
		{"edit_file", "edit_file", `{"path":"a","old_string":"x","new_string":"y"}`, `{"file_path":"a","old_string":"x","new_string":"y"}`},
		{"shell", "shell", `{"command":"ls","timeout_seconds":2}`, `{"command":"ls","timeout":2000}`},
		{"skill", "skill", `{"name":"x","file":"f"}`, `{"skill":"x","file":"f"}`},
		{"agent", "agent", `{"agent":"r","description":"d","prompt":"p"}`, `{"subagent_type":"r","description":"d","prompt":"p"}`},
		{"grep inchangé", "grep", `{"pattern":"p","path":"x"}`, `{"pattern":"p","path":"x"}`},
		{"glob inchangé", "glob", `{"pattern":"*.go"}`, `{"pattern":"*.go"}`},
		{"mcp inchangé", "mcp__s__t", `{"q":1}`, `{"q":1}`},
		{"outil inconnu", "inconnu", `{"path":"a"}`, `{"path":"a"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sameJSON(t, string(ToClaude(c.tenon, json.RawMessage(c.args))), c.want)
		})
	}
}

func TestToClaudeInvalidInputUnchanged(t *testing.T) {
	for _, in := range []string{`{"path":`, `[1]`, `"x"`, `null`} {
		got := ToClaude("read_file", json.RawMessage(in))
		if string(got) != in {
			t.Errorf("ToClaude(%s) = %s, want unchanged", in, got)
		}
	}
}

func TestFromClaude(t *testing.T) {
	cases := []struct {
		name, tenon, in, want string
	}{
		{"read_file", "read_file", `{"file_path":"a","offset":2,"limit":3}`, `{"path":"a","offset":2,"limit":3}`},
		{"write_file", "write_file", `{"file_path":"a","content":"x"}`, `{"path":"a","content":"x"}`},
		{"edit_file", "edit_file", `{"file_path":"a","old_string":"x"}`, `{"path":"a","old_string":"x"}`},
		{"shell ms", "shell", `{"command":"ls","timeout":2000}`, `{"command":"ls","timeout_seconds":2}`},
		{"shell arrondi", "shell", `{"command":"ls","timeout":1500}`, `{"command":"ls","timeout_seconds":2}`},
		{"shell 1 ms", "shell", `{"timeout":1}`, `{"timeout_seconds":1}`},
		{"shell champ inconnu", "shell", `{"command":"ls","timeout":1000,"x":1}`, `{"command":"ls","timeout_seconds":1,"x":1}`},
		{"skill", "skill", `{"skill":"x","file":"f"}`, `{"name":"x","file":"f"}`},
		{"agent", "agent", `{"subagent_type":"r","description":"d","prompt":"p"}`, `{"agent":"r","description":"d","prompt":"p"}`},
		{"grep inchangé", "grep", `{"pattern":"p","path":"x"}`, `{"pattern":"p","path":"x"}`},
		{"mcp inchangé", "mcp__s__t", `{"q":1}`, `{"q":1}`},
		{"outil inconnu", "inconnu", `{"file_path":"a"}`, `{"file_path":"a"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := FromClaude(c.tenon, json.RawMessage(c.in))
			if err != nil {
				t.Fatalf("erreur inattendue: %v", err)
			}
			sameJSON(t, string(got), c.want)
		})
	}
}

func TestFromClaudeErrors(t *testing.T) {
	cases := []struct {
		name, tenon, in, msg string
	}{
		{"tableau", "read_file", `[]`, "updatedInput must be an object"},
		{"chaîne", "read_file", `"x"`, "updatedInput must be an object"},
		{"null", "read_file", `null`, "updatedInput must be an object"},
		{"JSON invalide", "read_file", `{"file_path":`, "updatedInput must be an object"},
		{"timeout chaîne", "shell", `{"timeout":"5"}`, "invalid timeout in updatedInput"},
		{"timeout booléen", "shell", `{"timeout":true}`, "invalid timeout in updatedInput"},
		{"timeout null", "shell", `{"timeout":null}`, "invalid timeout in updatedInput"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := FromClaude(c.tenon, json.RawMessage(c.in))
			if err == nil || err.Error() != c.msg {
				t.Fatalf("erreur = %v, want %q", err, c.msg)
			}
		})
	}
}

// TestRoundTrip vérifie que FromClaude(ToClaude(x)) redonne x, clés mises à part.
func TestRoundTrip(t *testing.T) {
	cases := []struct {
		tenon, args string
	}{
		{"read_file", `{"path":"a","offset":2,"limit":3}`},
		{"write_file", `{"path":"a","content":"x"}`},
		{"edit_file", `{"path":"a","old_string":"x","new_string":"y"}`},
		{"shell", `{"command":"ls","timeout_seconds":2}`},
		{"skill", `{"name":"x","file":"f"}`},
		{"agent", `{"agent":"r","description":"d","prompt":"p"}`},
		{"grep", `{"pattern":"p","path":"x"}`},
		{"glob", `{"pattern":"*.go"}`},
		{"mcp__s__t", `{"q":1}`},
		{"inconnu", `{"path":"a"}`},
	}
	for _, c := range cases {
		t.Run(c.tenon, func(t *testing.T) {
			back, err := FromClaude(c.tenon, ToClaude(c.tenon, json.RawMessage(c.args)))
			if err != nil {
				t.Fatalf("erreur inattendue: %v", err)
			}
			sameJSON(t, string(back), c.args)
		})
	}
}

// TestRoundTripKeepsRawValues vérifie que les valeurs non traduites restent octet pour octet.
func TestRoundTripKeepsRawValues(t *testing.T) {
	const raw = `{"command":"ls","timeout_seconds":2,"big":12345678901234567890,"nested":{"b":1.50,"a":[1e3,"x"]}}`
	claude := ToClaude("shell", json.RawMessage(raw))
	back, err := FromClaude("shell", claude)
	if err != nil {
		t.Fatalf("erreur inattendue: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(back, &fields); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{"big": `12345678901234567890`, "nested": `{"b":1.50,"a":[1e3,"x"]}`} {
		if string(fields[k]) != want {
			t.Errorf("champ %s = %s, want %s", k, fields[k], want)
		}
	}
}
