package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
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
	if code != 0 || !strings.HasPrefix(out, "hook installed: ") || !strings.Contains(out, "commit-msg") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errb)
	}
	hook := filepath.Join(e.wd, ".git", "hooks", "commit-msg")
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

// agentEnv : dépôt git dont a.go, indexé, a été écrit par l'agent lors d'un
// tour headless.
func agentEnv(t *testing.T) *env {
	t.Helper()
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
	gitIn(t, e.wd, "add", "a.go")
	return e
}

func TestGitMarkProcessTimeout(t *testing.T) {
	e := agentEnv(t)
	release := make(chan struct{})
	defer close(release)
	origF, origT := markFunc, markTimeout
	defer func() { markFunc, markTimeout = origF, origT }()
	// Lecture lente, sourde au contexte.
	markFunc = func(ctx context.Context, msg, dir, sdir string) error {
		<-release
		return nil
	}
	markTimeout = 200 * time.Millisecond

	msg := filepath.Join(t.TempDir(), "MSG")
	e.writeFile(msg, "titre\n")
	start := time.Now()
	code, out, errb := e.run("git", "mark", msg)
	if code != 0 || out != "" || errb != "" || time.Since(start) > 3*time.Second {
		t.Fatalf("code=%d stdout=%q stderr=%q durée=%v", code, out, errb, time.Since(start))
	}
	if b, _ := os.ReadFile(msg); string(b) != "titre\n" {
		t.Fatalf("message modifié : %q", b)
	}
}

func TestGitMarkFIFOInSessions(t *testing.T) {
	e := agentEnv(t)
	if err := syscall.Mkfifo(filepath.Join(e.sessionsDir(), "s_00000000000f.jsonl"), 0o600); err != nil {
		t.Fatal(err)
	}
	msg := filepath.Join(t.TempDir(), "MSG")
	e.writeFile(msg, "titre\n")
	start := time.Now()
	code, _, errb := e.run("git", "mark", msg)
	if code != 0 || time.Since(start) > 6*time.Second {
		t.Fatalf("code=%d stderr=%q durée=%v", code, errb, time.Since(start))
	}
	// La FIFO est ignorée : la session régulière marque le message.
	if b, _ := os.ReadFile(msg); !strings.Contains(string(b), "AI-Session: s_") {
		t.Fatalf("message : %q", b)
	}
}

