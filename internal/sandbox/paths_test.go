package sandbox

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestDefaultWrite(t *testing.T) {
	common := []string{"/proj", "/tmp", "/dev/null", "/dev/tty", "/dev/zero", "/dev/random", "/dev/urandom"}

	lin := DefaultWrite("/proj", "/h", envOf(map[string]string{"TMPDIR": "/x/tmp"}), "linux")
	for _, p := range append([]string{"/x/tmp", "/h/.cache"}, common...) {
		if !slices.Contains(lin, p) {
			t.Errorf("linux: %q absent de %v", p, lin)
		}
	}
	for _, p := range []string{"/private/tmp", "/private/var/folders", "/h/Library/Caches"} {
		if slices.Contains(lin, p) {
			t.Errorf("linux: %q inattendu", p)
		}
	}

	dar := DefaultWrite("/proj", "/h", envOf(map[string]string{"TMPDIR": "/x/tmp"}), "darwin")
	for _, p := range append([]string{"/x/tmp", "/private/tmp", "/private/var/folders", "/h/Library/Caches"}, common...) {
		if !slices.Contains(dar, p) {
			t.Errorf("darwin: %q absent de %v", p, dar)
		}
	}
	if slices.Contains(dar, "/h/.cache") {
		t.Error("darwin: /h/.cache inattendu")
	}

	xdg := DefaultWrite("/proj", "/h", envOf(map[string]string{"XDG_CACHE_HOME": "/xdg"}), "linux")
	if !slices.Contains(xdg, "/xdg") || slices.Contains(xdg, "/h/.cache") {
		t.Errorf("XDG_CACHE_HOME ignoré: %v", xdg)
	}

	none := DefaultWrite("/proj", "/h", envOf(nil), "linux")
	if slices.Contains(none, "") {
		t.Errorf("entrée vide dans %v", none)
	}
}

func TestDefaultDenyRead(t *testing.T) {
	got := DefaultDenyRead("/h", "/cfg", "/data", "/etc/pol.toml")
	want := []string{
		"/h/.ssh", "/h/.gnupg", "/h/.aws", "/h/.azure", "/h/.config/gcloud",
		"/h/.kube", "/h/.docker", "/h/.netrc", "/h/.password-store",
		"/h/Library/Keychains",
		"/cfg", "/data", "/etc/pol.toml",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	got = DefaultDenyRead("/h", "", "", "")
	if len(got) != 10 {
		t.Errorf("arguments vides non ignorés: %v", got)
	}
}

func mkTree(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"a", "b/c", "b/secret", "b/d", "e"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestSplitAround(t *testing.T) {
	r := mkTree(t)
	j := func(p ...string) string { return filepath.Join(append([]string{r}, p...)...) }

	got, err := splitAround(r, []string{j("b", "secret")})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{j("a"), j("b", "c"), j("b", "d"), j("e")}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}

	got, _ = splitAround(r, []string{j("missing")})
	if !reflect.DeepEqual(got, []string{r}) {
		t.Errorf("absent: %v", got)
	}
	got, _ = splitAround(r, []string{"/definitely/elsewhere"})
	if !reflect.DeepEqual(got, []string{r}) {
		t.Errorf("hors racine: %v", got)
	}
	got, _ = splitAround(r, []string{r})
	if len(got) != 0 {
		t.Errorf("deny = racine: %v", got)
	}
}

func TestSplitAroundSymlinkDeny(t *testing.T) {
	r := mkTree(t)
	link := filepath.Join(r, "e", "lnk")
	if err := os.Symlink(filepath.Join(r, "b", "secret"), link); err != nil {
		t.Fatal(err)
	}
	got, _ := splitAround(r, []string{link})
	for _, p := range got {
		if p == filepath.Join(r, "b") || p == filepath.Join(r, "b", "secret") {
			t.Errorf("secret ouvert via lien: %v", got)
		}
	}
}

func TestCanonicalSymlink(t *testing.T) {
	r, _ := filepath.EvalSymlinks(t.TempDir())
	q := filepath.Join(r, "q")
	if err := os.Mkdir(q, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(r, "p")
	if err := os.Symlink(q, p); err != nil {
		t.Fatal(err)
	}
	miss := filepath.Join(r, "x", "..", "missing")
	got := Canonical([]string{p, miss, p})
	want := []string{q, filepath.Join(r, "missing")}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSplitAroundSkipsSymlinkSiblings(t *testing.T) {
	r := mkTree(t)
	outside, _ := filepath.EvalSymlinks(t.TempDir())
	toDenied := filepath.Join(r, "b", "lnk1")
	toOutside := filepath.Join(r, "b", "lnk2")
	if err := os.Symlink(filepath.Join(r, "b", "secret"), toDenied); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, toOutside); err != nil {
		t.Fatal(err)
	}
	got, err := splitAround(r, []string{filepath.Join(r, "b", "secret")})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(got, toDenied) || slices.Contains(got, toOutside) {
		t.Errorf("lien voisin accordé: %v", got)
	}
}

func TestSplitAroundRootUnderDeny(t *testing.T) {
	r := mkTree(t)
	got, err := splitAround(filepath.Join(r, "b"), []string{r})
	if err != nil || len(got) != 0 {
		t.Errorf("racine sous un interdit: %v, %v", got, err)
	}
}

func TestDenyAncestors(t *testing.T) {
	root := realDir(t)
	deny := filepath.Join(root, "a", "b", "secret")
	if err := os.MkdirAll(deny, 0o755); err != nil {
		t.Fatal(err)
	}
	// Un lien vers le dossier interdit : canonisé, ses parents à lui ne comptent pas.
	if err := os.Symlink(deny, filepath.Join(root, "lien")); err != nil {
		t.Fatal(err)
	}
	got := denyAncestors([]string{deny, filepath.Join(root, "lien"), filepath.Join(root, "absent", "x")})
	want := []string{filepath.Join(root, "a", "b"), filepath.Join(root, "a"), root}
	for _, w := range want {
		if !slices.Contains(got, w) {
			t.Fatalf("ancêtre %s manquant: %q", w, got)
		}
	}
	if !slices.Contains(got, "/") {
		t.Fatalf("racine manquante: %q", got)
	}
	for _, g := range got {
		if g == deny || g == filepath.Join(root, "absent") || g == filepath.Join(root, "lien") {
			t.Fatalf("entrée inattendue %s: %q", g, got)
		}
	}
	if len(got) != len(slices.Compact(slices.Sorted(slices.Values(got)))) {
		t.Fatalf("doublons: %q", got)
	}
}

func realDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}
