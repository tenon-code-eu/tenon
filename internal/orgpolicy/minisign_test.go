package orgpolicy

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tenon-code-eu/tenon/internal/orgpolicy/orgpolicytest"
)

func readTD(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestVerifyMinisignVectors(t *testing.T) {
	pubFile := readTD(t, "test.pub")
	pk, err := ParsePublicKey(pubFile)
	if err != nil {
		t.Fatal(err)
	}
	// L'ID doit correspondre à celui imprimé par minisign.
	first := strings.SplitN(string(pubFile), "\n", 2)[0]
	if want := strings.TrimPrefix(first, "untrusted comment: minisign public key "); pk.ID() != want {
		t.Fatalf("ID() = %q, want %q", pk.ID(), want)
	}
	msg := readTD(t, "policy.toml")
	for file, want := range map[string]string{
		"policy.toml.prehash.minisig": "vector ED",
		"policy.toml.legacy.minisig":  "vector Ed",
	} {
		got, err := Verify(pk, msg, readTD(t, file))
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		if got != want {
			t.Fatalf("%s: comment %q, want %q", file, got, want)
		}
	}
}

func TestVerifyRejects(t *testing.T) {
	pk, err := ParsePublicKey(readTD(t, "test.pub"))
	if err != nil {
		t.Fatal(err)
	}
	other, err := ParsePublicKey(readTD(t, "other.pub"))
	if err != nil {
		t.Fatal(err)
	}
	msg := readTD(t, "policy.toml")
	sig := string(readTD(t, "policy.toml.prehash.minisig"))
	lines := strings.Split(strings.TrimRight(sig, "\n"), "\n")
	join := func(l ...string) []byte { return []byte(strings.Join(l, "\n") + "\n") }

	badMsg := append([]byte{}, msg...)
	badMsg[0] ^= 1
	xx := []byte("Xx" + strings.Repeat("\x00", 72))

	cases := []struct {
		name string
		pk   PublicKey
		msg  []byte
		sig  []byte
	}{
		{"message altéré", pk, badMsg, []byte(sig)},
		{"commentaire de confiance modifié", pk, msg, join(lines[0], lines[1], lines[2]+"!", lines[3])},
		{"mauvaise clé", other, msg, []byte(sig)},
		{"signature tronquée", pk, msg, join(lines[0], lines[1][:40], lines[2], lines[3])},
		{"base64 invalide", pk, msg, join(lines[0], "!!!not base64!!!", lines[2], lines[3])},
		{"algorithme inconnu", pk, msg, join(lines[0], b64(xx), lines[2], lines[3])},
		{"ligne 3 absente", pk, msg, join(lines[0], lines[1])},
		{"ligne 4 absente", pk, msg, join(lines[0], lines[1], lines[2])},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Verify(c.pk, c.msg, c.sig)
			if err == nil || !errors.Is(err, ErrPolicy) {
				t.Fatalf("err = %v, want ErrPolicy", err)
			}
		})
	}
}

func TestParsePublicKeyRejects(t *testing.T) {
	good := strings.Split(string(readTD(t, "test.pub")), "\n")[1]
	for name, in := range map[string][]byte{
		"vide":             nil,
		"longueur":         []byte("RWThUoDFw3Zy"),
		"algorithme":       []byte("RUThUoDFw3ZyyTTJgd0iU6yqhOi/++SO3hC494Lx9+EwOD2te56R5ono"),
		"base64":           []byte("%%%"),
		"commentaire seul": []byte("untrusted comment: x\n"),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParsePublicKey(in); !errors.Is(err, ErrPolicy) {
				t.Fatalf("err = %v, want ErrPolicy", err)
			}
		})
	}
	if _, err := ParsePublicKey([]byte(good + "\n")); err != nil {
		t.Fatalf("ligne base64 seule: %v", err)
	}
}

func TestOrgpolicytestSignRoundTrip(t *testing.T) {
	dir := t.TempDir()
	id := orgpolicytest.Sign(t, dir, "id = \"x\"\nversion = 1\n")
	pubB, _ := os.ReadFile(filepath.Join(dir, "policy.pub"))
	pk, err := ParsePublicKey(pubB)
	if err != nil {
		t.Fatal(err)
	}
	if pk.ID() != id {
		t.Fatalf("ID %q, want %q", pk.ID(), id)
	}
	msg, _ := os.ReadFile(filepath.Join(dir, "policy.toml"))
	sig, _ := os.ReadFile(filepath.Join(dir, "policy.toml.minisig"))
	c, err := Verify(pk, msg, sig)
	if err != nil {
		t.Fatal(err)
	}
	if c != "timestamp:0\tfile:policy.toml" {
		t.Fatalf("comment %q", c)
	}
}

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
