package main

import (
	"bytes"
	"context"
	"testing"
)

func TestVersionFlag(t *testing.T) {
	var out, errb bytes.Buffer
	code := run(context.Background(), []string{"--version"}, &out, &errb, func(string) string { return "" }, t.TempDir())
	if code != 0 || out.String() != "tenon dev\n" {
		t.Fatalf("code=%d out=%q", code, out.String())
	}
}

func TestUnknownFlag(t *testing.T) {
	var out, errb bytes.Buffer
	code := run(context.Background(), []string{"--nope"}, &out, &errb, func(string) string { return "" }, t.TempDir())
	if code != 1 || errb.Len() == 0 {
		t.Fatalf("code=%d stderr=%q", code, errb.String())
	}
}
