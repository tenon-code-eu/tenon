package routing

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"syscall"
	"testing"

	"github.com/tenon-code-eu/tenon/internal/llm"
	"github.com/tenon-code-eu/tenon/internal/provider"
)

type fakeProv struct{ id string }

func (f fakeProv) Info() llm.ProviderInfo {
	if f.id == "local" {
		return llm.ProviderInfo{Jurisdiction: llm.Local}
	}
	return llm.ProviderInfo{Jurisdiction: llm.EU}
}
func (f fakeProv) Stream(context.Context, llm.Request) (<-chan llm.Chunk, error) {
	return nil, nil
}

func cand(id string) Candidate { return Candidate{ID: id, Provider: fakeProv{id}} }

func ids(cs []Candidate) []string {
	out := []string{}
	for _, c := range cs {
		out = append(out, c.ID)
	}
	return out
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestCandidatesNormal(t *testing.T) {
	loc := cand("local")
	r := New(cand("a"), &loc, []Candidate{cand("a"), cand("b"), cand("b"), cand("c")})
	if got := ids(r.Candidates(false)); !eq(got, []string{"a", "b", "c"}) {
		t.Fatalf("got %v", got)
	}
}

func TestCandidatesSensitive(t *testing.T) {
	loc := cand("local")
	r := New(cand("a"), &loc, []Candidate{cand("b")})
	if got := ids(r.Candidates(true)); !eq(got, []string{"local"}) {
		t.Fatalf("got %v", got)
	}
	r = New(cand("a"), nil, []Candidate{cand("b")})
	if got := r.Candidates(true); len(got) != 0 {
		t.Fatalf("got %v", ids(got))
	}
	if _, ok := r.Local(); ok {
		t.Fatal("no local expected")
	}
}

func TestCandidatesCurrentLocal(t *testing.T) {
	loc := cand("local")
	r := New(loc, &loc, []Candidate{cand("b")})
	if got := ids(r.Candidates(false)); !eq(got, []string{"local", "b"}) {
		t.Fatalf("got %v", got)
	}
	if got := ids(r.Candidates(true)); !eq(got, []string{"local"}) {
		t.Fatalf("got %v", got)
	}
	if c, ok := r.Local(); !ok || c.ID != "local" {
		t.Fatal("local missing")
	}
}

func TestRetryable(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"unavailable", fmt.Errorf("%w: x", provider.ErrUnavailable), true},
		{"refused", &url.Error{Op: "Post", URL: "http://x", Err: syscall.ECONNREFUSED}, true},
		{"nofirst", fmt.Errorf("w: %w", ErrNoFirstChunk), true},
		{"auth", provider.ErrAuth, false},
		{"ctxlen", provider.ErrContextLength, false},
		{"incomplete", provider.ErrIncomplete, false},
		{"400", fmt.Errorf("provider: HTTP status 400: x"), false},
		{"canceled", context.Canceled, false},
		{"nil", nil, false},
		{"other", errors.New("boom"), false},
	}
	for _, c := range cases {
		if got := Retryable(c.err); got != c.want {
			t.Errorf("%s: got %v", c.name, got)
		}
	}
}

func TestNewIgnoresNonLocalLocal(t *testing.T) {
	remote := cand("remote")
	r := New(cand("a"), &remote, nil)
	if _, ok := r.Local(); ok {
		t.Fatal("non-local provider accepted as local")
	}
	if got := r.Candidates(true); len(got) != 0 {
		t.Fatalf("got %v", ids(got))
	}
}
