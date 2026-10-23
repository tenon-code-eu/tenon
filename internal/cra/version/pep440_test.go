package version

import "testing"

func TestPEP440Order(t *testing.T) {
	chain := []string{"1.0.dev1", "1.0a1", "1.0a2.dev1", "1.0a2", "1.0b1", "1.0rc1", "1.0", "1.0.post1", "1.1"}
	for i := 0; i < len(chain)-1; i++ {
		for j := i + 1; j < len(chain); j++ {
			expect(t, PEP440, chain[i], chain[j], -1)
			expect(t, PEP440, chain[j], chain[i], 1)
		}
	}
	expect(t, PEP440, "1.0", "1.0.0", 0)
	expect(t, PEP440, "1.0", "1.0.0.0", 0)
	expect(t, PEP440, "1!0.5", "2.0", 1)
	expect(t, PEP440, "1.0alpha1", "1.0a1", 0)
	expect(t, PEP440, "1.0-beta.2", "1.0b2", 0)
	expect(t, PEP440, "1.0_pre_3", "1.0rc3", 0)
	expect(t, PEP440, "1.0c1", "1.0rc1", 0)
	expect(t, PEP440, "1.0preview1", "1.0rc1", 0)
	expect(t, PEP440, "1.0A1", "1.0a1", 0)
	expect(t, PEP440, "v1.0", "1.0", 0)
	expect(t, PEP440, "1.0a", "1.0a0", 0)
	expect(t, PEP440, "1.0-1", "1.0.post1", 0)
	expect(t, PEP440, "1.0-rev2", "1.0.post2", 0)
	expect(t, PEP440, "1.0.post1.dev1", "1.0.post1", -1)
	expect(t, PEP440, "1.0.post1.dev1", "1.0", 1)
	expect(t, PEP440, "1.0.post", "1.0.post0", 0)
	expect(t, PEP440, "1.0.dev", "1.0.dev0", 0)
	expect(t, PEP440, "1.0.dev1", "1.0a1.dev1", -1)
	expect(t, PEP440, "1.0a1.dev1", "1.0a1", -1)
	expect(t, PEP440, "1.0.post1", "1.0.post2", -1)
	expect(t, PEP440, "1.0rc1.post1", "1.0rc1", 1)
	expect(t, PEP440, "1.0rc1.post1", "1.0", -1)
	expect(t, PEP440, "1.9", "1.10", -1)
	expect(t, PEP440, "0!1.0", "1.0", 0)
	expect(t, PEP440, "1.0", "1.0.1", -1)
	expect(t, PEP440, "2.0.0+cu117", "2.0.0", 0)
	expect(t, PEP440, "1.0+ubuntu1", "1.0", 0)
	expect(t, PEP440, "1.0+local.1", "1.0a1", 1)
	expect(t, PEP440, "99999999999999999999", "100000000000000000000", -1)
}

func TestPEP440Errors(t *testing.T) {
	for _, bad := range []string{"1.0+", "1.0+a..b", "1.0+-x", "", "abc", "1.0.", "1.0.x", "!1.0", "1.0a1b2", "1.0-"} {
		if _, err := PEP440.Compare(bad, "1.0"); err == nil {
			t.Errorf("%q : erreur attendue", bad)
		}
		if _, err := PEP440.Compare("1.0", bad); err == nil {
			t.Errorf("%q (b) : erreur attendue", bad)
		}
	}
}
