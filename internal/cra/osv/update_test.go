package osv

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func zipBytes(t *testing.T, names ...string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range names {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(`{"id":"X"}`))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

var fixedNow = func() time.Time { return time.Date(2026, 10, 23, 8, 30, 0, 0, time.UTC) }

func readIndex(t *testing.T, dir string) map[string]indexEntry {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	var idx map[string]indexEntry
	if err := json.Unmarshal(b, &idx); err != nil {
		t.Fatal(err)
	}
	return idx
}

func TestUpdateOK(t *testing.T) {
	pyi := zipBytes(t, "A.json", "B.json", "README.txt")
	goz := zipBytes(t, "G.json")
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/PyPI/all.zip":
			w.Write(pyi)
		case "/Go/all.zip":
			w.Write(goz)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	dir := filepath.Join(t.TempDir(), "osv")
	if err := Update(context.Background(), srv.Client(), srv.URL, []string{"PyPI", "Go"}, dir, fixedNow); err != nil {
		t.Fatal(err)
	}
	idx := readIndex(t, dir)
	sum := sha256.Sum256(pyi)
	want := indexEntry{Date: "2026-10-23T08:30:00Z", SHA256: hex.EncodeToString(sum[:]), Entries: 2}
	if idx["PyPI"] != want {
		t.Fatalf("PyPI : %+v, attendu %+v", idx["PyPI"], want)
	}
	if idx["Go"].Entries != 1 {
		t.Fatalf("Go : %+v", idx["Go"])
	}
	got, err := os.ReadFile(filepath.Join(dir, "PyPI", "all.zip"))
	if err != nil || !bytes.Equal(got, pyi) {
		t.Fatalf("archive : %v", err)
	}
	// La base produite doit être relisible (sha256 vérifié par Open).
	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := db.Date("Go"); !ok {
		t.Fatal("date absente")
	}
	st, err := os.Stat(filepath.Join(dir, "PyPI", "all.zip"))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v %v", st, err)
	}
	// Aucun fichier temporaire résiduel.
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if e.Name() != "index.json" && e.Name() != "PyPI" && e.Name() != "Go" {
			t.Fatalf("résidu %s", e.Name())
		}
	}
}

// previous prépare une base existante avec une archive valide et renvoie son
// contenu brut, pour vérifier qu'un échec n'y touche pas.
func previous(t *testing.T) (dir string, archive, index []byte) {
	t.Helper()
	dir = fixture(t, map[string][]string{"PyPI": {`{"id":"OLD"}`}})
	archive, _ = os.ReadFile(filepath.Join(dir, "PyPI", "all.zip"))
	index, _ = os.ReadFile(filepath.Join(dir, "index.json"))
	return
}

func assertUntouched(t *testing.T, dir string, archive, index []byte) {
	t.Helper()
	a, _ := os.ReadFile(filepath.Join(dir, "PyPI", "all.zip"))
	i, _ := os.ReadFile(filepath.Join(dir, "index.json"))
	if !bytes.Equal(a, archive) || !bytes.Equal(i, index) {
		t.Fatal("archive ou index précédent modifié")
	}
	for _, p := range []string{dir, filepath.Join(dir, "PyPI")} {
		ents, _ := os.ReadDir(p)
		for _, e := range ents {
			if strings.Contains(e.Name(), "tmp") || strings.Contains(e.Name(), "part") {
				t.Fatalf("fichier temporaire résiduel %s", e.Name())
			}
		}
	}
}

func TestUpdateTruncated(t *testing.T) {
	good := zipBytes(t, "A.json")
	cases := map[string]http.HandlerFunc{
		"coupé en cours de corps": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", "100000")
			w.Write(good[:len(good)/2])
			panic(http.ErrAbortHandler)
		},
		"zip tronqué sans coupure": func(w http.ResponseWriter, r *http.Request) {
			w.Write(good[:len(good)/2])
		},
		"pas un zip": func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("<html>oops</html>"))
		},
		"zip sans entrée json": func(w http.ResponseWriter, r *http.Request) {
			w.Write(zipBytes(t, "README.txt"))
		},
		"statut 500": func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "boom", 500)
		},
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewTLSServer(h)
			defer srv.Close()
			dir, archive, index := previous(t)
			err := Update(context.Background(), srv.Client(), srv.URL, []string{"PyPI"}, dir, fixedNow)
			if err == nil {
				t.Fatal("erreur attendue")
			}
			if !strings.Contains(err.Error(), "PyPI") {
				t.Fatalf("l'erreur doit nommer l'écosystème : %v", err)
			}
			assertUntouched(t, dir, archive, index)
		})
	}
}

