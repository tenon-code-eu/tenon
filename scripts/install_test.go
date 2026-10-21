package scripts

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

const testVersion = "v1.2.3"

// Binaires réels nécessaires à install.sh ; minisign en est volontairement absent.
var installBins = []string{"sh", "curl", "tar", "gzip", "uname", "mktemp", "awk", "sed", "grep", "cut", "cp", "mv", "chmod", "mkdir", "rm", "head", "tr", "cat"}

// restrictedPath construit un répertoire de liens vers les binaires réels.
func restrictedPath(t *testing.T) (dir, sh string) {
	t.Helper()
	dir = t.TempDir()
	bins := append([]string{}, installBins...)
	if _, err := exec.LookPath("sha256sum"); err == nil {
		bins = append(bins, "sha256sum")
	} else {
		bins = append(bins, "shasum")
	}
	for _, b := range bins {
		p, err := exec.LookPath(b)
		if err != nil {
			t.Skipf("binaire requis absent : %s", b)
		}
		if b == "sh" {
			sh = p
		}
		if err := os.Symlink(p, filepath.Join(dir, b)); err != nil {
			t.Fatal(err)
		}
	}
	return dir, sh
}

// archive fabrique l'archive tenon_<v>_<os>_<arch>.tar.gz.
func archive(t *testing.T, sh, name string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	script := fmt.Sprintf("#!%s\necho \"tenon %s\"\n", sh, testVersion)
	if err := tw.WriteHeader(&tar.Header{Name: name + "/tenon", Mode: 0o755, Size: int64(len(script))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(script)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type mirror struct {
	srv   *httptest.Server
	mu    sync.Mutex
	paths []string
}

func newMirror(t *testing.T, sh string, badHash bool) *mirror {
	t.Helper()
	name := fmt.Sprintf("tenon_%s_%s_%s", testVersion, runtime.GOOS, runtime.GOARCH)
	arc := archive(t, sh, name)
	sum := fmt.Sprintf("%x", sha256.Sum256(arc))
	if badHash {
		sum = strings.Repeat("0", 64)
	}
	sums := fmt.Sprintf("%s  %s.tar.gz\n", sum, name)
	m := &mirror{}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		m.paths = append(m.paths, r.URL.Path)
		m.mu.Unlock()
		switch r.URL.Path {
		case "/" + testVersion + "/" + name + ".tar.gz":
			_, _ = w.Write(arc)
		case "/" + testVersion + "/SHA256SUMS":
			_, _ = w.Write([]byte(sums))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(m.srv.Close)
	return m
}

type result struct {
	code   int
	stderr string
	dir    string
}

func runInstall(t *testing.T, pathDir, sh string, env ...string) result {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(t.TempDir(), "bin")
	cmd := exec.Command(sh, "../install.sh")
	cmd.Env = append([]string{"HOME=" + home, "TENON_INSTALL_DIR=" + dir, "PATH=" + pathDir}, env...)
	var se bytes.Buffer
	cmd.Stderr = &se
	err := cmd.Run()
	code := 0
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatal(err)
		}
		code = ee.ExitCode()
	}
	return result{code: code, stderr: se.String(), dir: dir}
}

func TestInstallMirrorOK(t *testing.T) {
	p, sh := restrictedPath(t)
	m := newMirror(t, sh, false)
	r := runInstall(t, p, sh, "TENON_DOWNLOAD_BASE="+m.srv.URL, "TENON_VERSION="+testVersion)
	if r.code != 0 {
		t.Fatalf("code %d: %s", r.code, r.stderr)
	}
	out, err := exec.Command(filepath.Join(r.dir, "tenon"), "--version").Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(out)) != "tenon "+testVersion {
		t.Fatalf("version: %q", out)
	}
	if !strings.Contains(r.stderr, "signature not verified") {
		t.Fatalf("avertissement absent: %s", r.stderr)
	}
}

func TestInstallMirrorTrailingSlash(t *testing.T) {
	p, sh := restrictedPath(t)
	m := newMirror(t, sh, false)
	r := runInstall(t, p, sh, "TENON_DOWNLOAD_BASE="+m.srv.URL+"/", "TENON_VERSION="+testVersion)
	if r.code != 0 {
		t.Fatalf("code %d: %s", r.code, r.stderr)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.paths) == 0 {
		t.Fatal("aucune requête")
	}
	for _, q := range m.paths {
		if strings.Contains(q, "//") {
			t.Fatalf("chemin avec //: %s", q)
		}
	}
}

func TestInstallMirrorNoVersion(t *testing.T) {
	p, sh := restrictedPath(t)
	r := runInstall(t, p, sh, "TENON_DOWNLOAD_BASE=https://mirror.invalid")
	if r.code != 1 || !strings.Contains(r.stderr, "TENON_DOWNLOAD_BASE requires TENON_VERSION=vX.Y.Z.") {
		t.Fatalf("code %d: %s", r.code, r.stderr)
	}
}

func TestInstallMirrorHTTPRemote(t *testing.T) {
	p, sh := restrictedPath(t)
	r := runInstall(t, p, sh, "TENON_DOWNLOAD_BASE=http://example.invalid", "TENON_VERSION="+testVersion)
	if r.code != 1 || !strings.Contains(r.stderr, "TENON_DOWNLOAD_BASE must be https.") {
		t.Fatalf("code %d: %s", r.code, r.stderr)
	}
}

func TestInstallMirrorBadHash(t *testing.T) {
	p, sh := restrictedPath(t)
	m := newMirror(t, sh, true)
	r := runInstall(t, p, sh, "TENON_DOWNLOAD_BASE="+m.srv.URL, "TENON_VERSION="+testVersion)
	if r.code != 1 || !strings.Contains(r.stderr, "wrong SHA-256 hash") {
		t.Fatalf("code %d: %s", r.code, r.stderr)
	}
}

func TestInstallMirrorRequireSignature(t *testing.T) {
	p, sh := restrictedPath(t)
	m := newMirror(t, sh, false)
	r := runInstall(t, p, sh, "TENON_DOWNLOAD_BASE="+m.srv.URL, "TENON_VERSION="+testVersion, "TENON_REQUIRE_SIGNATURE=1")
	if r.code != 1 || !strings.Contains(r.stderr, "TENON_REQUIRE_SIGNATURE=1") {
		t.Fatalf("code %d: %s", r.code, r.stderr)
	}
}

func TestInstallMirrorRefusedHosts(t *testing.T) {
	p, sh := restrictedPath(t)
	for _, base := range []string{
		"http://localhost.evil.com",
		"http://127.0.0.1.nip.io",
		"http://localhost@evil",
		"http://evil@localhost",
		"http://LOCALHOST",
		"http://[::1]",
		"http://localhost.:80",
	} {
		r := runInstall(t, p, sh, "TENON_DOWNLOAD_BASE="+base, "TENON_VERSION="+testVersion)
		if r.code != 1 || !strings.Contains(r.stderr, "TENON_DOWNLOAD_BASE must be https.") {
			t.Errorf("%s: code %d: %s", base, r.code, r.stderr)
		}
	}
}

func TestInstallMirrorLocalhostName(t *testing.T) {
	addrs, err := net.LookupHost("localhost")
	if err != nil || !slices.Contains(addrs, "127.0.0.1") {
		t.Skip("localhost ne résout pas vers 127.0.0.1")
	}
	p, sh := restrictedPath(t)
	m := newMirror(t, sh, false)
	base := strings.Replace(m.srv.URL, "127.0.0.1", "localhost", 1)
	r := runInstall(t, p, sh, "TENON_DOWNLOAD_BASE="+base, "TENON_VERSION="+testVersion)
	if r.code != 0 {
		t.Fatalf("code %d: %s", r.code, r.stderr)
	}
}

// Une redirection depuis un miroir local ne doit pas sortir en http.
func TestInstallMirrorHTTPRedirectRefused(t *testing.T) {
	p, sh := restrictedPath(t)
	var hits int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
	}))
	defer other.Close()
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+r.URL.Path, http.StatusFound)
	}))
	defer redir.Close()
	r := runInstall(t, p, sh, "TENON_DOWNLOAD_BASE="+redir.URL, "TENON_VERSION="+testVersion)
	if r.code != 1 {
		t.Fatalf("code %d: %s", r.code, r.stderr)
	}
	if n := atomic.LoadInt32(&hits); n != 0 {
		t.Fatalf("le second serveur a reçu %d requêtes", n)
	}
}
