//go:build unix

package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/sandbox"
)

func newShell(root string, timeout time.Duration) Tool {
	return Shell(root, timeout, 10000, ShellOptions{})
}

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
	if res.IsError || !strings.Contains(res.Content, "bonjour") || !strings.Contains(res.Content, "[exit code: 0]") {
		t.Fatalf("résultat inattendu : %+v", res)
	}
}

func TestShellNonZero(t *testing.T) {
	res := runShell(t, newShell(t.TempDir(), 5*time.Second), `{"command":"exit 3"}`)
	if !res.IsError || !strings.Contains(res.Content, "[exit code: 3]") {
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
	if !res.IsError || !strings.Contains(res.Content, "timed out") {
		t.Fatalf("résultat inattendu : %+v", res)
	}
}

func TestShellTimeoutArgOverrides(t *testing.T) {
	start := time.Now()
	res := runShell(t, newShell(t.TempDir(), time.Minute), `{"command":"sleep 30","timeout_seconds":1}`)
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("trop long : %v", d)
	}
	if !strings.Contains(res.Content, "timed out") {
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
	if res.IsError || !strings.Contains(res.Content, "ok") || !strings.Contains(res.Content, "[exit code: 0]") {
		t.Fatalf("résultat inattendu : %+v", res)
	}
}

func TestShellOutputCapped(t *testing.T) {
	res := runShell(t, Shell(t.TempDir(), time.Second, 1000, ShellOptions{}), `{"command":"yes"}`)
	if len(res.Content) > 1200 || !strings.Contains(res.Content, "output truncated") {
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

func TestShellEnvFiltered(t *testing.T) {
	t.Setenv("MISTRAL_API_KEY", "secret-du-parent")
	tl := Shell(t.TempDir(), 5*time.Second, 10000, ShellOptions{Env: []string{"A=1"}})
	res := runShell(t, tl, `{"command":"env"}`)
	if res.IsError || strings.Contains(res.Content, "MISTRAL_API_KEY") || strings.Contains(res.Content, "secret-du-parent") {
		t.Fatalf("environnement non filtré : %q", res.Content)
	}
	body, _, _ := strings.Cut(res.Content, "\n[exit code:")
	var got []string
	for _, l := range strings.Split(strings.TrimSpace(body), "\n") {
		name, _, _ := strings.Cut(l, "=")
		switch name {
		case "PWD", "SHLVL", "_": // ajoutées par bash
		default:
			got = append(got, l)
		}
	}
	if !reflect.DeepEqual(got, []string{"A=1"}) {
		t.Fatalf("env = %q, attendu [A=1] (plus PWD, SHLVL, _)", got)
	}
}

func TestShellUnconfinedRecorded(t *testing.T) {
	tl := newShell(t.TempDir(), 5*time.Second)
	res := runShell(t, tl, `{"command":"true"}`)
	// Sans isolation, la commande a toujours le réseau (R12).
	if want := (&event.SandboxUse{Network: true}); !reflect.DeepEqual(res.Sandbox, want) {
		t.Fatalf("Sandbox = %+v, attendu %+v", res.Sandbox, want)
	}
	res, err := tl.Run(WithNetwork(context.Background(), true), []byte(`{"command":"true"}`))
	if err != nil {
		t.Fatal(err)
	}
	if want := (&event.SandboxUse{Network: true}); !reflect.DeepEqual(res.Sandbox, want) {
		t.Fatalf("Sandbox = %+v, attendu %+v", res.Sandbox, want)
	}
}

func TestShellConfined(t *testing.T) {
	if st := sandbox.Probe(); !st.Available {
		t.Skipf("isolation indisponible : %s", st.Reason)
	}
	root, outside := t.TempDir(), t.TempDir()
	tl := Shell(root, 10*time.Second, 10000, ShellOptions{
		Sandbox: &sandbox.Spec{Dir: root, Write: []string{root, "/dev/null"}},
	})

	cmd := `{"command":"echo x > ` + filepath.Join(outside, "f") + `"}`
	res := runShell(t, tl, cmd)
	if !res.IsError {
		t.Fatalf("écriture hors projet acceptée : %+v", res)
	}
	if _, err := os.Stat(filepath.Join(outside, "f")); !os.IsNotExist(err) {
		t.Fatalf("fichier hors projet créé : %v", err)
	}
	if want := (&event.SandboxUse{Confined: true}); !reflect.DeepEqual(res.Sandbox, want) {
		t.Fatalf("Sandbox = %+v, attendu %+v", res.Sandbox, want)
	}

	res = runShell(t, tl, `{"command":"echo x > f && cat f"}`)
	if res.IsError || !strings.Contains(res.Content, "x") {
		t.Fatalf("écriture dans le projet refusée : %+v", res)
	}

	res, err := tl.Run(WithNetwork(context.Background(), true), []byte(`{"command":"true"}`))
	if err != nil {
		t.Fatal(err)
	}
	if want := (&event.SandboxUse{Confined: true, Network: true}); res.IsError || !reflect.DeepEqual(res.Sandbox, want) {
		t.Fatalf("résultat %+v, Sandbox = %+v, attendu %+v", res, res.Sandbox, want)
	}
}
