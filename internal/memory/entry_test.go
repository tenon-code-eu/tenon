package memory

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func sample() Entry {
	return Entry{
		Scope: User, ID: "build-cmd", Title: `Build: "make all" # fast`,
		Body: "Run make all.\n\nSecond paragraph.\n", Author: "model",
		Sensitive: true, Created: time.Date(2026, 10, 26, 9, 8, 7, 0, time.UTC),
	}
}

func TestEntryRoundTrip(t *testing.T) {
	for _, title := range []string{"simple title", `Build: "make all" # fast`, "- dash", "  padded ", "it's 'quoted'", `back\slash`} {
		e := sample()
		e.Title = title
		data, err := encodeEntry(e)
		if err != nil {
			t.Fatalf("%q: %v", title, err)
		}
		if !strings.Contains(string(data), "created: 2026-10-26T09:08:07Z\n") {
			t.Fatalf("created not RFC 3339 UTC:\n%s", data)
		}
		got, err := parseEntry(User, "build-cmd", data)
		if err != nil {
			t.Fatalf("%q: %v", title, err)
		}
		if got.Title != title || got.Body != e.Body || got.Author != "model" || !got.Sensitive ||
			!got.Created.Equal(e.Created) || got.ID != e.ID || got.Scope != User || got.SHA256 == "" {
			t.Fatalf("%q: round trip differs: %+v", title, got)
		}
	}
}

func TestEntryInvalid(t *testing.T) {
	cases := map[string]func(*Entry){
		"bad id":         func(e *Entry) { e.ID = "Bad_ID" },
		"empty id":       func(e *Entry) { e.ID = "" },
		"empty title":    func(e *Entry) { e.Title = "" },
		"long title":     func(e *Entry) { e.Title = strings.Repeat("a", 81) },
		"two-line title": func(e *Entry) { e.Title = "a\nb" },
		"bad author":     func(e *Entry) { e.Author = "robot" },
	}
	for name, mut := range cases {
		e := sample()
		mut(&e)
		if _, err := encodeEntry(e); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", name, err)
		}
	}
	e := sample()
	e.Body = strings.Repeat("x", 4097)
	_, err := encodeEntry(e)
	if !errors.Is(err, ErrTooLarge) || err.Error() != "memory: entry too large (4097 bytes, max 4096)" {
		t.Errorf("body: %v", err)
	}
	e.Body = strings.Repeat("x", 4096)
	if _, err := encodeEntry(e); err != nil {
		t.Errorf("4096 bytes must pass: %v", err)
	}
	e = sample()
	e.Title = strings.Repeat("é", 80)
	if _, err := encodeEntry(e); err != nil {
		t.Errorf("80 chars must pass: %v", err)
	}
	// id différent du nom de fichier
	data, _ := encodeEntry(sample())
	if _, err := parseEntry(User, "other", data); !errors.Is(err, ErrInvalid) {
		t.Errorf("id != file name: %v", err)
	}
}

func TestParseScope(t *testing.T) {
	for _, s := range []string{"project", "user", "global"} {
		if got, err := ParseScope(s); err != nil || string(got) != s {
			t.Errorf("%s: %v %v", s, got, err)
		}
	}
	if _, err := ParseScope("team"); !errors.Is(err, ErrInvalid) {
		t.Errorf("want ErrInvalid, got %v", err)
	}
}
