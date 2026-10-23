//go:build darwin

package sandbox

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func needSandbox(t *testing.T) {
	t.Helper()
	if !Probe().Available {
		t.Skip("sandbox-exec indisponible")
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
	return cmd.Run()
}

func fakeSpec(t *testing.T, proj, home string) Spec {
	t.Helper()
	// TMPDIR hors des tests : les dossiers temporaires de test ne sont pas inscriptibles par défaut.
	t.Setenv("TMPDIR", "/nonexistent-tmp")
	return Spec{
		Dir:      proj,
		Write:    DefaultWrite(proj, home, func(string) string { return "" }, "darwin"),
		DenyRead: DefaultDenyRead(home, filepath.Join(home, ".config", "tenon"), filepath.Join(home, ".local", "share", "tenon"), ""),
	}
}

func TestDarwinWriteOutsideRefused(t *testing.T) {
	needSandbox(t)
	proj, home, other := realTemp(t), realTemp(t), realTemp(t)
	// other sous /private/var/folders est inscriptible par défaut ; on l'écrit donc hors de la liste.
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
}

func TestDarwinDenyRead(t *testing.T) {
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
	ok := filepath.Join(proj, "ok")
	if err := os.WriteFile(ok, []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runWrapped(t, s, "/bin/cat", ok); err != nil {
		t.Fatalf("lecture normale refusée: %v", err)
	}
}

func TestDarwinNetwork(t *testing.T) {
	needSandbox(t)
	if _, err := os.Stat("/usr/bin/curl"); err != nil {
		t.Skip("curl absent")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	defer srv.Close()
	proj, home := realTemp(t), realTemp(t)
	s := fakeSpec(t, proj, home)
	if err := runWrapped(t, s, "/usr/bin/curl", "-s", "--max-time", "5", srv.URL); err == nil {
		t.Fatal("réseau ouvert alors que fermé")
	}
	s.Network = true
	if err := runWrapped(t, s, "/usr/bin/curl", "-s", "--max-time", "5", srv.URL); err != nil {
		t.Fatalf("réseau refusé alors qu'ouvert: %v", err)
	}
}

func TestWrapKeepsCmd(t *testing.T) {
	proj := realTemp(t)
	cmd := exec.Command("/bin/echo", "a", "b")
	cmd.Dir = proj
	cmd.Env = []string{"A=1"}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 3 * time.Second
	if err := Wrap(cmd, Spec{Dir: proj, Write: []string{proj}}); err != nil {
		t.Fatal(err)
	}
	if cmd.Dir != proj || len(cmd.Env) != 1 || cmd.Env[0] != "A=1" || cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid || cmd.WaitDelay != 3*time.Second {
		t.Fatalf("champs modifiés: %+v", cmd)
	}
	if cmd.Path != "/usr/bin/sandbox-exec" || cmd.Args[0] != "sandbox-exec" || cmd.Args[1] != "-p" || cmd.Args[3] != "--" || cmd.Args[4] != "/bin/echo" || cmd.Args[6] != "b" {
		t.Fatalf("argv inattendu: %q", cmd.Args)
	}
}

func TestProbeCached(t *testing.T) {
	resetProbe()
	a := Probe()
	if b := Probe(); a != b {
		t.Fatalf("sonde instable: %+v / %+v", a, b)
	}
	if a.Available && (a.Mechanism != "sandbox-exec" || a.Network != "full") {
		t.Fatalf("statut inattendu: %+v", a)
	}
}

// R16 : une commande confinée ne lit pas le trousseau de l'utilisateur, où
// tenon range les clés des fournisseurs (go-keyring passe par
// /usr/bin/security).
func TestDarwinKeychainDenied(t *testing.T) {
	needSandbox(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("dossier personnel inconnu")
	}
	kc := filepath.Join(home, "Library", "Keychains")
	if _, err := os.Stat(kc); err != nil {
		t.Skip("pas de ~/Library/Keychains")
	}
	proj := realTemp(t)
	s := fakeSpec(t, proj, home)
	service := fmt.Sprintf("tenon-sandbox-test-%d-%d", os.Getpid(), time.Now().UnixNano())
	add := exec.Command("/usr/bin/security", "add-generic-password", "-s", service, "-a", "probe", "-w", "secret-test", "-U")
	if err := add.Run(); err != nil {
		// Repli : sans élément de test, le dossier du trousseau doit rester illisible.
		if err := runWrapped(t, s, "/bin/ls", kc); err == nil {
			t.Fatal("ls ~/Library/Keychains accepté sous le profil")
		}
		return
	}
	t.Cleanup(func() {
		_ = exec.Command("/usr/bin/security", "delete-generic-password", "-s", service, "-a", "probe").Run()
	})
	find := []string{"find-generic-password", "-s", service, "-a", "probe", "-w"}
	if out, err := exec.Command("/usr/bin/security", find...).Output(); err != nil || strings.TrimSpace(string(out)) != "secret-test" {
		t.Skipf("élément de test illisible hors bac à sable (%v) : trousseau verrouillé ?", err)
	}
	if err := runWrapped(t, s, "/usr/bin/security", find...); err == nil {
		t.Fatal("clé lue dans le trousseau sous le profil")
	}
	if err := runWrapped(t, s, "/bin/ls", kc); err == nil {
		t.Fatal("ls ~/Library/Keychains accepté sous le profil")
	}
}

// R19 : sous le profil, avec les chemins de GitDenyWrite, .git/config
// reste lisible mais pas inscriptible ; .git/hooks (même absent) et
// .git/commondir ne peuvent pas être créés ; le reste de .git (objets) est
// inscriptible ; ni .git ni le projet ne peuvent être renommés ; touch et
// tar sur la racine du projet fonctionnent.
func TestDarwinDenyWriteGit(t *testing.T) {
	needSandbox(t)
	base, home := realTemp(t), realTemp(t)
	proj := filepath.Join(base, "proj")
	git := filepath.Join(proj, ".git")
	if err := os.MkdirAll(filepath.Join(git, "objects"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(git, "config")
	if err := os.WriteFile(cfg, []byte("[core]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(base, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	tgz := filepath.Join(base, "x.tgz")
	if err := exec.Command("/usr/bin/tar", "czf", tgz, "-C", src, ".").Run(); err != nil {
		t.Fatal(err)
	}
	s := fakeSpec(t, proj, home)
	s.Write = append(s.Write, base) // le dossier parent est inscriptible : renommer le projet y serait possible
	s.DenyWrite = GitDenyWrite(proj)
	sh := func(c string) error { return runWrapped(t, s, "/bin/sh", "-c", c) }
	for _, c := range []string{
		"echo '[core] fsmonitor = x' >> " + cfg,
		"mkdir " + filepath.Join(git, "hooks") + " && echo x > " + filepath.Join(git, "hooks", "pre-commit"),
		"echo " + base + "/evil > " + filepath.Join(git, "commondir"),
		"echo x > " + filepath.Join(git, "config.worktree"),
		"mv " + git + " " + filepath.Join(proj, ".git-old"),
		"mv " + proj + " " + filepath.Join(base, "proj2"),
	} {
		if err := sh(c); err == nil {
			t.Errorf("accepté sous le profil : %s", c)
		}
	}
	for _, p := range []string{"hooks", "commondir", "config.worktree"} {
		if _, err := os.Lstat(filepath.Join(git, p)); err == nil {
			t.Errorf(".git/%s créé", p)
		}
	}
	for _, c := range []string{
		"cat " + cfg,
		"echo x > " + filepath.Join(git, "objects", "x"),
		"touch " + proj,
		"/usr/bin/tar xzf " + tgz + " -C " + proj,
	} {
		if err := sh(c); err != nil {
			t.Errorf("refusé sous le profil : %s (%v)", c, err)
		}
	}
}
