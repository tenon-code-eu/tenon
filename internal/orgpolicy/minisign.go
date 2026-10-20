// Package orgpolicy charge, vérifie et applique la politique d'organisation
// signée (policy.toml + signature minisign). Ce fichier contient la
// vérification des signatures minisign (Ed25519, formats "Ed" et "ED").
package orgpolicy

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/blake2b"
)

// ErrPolicy est l'erreur racine : toute erreur de politique l'enveloppe.
var ErrPolicy = errors.New("policy violation")

const (
	algLegacy  = "Ed" // signe directement le message
	algPrehash = "ED" // signe BLAKE2b-512 du message

	trustedPrefix = "trusted comment: "
)

// PublicKey est une clé publique minisign.
type PublicKey struct {
	KeyID [8]byte
	Key   ed25519.PublicKey
}

// ID renvoie l'identifiant de clé tel qu'affiché par minisign.
func (k PublicKey) ID() string {
	return fmt.Sprintf("%016X", binary.LittleEndian.Uint64(k.KeyID[:]))
}

func policyErr(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrPolicy, fmt.Sprintf(format, a...))
}

// ParsePublicKey accepte le fichier .pub (commentaire + base64) ou la ligne
// base64 seule.
func ParsePublicKey(b []byte) (PublicKey, error) {
	var pk PublicKey
	lines := splitLines(b)
	var enc string
	switch {
	case len(lines) == 0:
		return pk, policyErr("public key: empty")
	case strings.HasPrefix(lines[0], "untrusted comment:"):
		if len(lines) < 2 {
			return pk, policyErr("public key: missing key line")
		}
		enc = lines[1]
	default:
		enc = lines[0]
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(enc))
	if err != nil {
		return pk, policyErr("public key: invalid base64")
	}
	if len(raw) != 2+8+ed25519.PublicKeySize {
		return pk, policyErr("public key: invalid length")
	}
	if string(raw[:2]) != algLegacy {
		return pk, policyErr("public key: unsupported algorithm")
	}
	copy(pk.KeyID[:], raw[2:10])
	pk.Key = ed25519.PublicKey(bytes.Clone(raw[10:]))
	return pk, nil
}

// Verify vérifie la signature minisign sig de msg avec pk et renvoie le
// commentaire de confiance (authentifié par la signature globale).
func Verify(pk PublicKey, msg, sig []byte) (trustedComment string, err error) {
	if len(pk.Key) != ed25519.PublicKeySize {
		return "", policyErr("signature: invalid public key")
	}
	lines := splitLines(sig)
	if len(lines) < 4 {
		return "", policyErr("signature: malformed file")
	}
	raw, err := base64.StdEncoding.DecodeString(lines[1])
	if err != nil {
		return "", policyErr("signature: invalid base64")
	}
	if len(raw) != 2+8+ed25519.SignatureSize {
		return "", policyErr("signature: invalid length")
	}
	alg := string(raw[:2])
	if alg != algLegacy && alg != algPrehash {
		return "", policyErr("signature: unsupported algorithm")
	}
	if !bytes.Equal(raw[2:10], pk.KeyID[:]) {
		return "", policyErr("signature: key ID mismatch")
	}
	sigBytes := raw[10:]

	signed := msg
	if alg == algPrehash {
		h := blake2b.Sum512(msg)
		signed = h[:]
	}
	if !ed25519.Verify(pk.Key, signed, sigBytes) {
		return "", policyErr("signature: invalid")
	}

	if !strings.HasPrefix(lines[2], trustedPrefix) {
		return "", policyErr("signature: missing trusted comment")
	}
	comment := strings.TrimPrefix(lines[2], trustedPrefix)
	global, err := base64.StdEncoding.DecodeString(lines[3])
	if err != nil || len(global) != ed25519.SignatureSize {
		return "", policyErr("signature: invalid global signature")
	}
	gm := append(bytes.Clone(sigBytes), comment...)
	if !ed25519.Verify(pk.Key, gm, global) {
		return "", policyErr("signature: trusted comment not authentic")
	}
	return comment, nil
}

// splitLines découpe sur \n en retirant un \r final ; les lignes vides de
// fin sont ignorées.
func splitLines(b []byte) []string {
	parts := strings.Split(string(b), "\n")
	for len(parts) > 0 && strings.TrimRight(parts[len(parts)-1], "\r") == "" {
		parts = parts[:len(parts)-1]
	}
	for i, p := range parts {
		parts[i] = strings.TrimSuffix(p, "\r")
	}
	return parts
}
