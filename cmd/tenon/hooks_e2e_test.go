package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hooksFile renvoie le chemin du fichier de hooks utilisateur de e.
func (e *env) hooksFile() string {
	return filepath.Join(e.vars["XDG_CONFIG_HOME"], "tenon", "hooks.json")
}

func TestHooksBlockShell(t *testing.T) {
	f := newFakeLLM(t, sseCall("shell", `{"command":"echo hi"}`), sseText("fini"))
	e := newEnv(t, f, "eu")
	e.writeFile(e.hooksFile(), `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"echo nope >&2; exit 2"}]}]}}`)
	code, _, errb := e.run("-p", "go", "--lang", "en")
	if code != 3 || !strings.Contains(errb, "blocked: nope") {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
}

func TestHooksClaudeSettingsOptIn(t *testing.T) {
	const settings = `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo claude-hook"}]}]}}`
	e := newEnv(t, nil, "eu")
	e.writeFile(filepath.Join(e.vars["HOME"], ".claude", "settings.json"), settings)
	code, out, errb := e.run("hooks")
	if code != 0 || strings.Contains(out, "claude-hook") || !strings.Contains(out, "aucun hook") {
		t.Fatalf("sans opt-in : code=%d out=%q stderr=%q", code, out, errb)
	}
	e = newEnvExtra(t, nil, "eu", "\n[hooks]\nclaude_settings = true\n")
	e.writeFile(filepath.Join(e.vars["HOME"], ".claude", "settings.json"), settings)
	code, out, errb = e.run("hooks", "--lang", "en")
	if code != 0 || !strings.Contains(out, "claude:Stop:0.0  *  command  echo claude-hook") {
		t.Fatalf("avec opt-in : code=%d out=%q stderr=%q", code, out, errb)
	}
}

func TestHooksProjectNotice(t *testing.T) {
	f := newFakeLLM(t, sseText("ok"))
	e := newEnv(t, f, "eu")
	marker := filepath.Join(e.wd, "ran")
	e.writeFile(filepath.Join(e.wd, ".claude", "settings.json"),
		`{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"touch `+marker+`"}]}]}}`)
	code, _, errb := e.run("-p", "go", "--lang", "en")
	if code != 0 || !strings.Contains(errb, "project hooks ignored: ") {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("hook du projet exécuté")
	}
	if _, out, _ := e.run("hooks", "--lang", "en"); !strings.Contains(out, "project hooks ignored: ") {
		t.Fatalf("tenon hooks : %q", out)
	}
}

func TestHooksInvalidFileRefusesStart(t *testing.T) {
	e := newEnv(t, nil, "eu")
	e.writeFile(e.hooksFile(), `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":""}]}]}}`)
	for _, args := range [][]string{{"-p", "x"}, {"config", "check"}, {"hooks"}} {
		code, _, errb := e.run(args...)
		if code != 1 || !strings.Contains(errb, "hooks.Stop[0].hooks[0]") {
			t.Fatalf("%v : code=%d stderr=%q", args, code, errb)
		}
	}
}

func TestHooksSessionEnd(t *testing.T) {
	f := newFakeLLM(t, sseText("ok"))
	e := newEnv(t, f, "eu")
	out := filepath.Join(e.wd, "end.json")
	e.writeFile(e.hooksFile(), `{"hooks":{"SessionEnd":[{"hooks":[{"type":"command","command":"cat > `+out+`"}]}]}}`)
	if code, _, errb := e.run("-p", "go"); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var in struct {
		Reason string `json:"reason"`
	}
	if json.Unmarshal(b, &in) != nil || in.Reason != "other" {
		t.Fatalf("entrée du hook : %s", b)
	}
}
