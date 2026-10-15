package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitIn exécute git dans dir, isolé de la configuration de l'utilisateur.
func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v : %v\n%s", args, err, out)
	}
}

func isolateGit(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.invalid")
}

func TestCommitGetsTrailers(t *testing.T) {
	isolateGit(t)
	f := newFakeLLM(t, sseCall("write_file", `{"path":"a.go","content":"package a\n"}`), sseText("fini"))
	e := newEnvExtra(t, f, "eu", `
[[permissions.rules]]
tool = "write_file"
decision = "allow"
`)
	gitIn(t, e.wd, "init", "-q")
	if code, _, errb := e.run("-p", "écris a.go"); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	e.writeFile(filepath.Join(e.wd, "b.go"), "package b\n")
	gitIn(t, e.wd, "add", "a.go", "b.go")

	msg := filepath.Join(t.TempDir(), "COMMIT_EDITMSG")
	e.writeFile(msg, "ajout de a.go\n")
	code, out, errb := e.run("git", "mark", msg)
	if code != 0 || out != "" || errb != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errb)
	}
	b, err := os.ReadFile(msg)
	if err != nil {
		t.Fatal(err)
	}
	want := "ajout de a.go\n\nAI-Assisted-By: tenon (test-model, Test SAS, FR)\nAI-Session: s_"
	if !strings.HasPrefix(string(b), want) {
		t.Fatalf("message : %q", b)
	}
}

func TestGitMarkNeverFails(t *testing.T) {
	isolateGit(t)
	e := newEnv(t, nil, "eu")
	// Hors dépôt git, message absent, arguments en trop : code 0, silence.
	for _, args := range [][]string{
		{"git", "mark", filepath.Join(e.wd, "absent")},
		{"git", "mark"},
		{"git", "mark", "a", "b"},
	} {
		code, out, errb := e.run(args...)
		if code != 0 || out != "" || errb != "" {
			t.Fatalf("%v : code=%d stdout=%q stderr=%q", args, code, out, errb)
		}
	}
	// TENON_DEBUG=1 : l'erreur est écrite, le code reste 0.
	e.vars["TENON_DEBUG"] = "1"
	code, _, errb := e.run("git", "mark", filepath.Join(e.wd, "absent"))
	if code != 0 || errb == "" {
		t.Fatalf("debug : code=%d stderr=%q", code, errb)
	}
}

func TestGitInstallHookCLI(t *testing.T) {
	isolateGit(t)
	e := newEnv(t, nil, "eu")
	gitIn(t, e.wd, "init", "-q")
	code, out, errb := e.run("--lang", "en", "git", "install-hook")
	if code != 0 || !strings.HasPrefix(out, "hook installed: ") || !strings.Contains(out, "prepare-commit-msg") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errb)
	}
	hook := filepath.Join(e.wd, ".git", "hooks", "prepare-commit-msg")
	e.writeFile(hook, "#!/bin/sh\nexit 0\n")
	code, _, errb = e.run("git", "install-hook")
	if code != 1 || !strings.Contains(errb, "--force") {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	if code, _, errb = e.run("git", "install-hook", "--force"); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	if _, err := os.Stat(hook + ".bak"); err != nil {
		t.Fatal(err)
	}
	// Sous-commande inconnue.
	if code, _, _ = e.run("git", "frobnicate"); code != 1 {
		t.Fatalf("code=%d", code)
	}
}
