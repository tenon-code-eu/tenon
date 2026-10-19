package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
