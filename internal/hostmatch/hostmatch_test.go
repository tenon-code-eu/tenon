package hostmatch

import "testing"

func TestMatch(t *testing.T) {
	entries := []string{"api.mistral.ai", "*.scaleway.ai"}
	for h, want := range map[string]bool{
		"api.mistral.ai": true, "API.Mistral.AI": true, HostOf("https://API.Mistral.AI:443/v1"): true,
		"api.scaleway.ai": true, "a.b.scaleway.ai": true, "scaleway.ai": false,
		"api.scaleway.ai.evil.eu": false, "mistral.ai": false, "localhost": false, "": false,
	} {
		if got := Match(entries, h); got != want {
			t.Errorf("%q: got %v", h, got)
		}
	}
	if Match(nil, "x.eu") {
		t.Error("liste vide : refus attendu")
	}
}

func TestListAllows(t *testing.T) {
	l := List{Name: "global", Hosts: []string{"api.mistral.ai"}, Local: false}
	for h, want := range map[string]bool{"api.mistral.ai": true, "localhost": false, "127.0.0.1": false, "gpu.lan": false, "": false} {
		if got := l.Allows(h); got != want {
			t.Errorf("sans local %q: got %v", h, got)
		}
	}
	l.Local = true
	for h, want := range map[string]bool{"api.mistral.ai": true, "localhost": true, "127.0.0.1": true, "gpu.lan": false, "": false} {
		if got := l.Allows(h); got != want {
			t.Errorf("local %q: got %v", h, got)
		}
	}
	if (List{Local: true}).Allows("") {
		t.Error("hôte vide : refus attendu")
	}
}

func TestCheckEntry(t *testing.T) {
	for _, e := range []string{"api.mistral.ai", "*.scaleway.ai"} {
		if err := CheckEntry(e); err != nil {
			t.Errorf("%q: %v", e, err)
		}
	}
	for _, e := range []string{"", "https://x.eu", "x.eu/v1", "x.eu:443", "*.eu", "a.*.eu", "*", "x..eu"} {
		if err := CheckEntry(e); err == nil {
			t.Errorf("%q: erreur attendue", e)
		}
	}
}

func TestHostOf(t *testing.T) {
	for in, want := range map[string]string{
		"https://API.Mistral.AI:443/v1": "api.mistral.ai",
		"http://[::1]:8000":             "::1",
		"::bad":                         "",
	} {
		if got := HostOf(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}