func TestUpdatePartialKeepsEarlier(t *testing.T) {
	ok := zipBytes(t, "A.json")
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/PyPI/all.zip" {
			w.Write(ok)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	dir := t.TempDir()
	err := Update(context.Background(), srv.Client(), srv.URL, []string{"PyPI", "npm"}, dir, fixedNow)
	if err == nil || !strings.Contains(err.Error(), "npm") {
		t.Fatalf("erreur attendue nommant npm : %v", err)
	}
	if _, ok := readIndex(t, dir)["PyPI"]; !ok {
		t.Fatal("PyPI réussi doit rester dans l'index")
	}
}

func TestUpdateTooLarge(t *testing.T) {
	old := maxArchive
	maxArchive = 64
	defer func() { maxArchive = old }()
	big := zipBytes(t, "A.json", "B.json", "C.json")
	for _, chunked := range []bool{false, true} {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if chunked {
				w.(http.Flusher).Flush()
			}
			w.Write(big)
		}))
		dir, archive, index := previous(t)
		err := Update(context.Background(), srv.Client(), srv.URL, []string{"PyPI"}, dir, fixedNow)
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), "too large") {
			t.Fatalf("chunked=%v : %v", chunked, err)
		}
		assertUntouched(t, dir, archive, index)
	}
}

func TestUpdateRedirectHTTP(t *testing.T) {
	c := Client(true)
	req := func(raw string) *http.Request {
		u, _ := url.Parse(raw)
		return &http.Request{URL: u}
	}
	cases := []struct {
		name string
		via  string
		to   string
		ok   bool
	}{
		{"https vers https", "https://a.example/x", "https://b.example/y", true},
		{"https vers http", "https://a.example/x", "http://b.example/y", false},
		{"https vers http local", "https://a.example/x", "http://127.0.0.1/y", false},
		{"http local vers http local", "http://127.0.0.1:1/x", "http://localhost:2/y", true},
		{"http local vers https distant", "http://127.0.0.1:1/x", "https://evil.example/y", false},
		{"http local vers http distant", "http://127.0.0.1:1/x", "http://evil.example/y", false},
	}
	for _, tc := range cases {
		err := c.CheckRedirect(req(tc.to), []*http.Request{req(tc.via)})
		if (err == nil) != tc.ok {
			t.Errorf("%s : err=%v, ok attendu=%v", tc.name, err, tc.ok)
		}
	}
	if err := c.CheckRedirect(req("https://a/"), make([]*http.Request, 10)); err == nil {
		t.Error("trop de redirections")
	}
}

func TestUpdateRedirectRefusedEndToEnd(t *testing.T) {
	// Un serveur http local qui redirige vers un hôte distant : refusé.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://evil.example/PyPI/all.zip", http.StatusFound)
	}))
	defer srv.Close()
	dir, archive, index := previous(t)
	err := Update(context.Background(), Client(true), srv.URL, []string{"PyPI"}, dir, fixedNow)
	if err == nil {
		t.Fatal("redirection refusée attendue")
	}
	assertUntouched(t, dir, archive, index)
}

func TestCheckBase(t *testing.T) {
	const msg = "cra: osv mirror must be https (http only for local hosts)"
	cases := []struct {
		base string
		ok   bool
	}{
		{DefaultBase, true},
		{"https://mirror.example.org/osv", true},
		{"http://localhost:8080/osv", true},
		{"http://127.0.0.1:9000", true},
		{"http://192.168.1.5/osv", true},
		{"http://mirror.example.org/osv", false},
		{"ftp://mirror.example.org", false},
		{"mirror.example.org/osv", false},
		{"", false},
		{"https://", false},
	}
	for _, tc := range cases {
		err := CheckBase(tc.base)
		if (err == nil) != tc.ok {
			t.Errorf("%q : %v", tc.base, err)
		}
		if err != nil && err.Error() != msg {
			t.Errorf("%q : message %q", tc.base, err)
		}
	}
}
