package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)

func newStore(t *testing.T, project bool) (*Store, string, string) {
	t.Helper()
	root, data := t.TempDir(), t.TempDir()
	return Open(Options{Root: root, DataDir: data, Project: project, Now: func() time.Time { return t0 }}), root, data
}

func put(t *testing.T, s *Store, sc Scope, id string) Entry {
	t.Helper()
	e, err := s.Put(Entry{Scope: sc, ID: id, Title: "t " + id, Body: "b", Author: "user"})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestStorePaths(t *testing.T) {
	s, root, data := newStore(t, true)
	for _, sc := range []Scope{Project, User, Global} {
		put(t, s, sc, "x")
	}
	sum := sha256.Sum256([]byte(root))
	key := hex.EncodeToString(sum[:])[:16]
	if ProjectKey(root) != key {
		t.Fatalf("key %s != %s", ProjectKey(root), key)
	}
	want := map[string][2]os.FileMode{
		filepath.Join(root, ".tenon", "memory", "x.md"): {0o644, 0o700},
		filepath.Join(data, "memory", key, "x.md"):      {0o600, 0o700},
		filepath.Join(data, "memory", "global", "x.md"): {0o600, 0o700},
	}
	for p, m := range want {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != m[0] {
			t.Errorf("%s file mode %o", p, fi.Mode().Perm())
		}
		di, _ := os.Stat(filepath.Dir(p))
		if di.Mode().Perm() != m[1] {
			t.Errorf("%s dir mode %o", p, di.Mode().Perm())
		}
	}
}

func TestStoreGetOrder(t *testing.T) {
	s, _, _ := newStore(t, true)
	if _, err := s.Put(Entry{Scope: User, ID: "a", Title: "from user", Author: "user"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(Entry{Scope: Project, ID: "a", Title: "from project", Author: "user"}); err != nil {
		t.Fatal(err)
	}
	e, err := s.Get("", "a")
	if err != nil || e.Scope != Project || e.Title != "from project" {
		t.Fatalf("%+v %v", e, err)
	}
	e, err = s.Get(User, "a")
	if err != nil || e.Title != "from user" {
		t.Fatalf("%+v %v", e, err)
	}
	if _, err := s.Get("", "zzz"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := s.Get(User, "../x"); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

func TestStoreListOrder(t *testing.T) {
	s, _, _ := newStore(t, true)
	now := t0
	s.now = func() time.Time { now = now.Add(time.Minute); return now }
	put(t, s, Global, "g")
	put(t, s, User, "u1")
	put(t, s, User, "u2")
	put(t, s, Project, "p")
	got, notices := s.List()
	var ids []string
	for _, e := range got {
		ids = append(ids, string(e.Scope)+"/"+e.ID)
	}
	if len(notices) != 0 || strings.Join(ids, " ") != "project/p user/u2 user/u1 global/g" {
		t.Fatalf("%v %v", ids, notices)
	}
}

func TestStoreFull(t *testing.T) {
	s, _, _ := newStore(t, true)
	for i := 0; i < 200; i++ {
		put(t, s, User, fmt.Sprintf("e%d", i))
	}
	_, err := s.Put(Entry{Scope: User, ID: "one-more", Title: "t", Author: "user"})
	if !errors.Is(err, ErrFull) || err.Error() != "memory: user holds 200 entries; forget some first" {
		t.Fatalf("%v", err)
	}
	// remplacer une entrée existante reste permis
	put(t, s, User, "e0")
}

func TestStoreBrokenFile(t *testing.T) {
	s, root, _ := newStore(t, true)
	put(t, s, Project, "good")
	dir := filepath.Join(root, ".tenon", "memory")
	os.WriteFile(filepath.Join(dir, "x.md"), []byte("no frontmatter"), 0o644)
	got, notices := s.List()
	if len(got) != 1 || got[0].ID != "good" {
		t.Fatalf("%+v", got)
	}
	if len(notices) != 1 || !strings.HasPrefix(notices[0], "memory: project/x.md ignored: ") {
		t.Fatalf("%v", notices)
	}
}

func TestStoreSymlinks(t *testing.T) {
	// .tenon/memory est un lien symbolique : aucune entrée projet.
	s, root, _ := newStore(t, true)
	outside := t.TempDir()
	e := Entry{Scope: Project, ID: "evil", Title: "t", Author: "user"}
	data, _ := encodeEntry(e)
	os.WriteFile(filepath.Join(outside, "evil.md"), data, 0o644)
	os.MkdirAll(filepath.Join(root, ".tenon"), 0o755)
	if err := os.Symlink(outside, filepath.Join(root, ".tenon", "memory")); err != nil {
		t.Fatal(err)
	}
	got, notices := s.List()
	if len(got) != 0 || len(notices) != 1 {
		t.Fatalf("%+v %v", got, notices)
	}
	if _, err := s.Get(Project, "evil"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("%v", err)
	}
	if _, err := s.Put(Entry{Scope: Project, ID: "w", Title: "t", Author: "user"}); err == nil {
		t.Fatal("write through symlink must fail")
	}
	if _, err := os.Stat(filepath.Join(outside, "w.md")); err == nil {
		t.Fatal("file written outside the root")
	}

	// .tenon lui-même est un lien symbolique.
	s2, root2, _ := newStore(t, true)
	os.Symlink(t.TempDir(), filepath.Join(root2, ".tenon"))
	if got, n := s2.List(); len(got) != 0 || len(n) != 1 {
		t.Fatalf("%+v %v", got, n)
	}

	// un fichier d'entrée est un lien vers un secret.
	s3, root3, _ := newStore(t, true)
	put(t, s3, Project, "ok")
	secret := filepath.Join(t.TempDir(), "id_rsa")
	os.WriteFile(secret, []byte("---\nid: link\ntitle: t\nauthor: user\ncreated: 2026-01-01T00:00:00Z\n---\nSECRET"), 0o600)
	os.Symlink(secret, filepath.Join(root3, ".tenon", "memory", "link.md"))
	got, notices = s3.List()
	if len(got) != 1 || got[0].ID != "ok" || len(notices) != 1 || !strings.HasPrefix(notices[0], "memory: project/link.md ignored: ") {
		t.Fatalf("%+v %v", got, notices)
	}
	if _, err := s3.Get(Project, "link"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("%v", err)
	}
}

func TestStoreProjectOff(t *testing.T) {
	s, root, _ := newStore(t, false)
	dir := filepath.Join(root, ".tenon", "memory")
	os.MkdirAll(dir, 0o755)
	data, _ := encodeEntry(Entry{Scope: Project, ID: "p", Title: "t", Author: "user", Created: t0})
	os.WriteFile(filepath.Join(dir, "p.md"), data, 0o644)
	if got, _ := s.List(); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
	if _, err := s.Get("", "p"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("%v", err)
	}
	if _, err := s.Put(Entry{Scope: Project, ID: "q", Title: "t", Author: "user"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "q.md")); err == nil {
		t.Fatal("written")
	}
}

func TestStoreForget(t *testing.T) {
	s, _, _ := newStore(t, true)
	put(t, s, User, "a")
	if err := s.Forget(User, "a"); err != nil {
		t.Fatal(err)
	}
	if err := s.Forget(User, "a"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestStoreAtomic(t *testing.T) {
	s, _, data := newStore(t, true)
	put(t, s, User, "a")
	e2, err := s.Put(Entry{Scope: User, ID: "a", Title: "new", Body: "other", Author: "user"})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(data, "memory", ProjectKey(s.root))
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 || ents[0].Name() != "a.md" {
		t.Fatalf("%v", ents)
	}
	got, _ := s.Get(User, "a")
	if got.Title != "new" || got.SHA256 != e2.SHA256 || len(got.SHA256) != 64 {
		t.Fatalf("%+v", got)
	}
	if !got.Created.Equal(t0) {
		t.Fatalf("created %v", got.Created)
	}
}

func TestGetSkipsBrokenEarlierScope(t *testing.T) {
	s, root, _ := newStore(t, true)
	put(t, s, Global, "x")
	os.MkdirAll(filepath.Join(root, ".tenon", "memory"), 0o755)
	os.WriteFile(filepath.Join(root, ".tenon", "memory", "x.md"), []byte("junk"), 0o644)
	if e, err := s.Get("", "x"); err != nil || e.Scope != Global {
		t.Fatalf("%v %v", e, err)
	}
	if _, err := s.Get(Project, "x"); err == nil {
		t.Fatal("portée explicite doit échouer")
	}
	os.Remove(filepath.Join(s.dir(Global), "x.md"))
	if _, err := s.Get("", "x"); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("erreur du fichier cassé attendue: %v", err)
	}
}

func TestListNoticeNoDoubledPrefix(t *testing.T) {
	s, _, _ := newStore(t, true)
	put(t, s, User, "ok")
	os.WriteFile(filepath.Join(s.dir(User), "big.md"), make([]byte, maxFileBytes+10), 0o600)
	_, notes := s.List()
	for _, n := range notes {
		if strings.Contains(n, "memory: memory:") || strings.Contains(n, "ignored: memory:") {
			t.Fatal(n)
		}
	}
	if len(notes) == 0 {
		t.Fatal("pas d'avis")
	}
}

func TestLimitCountsOnlyValid(t *testing.T) {
	s, _, _ := newStore(t, true)
	put(t, s, User, "first")
	for i := 0; i < 250; i++ {
		os.WriteFile(filepath.Join(s.dir(User), fmt.Sprintf("junk%d.md", i)), []byte("x"), 0o600)
	}
	if _, err := s.Put(Entry{Scope: User, ID: "second", Title: "t", Author: "user"}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateTitle(t *testing.T) {
	for _, title := range []string{"   ", "a\tb", "a\x00b", "bad\xffutf", "a\u0085b"} {
		if err := (Entry{ID: "x", Title: title, Author: "user"}).validate(); err == nil {
			t.Errorf("%q accepté", title)
		}
	}
}

func TestListNoticeTooLargeExact(t *testing.T) {
	s, _, _ := newStore(t, true)
	put(t, s, User, "base")
	e := Entry{Scope: User, ID: "big", Title: "t", Author: "user", Created: t0}
	e.Body = "x"
	data, _ := encodeEntry(e)
	data = append(data, strings.Repeat("x", 4999)...)
	os.WriteFile(filepath.Join(s.dir(User), "big.md"), data, 0o600)
	_, notes := s.List()
	want := "memory: user/big.md ignored: entry too large (5000 bytes, max 4096)"
	if len(notes) != 1 || notes[0] != want {
		t.Fatalf("%q", notes)
	}
}