func TestCommitMsgHookFlows(t *testing.T) {
	e := agentEnv(t)
	if code, _, errb := e.run("git", "install-hook"); code != 0 {
		t.Fatalf("install-hook : code=%d stderr=%q", code, errb)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	e.writeFile(filepath.Join(bin, "tenon"), "#!/bin/sh\nexec \""+self+"\" \"$@\"\n")
	editor := filepath.Join(bin, "editor")
	e.writeFile(editor, "#!/bin/sh\nprintf 'msg\\n' > \"$1\"\n")
	for _, f := range []string{"tenon", "editor"} {
		if err := os.Chmod(filepath.Join(bin, f), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	vars := []string{
		"PATH=" + bin + string(os.PathListSeparator) + filepath.Dir(gitPath),
		"TENON_TEST_CLI=1",
	}
	for _, k := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "HOME"} {
		vars = append(vars, k+"="+e.vars[k])
	}
	gitHook := func(editor string, args ...string) (string, error) {
		cmd := exec.Command("git", args...)
		cmd.Dir = e.wd
		cmd.Env = append(os.Environ(), append(vars, "GIT_EDITOR="+editor)...)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	commit := func(editor string, args ...string) (string, error) {
		return gitHook(editor, append([]string{"commit", "-q"}, args...)...)
	}
	lastMsg := func() string {
		out, err := exec.Command("git", "-C", e.wd, "log", "-1", "--format=%B").Output()
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	stage := func(content string) {
		e.writeFile(filepath.Join(e.wd, "a.go"), content)
		gitIn(t, e.wd, "add", "a.go")
	}
	const trailers = "\n\nAI-Assisted-By: tenon (test-model, Test SAS, FR)\nAI-Session: s_"

	// Éditeur quitté sans rien écrire : le commit est abandonné.
	if out, err := commit("true"); err == nil {
		t.Fatalf("commit vide accepté : %s", out)
	}
	if err := exec.Command("git", "-C", e.wd, "rev-parse", "-q", "--verify", "HEAD").Run(); err == nil {
		t.Fatal("un commit a été créé")
	}

	// Éditeur qui écrit « msg ».
	if out, err := commit(editor); err != nil {
		t.Fatalf("commit : %v\n%s", err, out)
	}
	if m := lastMsg(); !strings.HasPrefix(m, "msg"+trailers) {
		t.Fatalf("message (éditeur) : %q", m)
	}

	// -m
	stage("package a // 2\n")
	if out, err := commit("true", "-m", "deux"); err != nil {
		t.Fatalf("commit -m : %v\n%s", err, out)
	}
	if m := lastMsg(); !strings.HasPrefix(m, "deux"+trailers) {
		t.Fatalf("message (-m) : %q", m)
	}

	// -F
	stage("package a // 3\n")
	file := filepath.Join(t.TempDir(), "MSG")
	e.writeFile(file, "trois\n")
	if out, err := commit("true", "-F", file); err != nil {
		t.Fatalf("commit -F : %v\n%s", err, out)
	}
	if m := lastMsg(); !strings.HasPrefix(m, "trois"+trailers) {
		t.Fatalf("message (-F) : %q", m)
	}

	// Conventional Commits.
	stage("package a // 4\n")
	if out, err := commit("true", "-m", "feat: ajoute x"); err != nil {
		t.Fatalf("commit feat : %v\n%s", err, out)
	}
	if m := lastMsg(); !strings.HasPrefix(m, "feat: ajoute x"+trailers) {
		t.Fatalf("message (feat) : %q", m)
	}
	stage("package a // 5\n")
	if out, err := commit("true", "-m", "fix: y", "-m", "Signed-off-by: A <a@b>"); err != nil {
		t.Fatalf("commit fix : %v\n%s", err, out)
	}
	if m := lastMsg(); !strings.HasPrefix(m, "fix: y\n\nSigned-off-by: A <a@b>\nAI-Assisted-By: tenon (test-model, Test SAS, FR)\nAI-Session: s_") {
		t.Fatalf("message (fix) : %q", m)
	}

	// commit -s, éditeur quitté sans écrire : abandonné.
	stage("package a // 6\n")
	head := func() string {
		out, err := exec.Command("git", "-C", e.wd, "rev-parse", "HEAD").Output()
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	before := head()
	if out, err := commit("true", "-s"); err == nil {
		t.Fatalf("commit -s vide accepté : %s", out)
	}
	if head() != before {
		t.Fatal("un commit a été créé")
	}

	// --amend --no-edit : pas de doublon.
	if out, err := commit("true", "--amend", "--no-edit"); err != nil {
		t.Fatalf("amend : %v\n%s", err, out)
	}
	m := lastMsg()
	if strings.Count(m, "AI-Assisted-By:") != 1 || strings.Count(m, "AI-Session:") != 1 {
		t.Fatalf("message (amend) : %q", m)
	}

	// Merge : la branche side porte un fichier de l'agent (commit marqué),
	// le commit de merge n'est pas marqué.
	gitIn(t, e.wd, "checkout", "-q", "-b", "side")
	stage("package a // 7\n")
	if out, err := commit("true", "-m", "side"); err != nil {
		t.Fatalf("commit side : %v\n%s", err, out)
	}
	if m := lastMsg(); !strings.HasPrefix(m, "side"+trailers) {
		t.Fatalf("message (side) : %q", m)
	}
	gitIn(t, e.wd, "checkout", "-q", "-")
	e.writeFile(filepath.Join(e.wd, "c.txt"), "c\n")
	gitIn(t, e.wd, "add", "c.txt")
	if out, err := commit("true", "-m", "principal"); err != nil {
		t.Fatalf("commit principal : %v\n%s", err, out)
	}
	if out, err := gitHook("true", "merge", "-q", "--no-ff", "side"); err != nil {
		t.Fatalf("merge : %v\n%s", err, out)
	}
	if m := lastMsg(); !strings.HasPrefix(m, "Merge branch 'side'") || strings.Contains(m, "AI-") {
		t.Fatalf("message (merge) : %q", m)
	}
}

func TestGitMarkSkipsConfig(t *testing.T) {
	isolateGit(t)
	e := newEnv(t, nil, "eu")
	gitIn(t, e.wd, "init", "-q")
	// Une FIFO à la place de la configuration du projet bloquerait sa
	// lecture : « git mark » ne lit aucune configuration.
	if err := os.MkdirAll(filepath.Join(e.wd, ".tenon"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(e.wd, ".tenon", "config.toml"), 0o600); err != nil {
		t.Fatal(err)
	}
	origT := markTimeout
	defer func() { markTimeout = origT }()
	markTimeout = 500 * time.Millisecond
	msg := filepath.Join(t.TempDir(), "MSG")
	e.writeFile(msg, "titre\n")
	type res struct {
		code     int
		out, err string
	}
	done := make(chan res, 1)
	go func() {
		var out, errb strings.Builder
		code := run(context.Background(), []string{"git", "mark", msg}, strings.NewReader(""), &out, &errb, func(k string) string { return e.vars[k] }, e.wd, func() bool { return false })
		done <- res{code, out.String(), errb.String()}
	}()
	select {
	case r := <-done:
		if r.code != 0 || r.out != "" || r.err != "" {
			t.Fatalf("code=%d stdout=%q stderr=%q", r.code, r.out, r.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("tenon git mark bloqué par la configuration")
	}
	if b, _ := os.ReadFile(msg); string(b) != "titre\n" {
		t.Fatalf("message modifié : %q", b)
	}
}
