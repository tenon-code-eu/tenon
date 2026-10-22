//go:build linux

package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestMain : le binaire de test sert d'assistant `__sandbox` (os.Executable).
func TestMain(m *testing.M) {
	MaybeMain()
	// Client de test sans dépendance : connexion à une socket Unix.
	if addr, ok := os.LookupEnv(unixClientEnv); ok {
		c, err := net.Dial("unix", addr)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		_ = c.Close()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

const unixClientEnv = "TENON_TEST_UNIX_CONNECT"

func needSandbox(t *testing.T) {
	t.Helper()
	if !Probe().Available {
		t.Skipf("landlock indisponible: %s", Probe().Reason)
	}
}

func realTemp(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func runWrapped(t *testing.T, s Spec, name string, args ...string) error {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = s.Dir
	if err := Wrap(cmd, s); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		t.Logf("%s %q: %v; stderr: %s", name, args, err, stderr.String())
	}
	return err
}

func fakeSpec(t *testing.T, proj, home string) Spec {
	t.Helper()
	return Spec{
		Dir:      proj,
		Write:    DefaultWrite(proj, home, func(string) string { return "" }, "linux"),
		DenyRead: DefaultDenyRead(home, filepath.Join(home, ".config", "tenon"), filepath.Join(home, ".local", "share", "tenon"), ""),
	}
}

func TestLinuxWriteOutsideRefused(t *testing.T) {
	needSandbox(t)
	proj, home, other := realTemp(t), realTemp(t), realTemp(t)
	// other est sous /tmp, inscriptible par défaut : on restreint Write au projet.
	s := fakeSpec(t, proj, home)
	s.Write = []string{proj}
	out := filepath.Join(other, "f")
	if err := runWrapped(t, s, "/bin/sh", "-c", "echo x > "+out); err == nil {
		t.Fatal("écriture hors projet acceptée")
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatal("fichier créé hors projet")
	}
	in := filepath.Join(proj, "f")
	if err := runWrapped(t, s, "/bin/sh", "-c", "echo x > "+in); err != nil {
		t.Fatalf("écriture dans le projet refusée: %v", err)
	}
	if _, err := os.Stat(in); err != nil {
		t.Fatal(err)
	}
	// /dev/null reste inscriptible (règle sur un fichier, pas un dossier).
	s.Write = append(s.Write, "/dev/null")
	if err := runWrapped(t, s, "/bin/sh", "-c", "echo x > /dev/null"); err != nil {
		t.Fatalf("écriture sur /dev/null refusée: %v", err)
	}
}

func TestLinuxDenyRead(t *testing.T) {
	needSandbox(t)
	proj, home := realTemp(t), realTemp(t)
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o755); err != nil {
		t.Fatal(err)
	}
	id := filepath.Join(home, ".ssh", "id")
	if err := os.WriteFile(id, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	inner := filepath.Join(proj, "secrets")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	innerF := filepath.Join(inner, "k")
	if err := os.WriteFile(innerF, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := fakeSpec(t, proj, home)
	s.DenyRead = append(s.DenyRead, inner)
	if err := runWrapped(t, s, "/bin/cat", id); err == nil {
		t.Fatal("lecture de ~/.ssh/id acceptée")
	}
	if err := runWrapped(t, s, "/bin/cat", innerF); err == nil {
		t.Fatal("lecture du dossier interdit du projet acceptée")
	}
	if err := runWrapped(t, s, "/bin/sh", "-c", "echo x > "+filepath.Join(inner, "new")); err == nil {
		t.Fatal("écriture dans le dossier interdit du projet acceptée")
	}
	// Un lien du projet vers ~/.ssh n'ouvre pas la cible.
	link := filepath.Join(proj, "lien")
	if err := os.Symlink(filepath.Join(home, ".ssh"), link); err != nil {
		t.Fatal(err)
	}
	if err := runWrapped(t, s, "/bin/cat", filepath.Join(link, "id")); err == nil {
		t.Fatal("lecture via un lien vers ~/.ssh acceptée")
	}
	ok := filepath.Join(proj, "ok")
	if err := os.WriteFile(ok, []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runWrapped(t, s, "/bin/cat", ok); err != nil {
		t.Fatalf("lecture normale refusée: %v", err)
	}
	// Les ancêtres des interdits restent listables (READ_DIR seul) ; le
	// contenu des fichiers interdits reste illisible.
	for _, d := range []string{"/", home, proj} {
		if err := runWrapped(t, s, "/bin/ls", d); err != nil {
			t.Fatalf("ls %s refusé: %v", d, err)
		}
	}
	if err := runWrapped(t, s, "/bin/sh", "-c", "echo "+proj+"/*"); err != nil {
		t.Fatalf("glob du projet refusé: %v", err)
	}
	if err := runWrapped(t, s, "/bin/cat", id); err == nil {
		t.Fatal("lecture de ~/.ssh/id acceptée après ls")
	}
	// Limite Landlock : le dossier qui contient un interdit n'est pas accordé
	// en entier (une règle couvrirait l'interdit) ; on écrit donc dans un
	// fichier existant et dans un sous-dossier voisin.
	sub := filepath.Join(proj, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := runWrapped(t, s, "/bin/sh", "-c", "echo y > "+ok+" && echo y > "+filepath.Join(sub, "w")); err != nil {
		t.Fatalf("écriture normale refusée: %v", err)
	}
}

func TestLinuxNetwork(t *testing.T) {
	needSandbox(t)
	if Probe().Network == "none" {
		t.Skipf("réseau non isolable: %s", Probe().Reason)
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash absent")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	defer srv.Close()
	_, port, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	script := "exec 3<>/dev/tcp/127.0.0.1/" + port
	proj, home := realTemp(t), realTemp(t)
	s := fakeSpec(t, proj, home)
	t.Logf("chemin réseau: %s", Probe().Network)
	if err := runWrapped(t, s, "bash", "-c", script); err == nil {
		t.Fatal("réseau ouvert alors que fermé")
	}
	s.Network = true
	if err := runWrapped(t, s, "bash", "-c", script); err != nil {
		t.Fatalf("réseau refusé alors qu'ouvert: %v", err)
	}
}

func TestLinuxBadSpec(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "__sandbox", "--", "/bin/true")
	cmd.Env = append(os.Environ(), SpecEnv+"=not-json")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err = cmd.Run()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 126 {
		t.Fatalf("code inattendu: %v", err)
	}
	if !strings.HasPrefix(stderr.String(), "sandbox: ") {
		t.Fatalf("stderr inattendu: %q", stderr.String())
	}
}

func TestLinuxSpecNotLeaked(t *testing.T) {
	needSandbox(t)
	proj, home := realTemp(t), realTemp(t)
	s := fakeSpec(t, proj, home)
	cmd := exec.Command("env")
	cmd.Dir = proj
	if err := Wrap(cmd, s); err != nil {
		t.Fatal(err)
	}
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), SpecEnv) {
		t.Fatalf("spec visible dans l'environnement: %s", out)
	}
}

func TestLinuxTimeoutKillsGroup(t *testing.T) {
	needSandbox(t)
	proj, home := realTemp(t), realTemp(t)
	s := fakeSpec(t, proj, home)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-c", "sleep 30 & sleep 30")
	cmd.Dir = proj
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	if err := Wrap(cmd, s); err != nil {
		t.Fatal(err)
	}
	if !cmd.SysProcAttr.Setpgid {
		t.Fatal("Setpgid perdu")
	}
	start := time.Now()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pgid := cmd.Process.Pid
	_ = cmd.Wait()
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("délai dépassé: %v", d)
	}
	// Laisse au noyau le temps de terminer les membres du groupe.
	deadline := time.Now().Add(time.Second)
	for {
		alive := groupMembers(t, pgid)
		if len(alive) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("survivants dans le groupe %d: %v", pgid, alive)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// groupMembers parcourt /proc et renvoie les processus vivants (hors zombies)
// du groupe pgid.
func groupMembers(t *testing.T, pgid int) []int {
	t.Helper()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		t.Fatal(err)
	}
	var out []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err != nil {
			continue
		}
		// Après le nom entre parenthèses : état, ppid, pgrp.
		i := bytes.LastIndexByte(b, ')')
		if i < 0 {
			continue
		}
		f := strings.Fields(string(b[i+1:]))
		if len(f) < 3 || f[0] == "Z" {
			continue
		}
		if g, _ := strconv.Atoi(f[2]); g == pgid {
			out = append(out, pid)
		}
	}
	return out
}

func TestLinuxWrapArgs(t *testing.T) {
	proj := realTemp(t)
	cmd := exec.Command("/bin/echo", "a", "b")
	cmd.Dir = proj
	cmd.Env = []string{"A=1", SpecEnv + "=injecte"}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	cmd.WaitDelay = 3 * time.Second
	if err := Wrap(cmd, Spec{Dir: proj, Write: []string{proj}, TCP: true, Network: true}); err != nil {
		t.Fatal(err)
	}
	exe, _ := os.Executable()
	want := []string{exe, "__sandbox", "--", "/bin/echo", "a", "b"}
	if cmd.Path != exe || strings.Join(cmd.Args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("argv inattendu: %q %q", cmd.Path, cmd.Args)
	}
	if cmd.Dir != proj || cmd.WaitDelay != 3*time.Second || !cmd.SysProcAttr.Setpgid || cmd.SysProcAttr.Pdeathsig != syscall.SIGKILL {
		t.Fatalf("champs modifiés: %+v", cmd)
	}
	var specs []string
	for _, kv := range cmd.Env {
		if strings.HasPrefix(kv, SpecEnv+"=") {
			specs = append(specs, strings.TrimPrefix(kv, SpecEnv+"="))
		}
	}
	if len(specs) != 1 || cmd.Env[0] != "A=1" {
		t.Fatalf("environnement inattendu: %q", cmd.Env)
	}
	var got Spec
	if err := json.Unmarshal([]byte(specs[0]), &got); err != nil {
		t.Fatal(err)
	}
	// Réseau ouvert : TCP fourni par l'appelant ignoré, pas de namespace.
	if got.TCP || !got.Network || cmd.SysProcAttr.Cloneflags != 0 {
		t.Fatalf("réseau inattendu: %+v flags=%x", got, cmd.SysProcAttr.Cloneflags)
	}
}

func TestLinuxWrapNetworkClosed(t *testing.T) {
	proj := realTemp(t)
	cmd := exec.Command("/bin/true")
	if err := Wrap(cmd, Spec{Dir: proj, Write: []string{proj}}); err != nil {
		t.Fatal(err)
	}
	var got Spec
	for _, kv := range cmd.Env {
		if v, ok := strings.CutPrefix(kv, SpecEnv+"="); ok {
			if err := json.Unmarshal([]byte(v), &got); err != nil {
				t.Fatal(err)
			}
		}
	}
	ns := cmd.SysProcAttr != nil && cmd.SysProcAttr.Cloneflags&(syscall.CLONE_NEWUSER|syscall.CLONE_NEWNET) == syscall.CLONE_NEWUSER|syscall.CLONE_NEWNET
	switch Probe().Network {
	case "full":
		if !ns || got.TCP || len(cmd.SysProcAttr.UidMappings) != 1 || cmd.SysProcAttr.UidMappings[0].HostID != os.Getuid() || cmd.SysProcAttr.GidMappingsEnableSetgroups {
			t.Fatalf("namespace attendu: %+v tcp=%v", cmd.SysProcAttr, got.TCP)
		}
	case "tcp":
		if ns || !got.TCP {
			t.Fatalf("repli TCP attendu: tcp=%v ns=%v", got.TCP, ns)
		}
	default:
		if ns || got.TCP {
			t.Fatalf("aucune isolation réseau attendue: tcp=%v ns=%v", got.TCP, ns)
		}
	}
}

func TestProbeLinux(t *testing.T) {
	resetProbe()
	a := Probe()
	if b := Probe(); a != b {
		t.Fatalf("sonde instable: %+v / %+v", a, b)
	}
	t.Logf("sonde: %+v", a)
	if !a.Available {
		if a.Mechanism != "none" || a.Reason == "" {
			t.Fatalf("statut inattendu: %+v", a)
		}
		t.Skipf("landlock indisponible: %s", a.Reason)
	}
	if a.Mechanism != "landlock" {
		t.Fatalf("mécanisme inattendu: %+v", a)
	}
	switch a.Network {
	case "full":
	case "tcp", "none":
		if a.Reason == "" {
			t.Fatalf("raison manquante: %+v", a)
		}
	default:
		t.Fatalf("réseau inattendu: %+v", a)
	}
}

// unixServer écoute sur une socket Unix et accepte en boucle.
func unixServer(t *testing.T, addr string) {
	t.Helper()
	l, err := net.Listen("unix", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
}

// runUnixClient lance le binaire de test, confiné, en client de addr.
func runUnixClient(t *testing.T, s Spec, addr string) error {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe)
	cmd.Dir = s.Dir
	cmd.Env = append(os.Environ(), unixClientEnv+"="+addr)
	if err := Wrap(cmd, s); err != nil {
		t.Fatal(err)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("client %s: %v; %s", addr, err, out)
	}
	return err
}

func TestLinuxUnixSocketPath(t *testing.T) {
	needSandbox(t)
	abi, _ := landlockABI()
	proj, home, outside := realTemp(t), realTemp(t), realTemp(t)
	s := fakeSpec(t, proj, home)
	s.Write = []string{proj}
	s.Network = true // isole l'effet de RESOLVE_UNIX du réglage réseau
	in := filepath.Join(proj, "in.sock")
	unixServer(t, in)
	if err := runUnixClient(t, s, in); err != nil {
		t.Fatalf("socket dans une zone inscriptible refusée: %v", err)
	}
	if abi < 9 {
		t.Skipf("landlock ABI %d < 9 : LANDLOCK_ACCESS_FS_RESOLVE_UNIX absent", abi)
	}
	out := filepath.Join(outside, "out.sock")
	unixServer(t, out)
	if err := runUnixClient(t, s, out); err == nil {
		t.Fatal("socket hors zone inscriptible accessible")
	}
}

func TestLinuxAbstractUnixScoped(t *testing.T) {
	needSandbox(t)
	abi, _ := landlockABI()
	if Probe().Network != "tcp" {
		t.Skipf("réseau %q : la portée abstraite ne sert qu'au repli TCP", Probe().Network)
	}
	if abi < 6 {
		t.Skipf("landlock ABI %d < 6 : LANDLOCK_SCOPE_ABSTRACT_UNIX_SOCKET absent", abi)
	}
	proj, home := realTemp(t), realTemp(t)
	s := fakeSpec(t, proj, home)
	addr := "@tenon-test-" + strconv.Itoa(os.Getpid())
	unixServer(t, addr)
	if err := runUnixClient(t, s, addr); err == nil {
		t.Fatal("socket abstraite accessible avec réseau fermé")
	}
	s.Network = true
	if err := runUnixClient(t, s, addr); err != nil {
		t.Fatalf("socket abstraite refusée avec réseau ouvert: %v", err)
	}
}
