package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
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
	if code != 1 || !strings.Contains(errb.String(), "l'interface interactive demande un terminal") {
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
