package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tenon-code-eu/tenon/internal/policy"
	"github.com/tenon-code-eu/tenon/internal/tools"
)

func TestVersionFlag(t *testing.T) {
	var out, errb bytes.Buffer
	code := run(context.Background(), []string{"--version"}, nil, &out, &errb, func(string) string { return "" }, t.TempDir(), func() bool { return false })
	if code != 0 || out.String() != "tenon dev\n" {
		t.Fatalf("code=%d out=%q", code, out.String())
	}
}

func TestUnknownFlag(t *testing.T) {
	var out, errb bytes.Buffer
	code := run(context.Background(), []string{"--nope"}, nil, &out, &errb, func(string) string { return "" }, t.TempDir(), func() bool { return false })
	if code != 1 || errb.Len() == 0 {
		t.Fatalf("code=%d stderr=%q", code, errb.String())
	}
}

func TestResumeWithoutPromptNeedsTTY(t *testing.T) {
	var out, errb bytes.Buffer
	code := run(context.Background(), []string{"--resume", "last"}, nil, &out, &errb, func(string) string { return "" }, t.TempDir(), func() bool { return false })
	if code != 1 || !strings.Contains(errb.String(), "the interactive interface needs a terminal") {
		t.Fatalf("code=%d stderr=%q", code, errb.String())
	}
}

func TestSessionsRejectsExtraArgs(t *testing.T) {
	var out, errb bytes.Buffer
	code := run(context.Background(), []string{"sessions", "x"}, nil, &out, &errb, func(string) string { return "" }, t.TempDir(), func() bool { return false })
	if code != 1 {
		t.Fatalf("code=%d stderr=%q", code, errb.String())
	}
}

// Une règle ask du projet prime une allow globale (le projet ne peut que
// restreindre) ; sans elle, l'allow globale s'applique.
func TestLoadSetupProjectAskOverridesGlobalAllow(t *testing.T) {
	xdg, wd := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(xdg, "tenon"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(xdg, "tenon", "config.toml"), []byte("[[permissions.rules]]\ntool = \"shell\"\nmatch = \"*\"\ndecision = \"allow\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(wd, ".tenon"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wd, ".tenon", "config.toml"), []byte("[[permissions.rules]]\ntool = \"shell\"\nmatch = \"git push*\"\ndecision = \"ask\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	getenv := func(k string) string {
		if k == "XDG_CONFIG_HOME" {
			return xdg
		}
		return ""
	}
	s, err := loadSetup(getenv, wd)
	if err != nil {
		t.Fatal(err)
	}
	perms, err := policy.NewPermissions(wd, s.rules)
	if err != nil {
		t.Fatal(err)
	}
	call := func(cmd string) policy.Call {
		return policy.Call{Tool: "shell", Risk: tools.Exec, Target: tools.Target{Command: cmd}}
	}
	if got := perms.Check(call("git push origin main")); got != policy.Ask {
		t.Errorf("git push : %v, attendu ask", got)
	}
	if got := perms.Check(call("ls")); got != policy.Allow {
		t.Errorf("ls : %v, attendu allow", got)
	}
}
