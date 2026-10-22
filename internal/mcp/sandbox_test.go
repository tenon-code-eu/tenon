package mcp

import (
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/tenon-code-eu/tenon/internal/config"
	"github.com/tenon-code-eu/tenon/internal/event"
	"github.com/tenon-code-eu/tenon/internal/sandbox"
)

// captureWrap remplace l'isolation par un relevé des Spec reçues ; la
// commande n'est pas modifiée (le serveur de test tourne sans confinement).
func captureWrap(t *testing.T) *[]sandbox.Spec {
	t.Helper()
	var got []sandbox.Spec
	old := wrapCommand
	wrapCommand = func(_ *exec.Cmd, s sandbox.Spec) error {
		got = append(got, s)
		return nil
	}
	t.Cleanup(func() { wrapCommand = old })
	return &got
}

func TestStdioSandboxRisk(t *testing.T) {
	for _, tc := range []struct {
		risk     string
		write    bool // projet inscriptible
		network  bool
		netOpen  bool // isolation réseau absente (sonde "none")
		wantNetU bool // SandboxUse.Network attendu
	}{
		{risk: "read"},
		{risk: ""},
		{risk: "network", network: true, wantNetU: true},
		{risk: "write", write: true},
		{risk: "exec", write: true},
		{risk: "read", netOpen: true, wantNetU: true},
	} {
		t.Run(tc.risk, func(t *testing.T) {
			specs := captureWrap(t)
			srv, getenv := stdioServer(t, "srv", "", nil)
			srv.Risk = tc.risk
			root := t.TempDir()
			base := &sandbox.Spec{Dir: "/ignore", Write: []string{"/tmp"}, DenyRead: []string{"/secret"}}
			m, down := start(t, []config.MCPServer{srv}, Options{Getenv: getenv, Root: root, Sandbox: base, NetworkOpen: tc.netOpen})
			if len(down) != 0 {
				t.Fatalf("down %v", down)
			}
			if len(*specs) != 1 {
				t.Fatalf("%d Spec(s)", len(*specs))
			}
			s := (*specs)[0]
			if slices.Contains(s.Write, root) != tc.write || s.Network != tc.network {
				t.Fatalf("spec %+v", s)
			}
			// Dossier de travail : HOME, jamais rendu inscriptible.
			home := getenv("HOME")
			if s.Dir != home || slices.Contains(s.Write, home) || !slices.Contains(s.Write, "/tmp") || !slices.Equal(s.DenyRead, []string{"/secret"}) {
				t.Fatalf("spec %+v", s)
			}
			// La Spec de base n'est pas modifiée.
			if !slices.Equal(base.Write, []string{"/tmp"}) || base.Network || base.Dir != "/ignore" {
				t.Fatalf("base modifiée : %+v", base)
			}
			if u := m.SandboxUse("srv"); u == nil || *u != (event.SandboxUse{Confined: true, Network: tc.wantNetU}) {
				t.Fatalf("SandboxUse %+v", u)
			}
		})
	}
}

// Sans Spec de base : serveur non confiné, réseau ouvert (journal honnête).
func TestStdioSandboxNone(t *testing.T) {
	specs := captureWrap(t)
	srv, getenv := stdioServer(t, "srv", "", nil)
	m, down := start(t, []config.MCPServer{srv}, Options{Getenv: getenv})
	if len(down) != 0 || len(*specs) != 0 {
		t.Fatalf("down %v specs %v", down, *specs)
	}
	if u := m.SandboxUse("srv"); u == nil || *u != (event.SandboxUse{Network: true}) {
		t.Fatalf("SandboxUse %+v", u)
	}
	if u := m.SandboxUse("absent"); u != nil {
		t.Fatalf("SandboxUse absent %+v", u)
	}
}

// Isolation réelle : le serveur stdio démarre confiné et répond ; la Spec
// interne ne lui parvient pas.
func TestStdioSandboxReal(t *testing.T) {
	st := sandbox.Probe()
	if !st.Available {
		t.Skipf("isolation indisponible : %s", st.Reason)
	}
	srv, getenv := stdioServer(t, "srv", "", nil)
	srv.Risk = "read"
	root := t.TempDir()
	base := &sandbox.Spec{
		Write:    sandbox.DefaultWrite("", getenv("HOME"), getenv, runtime.GOOS),
		DenyRead: sandbox.DefaultDenyRead(getenv("HOME"), "", "", ""),
	}
	m, down := start(t, []config.MCPServer{srv}, Options{Getenv: getenv, Root: root, Sandbox: base})
	if len(down) != 0 {
		t.Fatalf("down %v", down)
	}
	out := run(t, toolByName(t, m, "mcp__srv__env"), `{}`).Content
	if !strings.Contains(out, "\nPATH=") || strings.Contains(out, sandbox.SpecEnv) {
		t.Fatalf("env :\n%s", out)
	}
	if u := m.SandboxUse("srv"); u == nil || !u.Confined {
		t.Fatalf("SandboxUse %+v", u)
	}
}
