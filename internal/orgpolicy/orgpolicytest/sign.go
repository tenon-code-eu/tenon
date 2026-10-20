// Package orgpolicytest fournit des aides de test : génération d'une clé
// Ed25519 éphémère et écriture d'une politique signée au format minisign.
// Il n'importe pas internal/orgpolicy (ses tests internes l'importent).
package orgpolicytest

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/blake2b"
)

// Sign génère une clé neuve et écrit policy.toml, policy.pub et
// policy.toml.minisig (format "ED") dans dir. Renvoie l'identifiant de clé.
func Sign(t testing.TB, dir string, policyTOML string) (keyID string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("orgpolicytest: génération de clé: %v", err)
	}
	var id [8]byte
	if _, err := rand.Read(id[:]); err != nil {
		t.Fatalf("orgpolicytest: key id: %v", err)
	}
	keyID = fmt.Sprintf("%016X", binary.LittleEndian.Uint64(id[:]))

	pubRaw := append(append([]byte("Ed"), id[:]...), pub...)
	pubFile := fmt.Sprintf("untrusted comment: minisign public key %s\n%s\n",
		keyID, base64.StdEncoding.EncodeToString(pubRaw))

	msg := []byte(policyTOML)
	h := blake2b.Sum512(msg)
	sig := ed25519.Sign(priv, h[:])
	sigRaw := append(append([]byte("ED"), id[:]...), sig...)
	comment := "timestamp:0\tfile:policy.toml"
	global := ed25519.Sign(priv, append(append([]byte{}, sig...), comment...))
	sigFile := fmt.Sprintf("untrusted comment: signature from minisign secret key\n%s\ntrusted comment: %s\n%s\n",
		base64.StdEncoding.EncodeToString(sigRaw), comment,
		base64.StdEncoding.EncodeToString(global))

	for name, data := range map[string][]byte{
		"policy.toml":         msg,
		"policy.pub":          []byte(pubFile),
		"policy.toml.minisig": []byte(sigFile),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatalf("orgpolicytest: %v", err)
		}
	}
	return keyID
}
