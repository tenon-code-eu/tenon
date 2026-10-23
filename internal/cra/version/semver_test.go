package version

import "testing"

func TestSemverOrder(t *testing.T) {
	chain := []string{"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta", "1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0"}
	for i := 0; i < len(chain)-1; i++ {
		for j := i + 1; j < len(chain); j++ {
			expect(t, Semver, chain[i], chain[j], -1)
			expect(t, Semver, chain[j], chain[i], 1)
		}
	}
	expect(t, Semver, "v1.2.3", "1.2.3", 0)
	expect(t, Semver, "v1.2.3+incompatible", "v1.2.3", 0)
	expect(t, Semver, "1.2.3-beta.0", "1.2.3", -1)
	expect(t, Semver, "1.0.0+build", "1.0.0", 0)
	expect(t, Semver, "1.0.0+a", "1.0.0+b", 0)
	expect(t, Semver, "v0.0.0-20210101000000-abcdefabcdef", "v0.0.1", -1)
	expect(t, Semver, "v0.0.1-0.20210101000000-abcdefabcdef", "v0.0.1", -1)
	expect(t, Semver, "v0.0.1-0.20210101000000-abcdefabcdef", "v0.0.0", 1)
	expect(t, Semver, "v1.2.4-0.20210101000000-abcdefabcdef", "v1.2.4-0.20220101000000-abcdefabcdef", -1)
	expect(t, Semver, "1.2", "1.2.0", 0)
	expect(t, Semver, "1", "1.0.1", -1)
	expect(t, Semver, "1.9.0", "1.10.0", -1)
	expect(t, Semver, "99999999999999999999.0.0", "100000000000000000000.0.0", -1)
	expect(t, Semver, "1.0.0-1", "1.0.0-alpha", -1)
	expect(t, Semver, "1.0.0-2", "1.0.0-10", -1)
	expect(t, Semver, "1.0.0-alpha", "1.0.0-Alpha", 1)
}

func TestSemverErrors(t *testing.T) {
	for _, bad := range []string{"abc", "", "v", "1.2.3.4", "1..3", "1.0.0-", "1.0.0-a..b", "1.0.0+", "1.x.0", "-1.0.0"} {
		if _, err := Semver.Compare(bad, "1.0.0"); err == nil {
			t.Errorf("%q : erreur attendue", bad)
		}
		if _, err := Semver.Compare("1.0.0", bad); err == nil {
			t.Errorf("%q (b) : erreur attendue", bad)
		}
	}
}

func TestFor(t *testing.T) {
	for _, e := range []string{"Go", "npm", "crates.io"} {
		c, ok := For(e)
		if !ok || c != Semver {
			t.Errorf("%s : semver attendu", e)
		}
	}
	if c, ok := For("PyPI"); !ok || c != PEP440 {
		t.Error("PyPI : PEP440 attendu")
	}
	for _, e := range []string{"Maven", "NuGet", "RubyGems", "Packagist", "", "go"} {
		if _, ok := For(e); ok {
			t.Errorf("%s : pas de comparateur attendu", e)
		}
	}
}

func expect(t *testing.T, c Comparator, a, b string, want int) {
	t.Helper()
	got, err := c.Compare(a, b)
	if err != nil {
		t.Fatalf("Compare(%q,%q) : %v", a, b, err)
	}
	if got != want {
		t.Errorf("Compare(%q,%q) = %d, attendu %d", a, b, got, want)
	}
}
