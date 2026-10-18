package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tenon-code-eu/tenon/internal/event"
)

const reviewSkill = "---\nname: review\ndescription: Relit un diff avec rigueur.\n---\nRegardez le diff.\n"

// skillEnv crée un environnement avec un skill utilisateur « review » et un
// skill projet « deploy ».
func skillEnv(t *testing.T, f *fakeLLM) *env {
	t.Helper()
	e := newEnv(t, f, "eu")
	e.writeFile(filepath.Join(e.vars["HOME"], ".claude", "skills", "review", "SKILL.md"), reviewSkill)
	e.writeFile(filepath.Join(e.wd, ".tenon", "skills", "deploy", "SKILL.md"),
		"---\nname: deploy\ndescription: Déploie le projet.\nallowed-tools: Bash(make deploy), Read\n---\nLancez make deploy.\n")
	return e
}

func TestSkillsListCommand(t *testing.T) {
	e := skillEnv(t, nil)
	code, out, errb := e.run("skills")
	if code != 0 || errb != "" {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	if !strings.Contains(out, "review  user  ") || !strings.Contains(out, "Relit un diff avec rigueur.") {
		t.Fatalf("skill utilisateur absent : %q", out)
	}
	if !strings.Contains(out, "deploy  project  ") || !strings.Contains(out, "allowed-tools: Bash(make deploy), Read") {
		t.Fatalf("skill projet absent : %q", out)
	}
	e2 := newEnv(t, nil, "eu")
	if code, out, _ := e2.run("skills", "--lang", "en"); code != 0 || strings.TrimSpace(out) != "no skills" {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestSkillToolEndToEnd(t *testing.T) {
	f := newFakeLLM(t, sseCall("skill", `{"name":"review"}`), sseText("fait"))
	e := skillEnv(t, f)
	code, _, errb := e.run("-p", "relis", "--output", "json")
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	reqs := f.requests()
	if len(reqs) != 2 || !strings.Contains(reqs[1], "Regardez le diff.") || !strings.Contains(reqs[1], `"role":"tool"`) {
		t.Fatalf("requêtes : %q", reqs)
	}
	sum := sha256.Sum256([]byte(reviewSkill))
	want := "sha256:" + hex.EncodeToString(sum[:])
	found := false
	for _, ev := range decodeEvents(t, e.auditContents()) {
		var p event.ToolResultPayload
		if ev.Type == event.ToolResult && json.Unmarshal(ev.Payload, &p) == nil && p.Tool == "skill" {
			found = true
			if p.HashAfter != want {
				t.Fatalf("hash_after = %q, attendu %q", p.HashAfter, want)
			}
		}
	}
	if !found {
		t.Fatal("aucun ToolResult skill dans l'audit")
	}
	if code, out, errb := e.run("audit", "verify"); code != 0 {
		t.Fatalf("audit verify : code=%d stdout=%q stderr=%q", code, out, errb)
	}
}

func TestSlashSkillHeadless(t *testing.T) {
	f := newFakeLLM(t, sseText("ok"))
	e := skillEnv(t, f)
	if code, _, errb := e.run("-p", "/review main.go"); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	reqs := f.requests()
	if len(reqs) != 1 {
		t.Fatalf("requêtes : %d", len(reqs))
	}
	dir := filepath.Join(e.vars["HOME"], ".claude", "skills", "review")
	text := "Use the skill \"review\".\n<skill name=\"review\" source=\"user\" dir=\"" + dir + "\">\nRegardez le diff.\n</skill>\nUser request: main.go"
	var body struct {
		Messages []struct{ Role, Content string } `json:"messages"`
	}
	if err := json.Unmarshal([]byte(reqs[0]), &body); err != nil {
		t.Fatal(err)
	}
	last := body.Messages[len(body.Messages)-1]
	if last.Role != "user" || last.Content != text {
		t.Fatalf("message utilisateur : %q (attendu %q)", last.Content, text)
	}
	ents, err := e.sessionFiles()
	if err != nil || len(ents) != 1 {
		t.Fatalf("sessions : %v %v", ents, err)
	}
	raw, err := os.ReadFile(filepath.Join(e.sessionsDir(), ents[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ev := range decodeEvents(t, string(raw)) {
		var p event.UserMessagePayload
		if ev.Type == event.UserMessage && json.Unmarshal(ev.Payload, &p) == nil {
			found = true
			if p.Skill != "review" || p.Typed != "/review main.go" || p.SkillSHA256 == "" {
				t.Fatalf("UserMessage : %+v", p)
			}
		}
	}
	if !found {
		t.Fatal("aucun UserMessage")
	}
}

func TestSlashUnknownHeadless(t *testing.T) {
	f := newFakeLLM(t, sseText("ok"))
	e := skillEnv(t, f)
	code, _, errb := e.run("-p", "/nope", "--lang", "en")
	if code != 1 || !strings.Contains(errb, "unknown skill or command: /nope") || len(f.requests()) != 0 {
		t.Fatalf("code=%d stderr=%q requêtes=%d", code, errb, len(f.requests()))
	}
	if ents, _ := e.sessionFiles(); len(ents) != 0 {
		t.Fatalf("session créée : %v", ents)
	}
	if _, err := os.Stat(filepath.Join(e.dataDir, "audit")); err == nil && e.auditContents() != "" {
		t.Fatal("audit écrit")
	}
	// Sans clé de fournisseur : même message.
	delete(e.vars, "TEST_KEY")
	code, _, errb = e.run("-p", "/nope", "--lang", "en")
	if code != 1 || !strings.Contains(errb, "unknown skill or command: /nope") {
		t.Fatalf("sans clé : code=%d stderr=%q", code, errb)
	}
}

func TestSkillsListSanitised(t *testing.T) {
	e := newEnv(t, nil, "eu")
	e.writeFile(filepath.Join(e.wd, ".tenon", "skills", "evil", "SKILL.md"),
		"---\nname: evil\ndescription: joli \x1b[31mrouge \u202efin\n---\ncorps\n")
	code, out, _ := e.run("skills")
	if code != 0 || !strings.Contains(out, "evil  project") {
		t.Fatalf("code=%d out=%q", code, out)
	}
	if strings.ContainsAny(out, "\x1b\u202e") {
		t.Fatalf("caractères dangereux : %q", out)
	}
}

func TestSlashBuiltinAndEscapeHeadless(t *testing.T) {
	f := newFakeLLM(t, sseText("ok"))
	e := skillEnv(t, f)
	if code, _, errb := e.run("-p", "/help"); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	if code, _, errb := e.run("-p", "//review"); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	reqs := f.requests()
	if len(reqs) != 2 || !strings.Contains(reqs[0], `"content":"/help"`) || !strings.Contains(reqs[1], `"content":"/review"`) {
		t.Fatalf("requêtes : %q", reqs)
	}
}

func TestSkillsInSystemPrompt(t *testing.T) {
	f := newFakeLLM(t, sseText("ok"))
	e := skillEnv(t, f)
	if code, _, errb := e.run("-p", "salut"); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	reqs := f.requests()
	if len(reqs) != 1 || !strings.Contains(reqs[0], "## Skills disponibles") || !strings.Contains(reqs[0], "- review: Relit un diff") {
		t.Fatalf("prompt système sans skills : %q", reqs)
	}
}

func TestClaudeSkillCompat(t *testing.T) {
	f := newFakeLLM(t, sseCall("shell", `{"command":"./scripts/check.sh"}`), sseText("fini"))
	e := newEnv(t, f, "eu")
	dst := filepath.Join(e.vars["HOME"], ".claude", "skills", "claude-skill")
	src := "testdata/claude-skill"
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o700)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o700)
	})
	if err != nil {
		t.Fatal(err)
	}
	code, out, _ := e.run("skills")
	if code != 0 || !strings.Contains(out, "claude-skill  user  ") || !strings.Contains(out, "allowed-tools: Bash(./scripts/check.sh)") {
		t.Fatalf("code=%d out=%q", code, out)
	}
	// allowed-tools n'accorde rien : l'appel shell du modèle est refusé (pas
	// d'approbation en mode non interactif).
	code, out, errb := e.run("-p", "vérifie", "--output", "json")
	if code != 3 {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	for _, ev := range decodeEvents(t, out) {
		var p event.ToolResultPayload
		if ev.Type == event.ToolResult && json.Unmarshal(ev.Payload, &p) == nil && p.Tool == "shell" {
			if p.Denied == "" || strings.Contains(p.Content, "check-ok") {
				t.Fatalf("shell non refusé : %+v", p)
			}
			return
		}
	}
	t.Fatal("aucun ToolResult shell")
}

func TestSlashBuiltinWithWhitespaceHeadless(t *testing.T) {
	f := newFakeLLM(t, sseText("ok"))
	e := skillEnv(t, f)
	for _, p := range []string{"/help\tx", "/skills\nfoo"} {
		if code, _, errb := e.run("-p", p); code != 0 {
			t.Fatalf("%q: code=%d stderr=%q", p, code, errb)
		}
	}
	reqs := f.requests()
	if len(reqs) != 2 || !strings.Contains(reqs[0], `"content":"/help\tx"`) || !strings.Contains(reqs[1], `"content":"/skills\nfoo"`) {
		t.Fatalf("requêtes : %q", reqs)
	}
}
