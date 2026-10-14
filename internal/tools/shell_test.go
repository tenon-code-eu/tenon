//go:build unix

package tools

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newShell(root string, timeout time.Duration) Tool { return Shell(root, timeout, 10000) }

func runShell(t *testing.T, tl Tool, args string) Result {
	t.Helper()
	res, err := tl.Run(context.Background(), []byte(args))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return res
}

func TestShellEcho(t *testing.T) {
	res := runShell(t, newShell(t.TempDir(), 5*time.Second), `{"command":"echo bonjour"}`)
	if res.IsError || !strings.Contains(res.Content, "bonjour") || !strings.Contains(res.Content, "[code de sortie: 0]") {
		t.Fatalf("résultat inattendu : %+v", res)
	}
}

func TestShellNonZero(t *testing.T) {
	res := runShell(t, newShell(t.TempDir(), 5*time.Second), `{"command":"exit 3"}`)
	if !res.IsError || !strings.Contains(res.Content, "[code de sortie: 3]") {
		t.Fatalf("résultat inattendu : %+v", res)
	}
}

func TestShellRunsInRoot(t *testing.T) {
	root := t.TempDir()
	want, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	res := runShell(t, newShell(root, 5*time.Second), `{"command":"pwd"}`)
	if !strings.Contains(res.Content, want) {
		t.Fatalf("pwd = %q, attendu %q", res.Content, want)
	}
}

func TestShellTimeoutKillsGroup(t *testing.T) {
	start := time.Now()
	res := runShell(t, newShell(t.TempDir(), time.Second), `{"command":"sleep 30"}`)
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("trop long : %v", d)
	}
	if !res.IsError || !strings.Contains(res.Content, "délai dépassé") {
		t.Fatalf("résultat inattendu : %+v", res)
	}
}

func TestShellTimeoutArgOverrides(t *testing.T) {
	start := time.Now()
	res := runShell(t, newShell(t.TempDir(), time.Minute), `{"command":"sleep 30","timeout_seconds":1}`)
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("trop long : %v", d)
	}
	if !strings.Contains(res.Content, "délai dépassé") {
		t.Fatalf("résultat inattendu : %+v", res)
	}
}

func TestShellInvalidArgs(t *testing.T) {
	tl := newShell(t.TempDir(), time.Second)
	for _, a := range []string{`{"command":"echo x","timeout_seconds":0}`, `{"command":"echo x","timeout_seconds":-2}`, `{}`, `nope`} {
		if res := runShell(t, tl, a); !res.IsError {
			t.Errorf("%s : erreur attendue, obtenu %+v", a, res)
		}
	}
}

func TestShellBackgroundChildDoesNotHang(t *testing.T) {
	start := time.Now()
	res := runShell(t, newShell(t.TempDir(), 10*time.Second), `{"command":"sleep 30 & echo ok"}`)
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("trop long : %v", d)
	}
	if res.IsError || !strings.Contains(res.Content, "ok") || !strings.Contains(res.Content, "[code de sortie: 0]") {
		t.Fatalf("résultat inattendu : %+v", res)
	}
}

func TestShellOutputCapped(t *testing.T) {
	res := runShell(t, Shell(t.TempDir(), time.Second, 1000), `{"command":"yes"}`)
	if len(res.Content) > 1200 || !strings.Contains(res.Content, "sortie tronquée") {
		t.Fatalf("len=%d, contenu inattendu : %.200q", len(res.Content), res.Content)
	}
}

func TestShellCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	_, err := newShell(t.TempDir(), time.Minute).Run(ctx, []byte(`{"command":"sleep 30"}`))
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("trop long : %v", d)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestShellTarget(t *testing.T) {
	tg, err := newShell(t.TempDir(), time.Second).Target([]byte(`{"command":"ls"}`))
	if err != nil || tg.Command != "ls" {
		t.Fatalf("target = %+v, %v", tg, err)
	}
}
