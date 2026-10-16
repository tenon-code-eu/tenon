package main

import "testing"

func TestResolveVersion(t *testing.T) {
	for _, c := range []struct{ injected, module, want string }{
		{"v0.1.0", "v0.1.0", "v0.1.0"},
		{"v0.1.0", "v9.9.9", "v0.1.0"}, // -ldflags prime
		{"dev", "v0.1.0", "v0.1.0"},    // go install …@v0.1.0
		{"dev", "(devel)", "dev"},
		{"dev", "", "dev"},
	} {
		if got := resolveVersion(c.injected, c.module); got != c.want {
			t.Errorf("resolveVersion(%q, %q) = %q, attendu %q", c.injected, c.module, got, c.want)
		}
	}
}
