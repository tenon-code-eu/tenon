package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/session"
)

const allowWrite = `
[[permissions.rules]]
tool = "write_file"
decision = "allow"
`

func TestRewindListE2E(t *testing.T) {
	f := newFakeLLM(t, sseText("un"), sseText("deux"))
	e := newEnv(t, f, "eu")
	if code, _, errb := e.run("-p", "premier prompt"); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	if code, _, errb := e.run("-p", "second prompt", "--resume", "last"); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	code, out, errb := e.run("rewind", "--list", "--resume", "last", "--lang", "en")
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "#2  ") || !strings.HasSuffix(lines[0], "second prompt") ||
		!strings.HasPrefix(lines[1], "#1  ") || !strings.HasSuffix(lines[1], "premier prompt") {
		t.Fatalf("out=%q", out)
	}
}

func TestRewindListNeedsFlags(t *testing.T) {
	e := newEnv(t, nil, "eu")
	if code, _, errb := e.run("rewind", "--resume", "last"); code != 1 || !strings.Contains(errb, "inattendu") {
		t.Fatalf("sans --list : code=%d stderr=%q", code, errb)
	}
	if code, _, errb := e.run("rewind", "--list"); code != 1 || errb == "" {
		t.Fatalf("sans --resume : code=%d stderr=%q", code, errb)
	}
	if code, _, errb := e.run("rewind", "--list", "--resume", "last"); code != 1 || !strings.Contains(errb, "session") {
		t.Fatalf("sans session : code=%d stderr=%q", code, errb)
	}
}

func TestCheckpointSavedE2E(t *testing.T) {
	f := newFakeLLM(t, sseCall("write_file", `{"path":"x.txt","content":"nouveau"}`), sseText("ok"))
	e := newEnvExtra(t, f, "eu", allowWrite)
	e.writeFile(filepath.Join(e.wd, "x.txt"), "ancien contenu")
	if code, _, errb := e.run("-p", "edite"); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	found := false
	_ = filepath.WalkDir(filepath.Join(e.dataDir, "checkpoints"), func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if b, _ := os.ReadFile(p); string(b) == "ancien contenu" {
				found = true
			}
		}
		return nil
	})
	if !found {
		t.Fatal("copie d'avant écriture absente de checkpoints/")
	}
}

func TestCheckpointsDisabledE2E(t *testing.T) {
	f := newFakeLLM(t, sseText("ok"))
	e := newEnvExtra(t, f, "eu", "\n[checkpoints]\nretention_days = 0\n")
	code, _, errb := e.run("-p", "salut", "--lang", "en")
	if code != 0 || !strings.Contains(errb, "checkpoints disabled (retention_days = 0)") {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	if _, err := os.Stat(filepath.Join(e.dataDir, "checkpoints")); !os.IsNotExist(err) {
		t.Fatalf("checkpoints/ ne devrait pas exister : %v", err)
	}
}

// toolResults relit les ToolResult de toutes les sessions de e.
func toolResults(t *testing.T, e *env) []event.ToolResultPayload {
	t.Helper()
	ents, err := os.ReadDir(e.sessionsDir())
	if err != nil {
		t.Fatal(err)
	}
	var out []event.ToolResultPayload
	for _, de := range ents {
		id, ok := strings.CutSuffix(de.Name(), ".jsonl")
		if !ok {
			continue
		}
		evs, _, err := session.Load(e.sessionsDir(), id)
		if err != nil {
			t.Fatal(err)
		}
		for _, ev := range evs {
			if ev.Type != event.ToolResult {
				continue
			}
			var p event.ToolResultPayload
			if err := json.Unmarshal(ev.Payload, &p); err != nil {
				t.Fatal(err)
			}
			out = append(out, p)
		}
	}
	return out
}

func TestCheckpointsDisabledWritesNoCheckpointE2E(t *testing.T) {
	f := newFakeLLM(t, sseCall("write_file", `{"path":"x.txt","content":"nouveau"}`), sseText("ok"))
	e := newEnvExtra(t, f, "eu", allowWrite+"\n[checkpoints]\nretention_days = 0\n")
	e.writeFile(filepath.Join(e.wd, "x.txt"), "ancien")
	if code, _, errb := e.run("-p", "edite"); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	rs := toolResults(t, e)
	if len(rs) != 1 || rs[0].Tool != "write_file" || !rs[0].NoCheckpoint || rs[0].HashBefore == "" {
		t.Fatalf("ToolResult %+v", rs)
	}
}

func TestCheckpointsUnavailableE2E(t *testing.T) {
	f := newFakeLLM(t, sseCall("write_file", `{"path":"x.txt","content":"nouveau"}`), sseText("ok"))
	e := newEnvExtra(t, f, "eu", allowWrite)
	// Un fichier à la place du dossier : Open échoue.
	e.writeFile(filepath.Join(e.dataDir, "checkpoints"), "pas un dossier")
	e.writeFile(filepath.Join(e.wd, "x.txt"), "ancien")
	code, _, errb := e.run("-p", "edite", "--lang", "en")
	if code != 0 || !strings.Contains(errb, "checkpoints unavailable: ") || !strings.Contains(errb, ": /rewind cannot restore files") {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	if strings.Contains(errb, "checkpoint cleanup failed") {
		t.Fatalf("échec d'ouverture signalé comme nettoyage : %q", errb)
	}
	if rs := toolResults(t, e); len(rs) != 1 || !rs[0].NoCheckpoint {
		t.Fatalf("ToolResult %+v", rs)
	}
}

func TestKeepHashes(t *testing.T) {
	f := newFakeLLM(t, sseCall("write_file", `{"path":"x.txt","content":"nouveau"}`), sseText("ok"))
	e := newEnvExtra(t, f, "eu", allowWrite)
	e.writeFile(filepath.Join(e.wd, "x.txt"), "ancien")
	if code, _, errb := e.run("-p", "edite"); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	rs := toolResults(t, e)
	if len(rs) != 1 || rs[0].HashBefore == "" {
		t.Fatalf("ToolResult %+v", rs)
	}
	want := strings.TrimPrefix(rs[0].HashBefore, "sha256:")
	// Fichiers ignorés : autre extension, dossier, session illisible.
	e.writeFile(filepath.Join(e.sessionsDir(), "notes.txt"), "x")
	e.writeFile(filepath.Join(e.sessionsDir(), "bad.jsonl"), "pas du json\n")
	if err := os.Mkdir(filepath.Join(e.sessionsDir(), "d.jsonl"), 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	keep := keepHashes(e.sessionsDir(), 30, now)
	if len(keep) != 1 || !keep[want] {
		t.Fatalf("keep %v, attendu %s", keep, want)
	}
	// Une session plus vieille que la rétention ne protège plus ses copies.
	if keep := keepHashes(e.sessionsDir(), 30, now.Add(31*24*time.Hour)); len(keep) != 0 {
		t.Fatalf("session périmée conservée : %v", keep)
	}
	if keep := keepHashes(filepath.Join(e.dataDir, "absent"), 30, now); len(keep) != 0 {
		t.Fatalf("dossier absent : %v", keep)
	}
}
