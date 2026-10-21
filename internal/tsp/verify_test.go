package tsp

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/hex"
	"errors"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tenon-code-eu/tenon/internal/tsp/tsptest"
)

func readFile(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func pool(t *testing.T, name string) *x509.CertPool {
	t.Helper()
	p := x509.NewCertPool()
	if !p.AppendCertsFromPEM(readFile(t, name)) {
		t.Fatalf("%s: aucun certificat", name)
	}
	return p
}

func testDigest() []byte {
	h := sha256.Sum256([]byte("tenon"))
	return h[:]
}

func TestVerifyOpenSSLVectors(t *testing.T) {
	want, err := hex.DecodeString(strings.TrimSpace(string(readFile(t, "digest.hex"))))
	if err != nil || len(want) != 32 {
		t.Fatalf("digest.hex: %v (%d octets)", err, len(want))
	}
	for _, c := range []struct{ tsr, ca, other string }{
		{"rsa.tsr", "rsa-ca.pem", "ec-ca.pem"},
		{"ec.tsr", "ec-ca.pem", "rsa-ca.pem"},
	} {
		t.Run(c.tsr, func(t *testing.T) {
			tok := readFile(t, c.tsr)
			info, err := Verify(tok, pool(t, c.ca))
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			if !bytes.Equal(info.Digest, want) {
				t.Errorf("Digest = %x, want %x", info.Digest, want)
			}
			if info.GenTime.IsZero() {
				t.Error("GenTime nul")
			}
			if info.TSA == "" {
				t.Error("TSA vide")
			}
			if info.Serial == nil {
				t.Error("Serial nil")
			}
			if info.Nonce == nil {
				t.Error("Nonce nil (openssl ts -query en met un)")
			}
			if _, err := Verify(tok, pool(t, c.other)); !errors.Is(err, ErrToken) {
				t.Errorf("autre CA: err = %v, want ErrToken", err)
			}
		})
	}
}

func TestVerifyTampered(t *testing.T) {
	ca := tsptest.NewCA(t)
	nonce := big.NewInt(42)
	ok := ca.Token(t, testDigest(), nonce, tsptest.Options{})
	info, err := Verify(ok, ca.Pool)
	if err != nil {
		t.Fatalf("jeton intact: %v", err)
	}
	if !bytes.Equal(info.Digest, testDigest()) || info.Nonce.Cmp(nonce) != 0 || info.TSA == "" {
		t.Fatalf("info = %+v", info)
	}
	okRSA := ca.Token(t, testDigest(), nil, tsptest.Options{RSA: true})
	if info, err := Verify(okRSA, ca.Pool); err != nil || info.Nonce != nil {
		t.Fatalf("jeton RSA intact: %v (nonce %v)", err, info.Nonce)
	}

	cases := []struct {
		name   string
		tamper tsptest.Tamper
		msg    string
	}{
		{"Signature", tsptest.Signature, "bad signature"},
		{"Imprint", tsptest.Imprint, "message imprint of 31 bytes"},
		{"MessageDigest", tsptest.MessageDigest, "messageDigest does not match"},
		{"NoESSCertID", tsptest.NoESSCertID, "missing signingCertificate"},
		{"WrongESSCertID", tsptest.WrongESSCertID, "does not match signer certificate"},
		{"NoEKU", tsptest.NoEKU, "without extended key usage"},
		{"NonCriticalEKU", tsptest.NonCriticalEKU, "not critical"},
		{"EKUServerAuth", tsptest.EKUServerAuth, "timeStamping only"},
		{"WrongContentType", tsptest.WrongContentType, "contentType attribute is not TSTInfo"},
		{"NoSignerCert", tsptest.NoSignerCert, "signer certificate not in token"},
		{"TwoSigners", tsptest.TwoSigners, "2 signer infos"},
		{"SHA1", tsptest.SHA1, "unsupported algorithm 1.3.14.3.2.26"},
		{"PSS", tsptest.PSS, "unsupported algorithm 1.2.840.113549.1.1.10"},
	}
	for _, c := range cases {
		for _, rsaSigner := range []bool{false, true} {
			name := c.name
			if rsaSigner {
				name += "/RSA"
			}
			t.Run(name, func(t *testing.T) {
				tok := ca.Token(t, testDigest(), nonce, tsptest.Options{Tamper: c.tamper, RSA: rsaSigner})
				_, err := Verify(tok, ca.Pool)
				if !errors.Is(err, ErrToken) {
					t.Fatalf("err = %v, want ErrToken", err)
				}
				if !strings.Contains(err.Error(), c.msg) {
					t.Errorf("err = %q, want %q", err, c.msg)
				}
			})
		}
	}
}

func TestVerifyExpiredAfterGenTime(t *testing.T) {
	ca := tsptest.NewCA(t)
	now := time.Now()
	tok := ca.Token(t, testDigest(), nil, tsptest.Options{
		GenTime:         now.AddDate(0, -18, 0),
		SignerNotBefore: now.AddDate(-2, 0, 0),
		SignerNotAfter:  now.AddDate(-1, 0, 0),
	})
	info, err := Verify(tok, ca.Pool)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if d := now.AddDate(0, -18, 0).Sub(info.GenTime); d < -time.Second || d > time.Second {
		t.Errorf("GenTime = %v", info.GenTime)
	}
}

func TestVerifyGenTimeOutsideValidity(t *testing.T) {
	ca := tsptest.NewCA(t)
	now := time.Now()
	tok := ca.Token(t, testDigest(), nil, tsptest.Options{
		GenTime:         now,
		SignerNotBefore: now.AddDate(-2, 0, 0),
		SignerNotAfter:  now.AddDate(-1, 0, 0),
	})
	if _, err := Verify(tok, ca.Pool); !errors.Is(err, ErrToken) {
		t.Fatalf("err = %v, want ErrToken", err)
	}
}

func TestVerifyGarbage(t *testing.T) {
	rsaTok := readFile(t, "rsa.tsr")
	roots := pool(t, "rsa-ca.pem")
	for name, b := range map[string][]byte{
		"nil":       nil,
		"x":         []byte("x"),
		"tronqué":   rsaTok[:len(rsaTok)/2],
		"un octet":  rsaTok[:len(rsaTok)-1],
		"en trop":   append(append([]byte{}, rsaTok...), 0),
		"sequence0": {0x30, 0x00},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Verify(b, roots); !errors.Is(err, ErrToken) {
				t.Errorf("Verify: err = %v, want ErrToken", err)
			}
			if _, err := ParseToken(b); !errors.Is(err, ErrToken) {
				t.Errorf("ParseToken: err = %v, want ErrToken", err)
			}
		})
	}
}

func TestParseTokenNoVerify(t *testing.T) {
	ca := tsptest.NewCA(t)
	tok := ca.Token(t, testDigest(), big.NewInt(7), tsptest.Options{Tamper: tsptest.Signature})
	if _, err := Verify(tok, ca.Pool); err == nil {
		t.Fatal("Verify accepte une signature altérée")
	}
	info, err := ParseToken(tok)
	if err != nil {
		t.Fatalf("ParseToken: %v", err)
	}
	if !bytes.Equal(info.Digest, testDigest()) || info.Nonce.Int64() != 7 || info.GenTime.IsZero() || info.TSA == "" {
		t.Errorf("info = %+v", info)
	}
}

// tsResp : TimeStampResp minimale pour lire les réponses de tsptest.
type tsResp struct {
	Status struct{ Status int }
	Token  asn1.RawValue `asn1:"optional"`
}

func decodeResp(t *testing.T, der []byte) tsResp {
	t.Helper()
	var r tsResp
	if rest, err := asn1.Unmarshal(der, &r); err != nil || len(rest) > 0 {
		t.Fatalf("TimeStampResp: %v (%d octets en trop)", err, len(rest))
	}
	return r
}

func TestResponse(t *testing.T) {
	ca := tsptest.NewCA(t)
	r := decodeResp(t, ca.Response(t, testDigest(), big.NewInt(9), tsptest.Options{Status: 1}))
	info, err := Verify(r.Token.FullBytes, ca.Pool)
	if err != nil || r.Status.Status != 1 || info.Nonce.Int64() != 9 {
		t.Fatalf("granted: status %d, err %v, nonce %v", r.Status.Status, err, info.Nonce)
	}
	r = decodeResp(t, ca.Response(t, testDigest(), big.NewInt(9), tsptest.Options{Tamper: tsptest.Nonce}))
	if info, err := Verify(r.Token.FullBytes, ca.Pool); err != nil || info.Nonce.Int64() == 9 {
		t.Fatalf("Tamper Nonce: err %v, nonce %v", err, info.Nonce)
	}
	r = decodeResp(t, ca.Response(t, testDigest(), nil, tsptest.Options{Status: 2}))
	if r.Status.Status != 2 || len(r.Token.FullBytes) != 0 {
		t.Fatalf("rejected: status %d, jeton de %d octets", r.Status.Status, len(r.Token.FullBytes))
	}
}

func post(t *testing.T, url string, digest []byte, nonce *big.Int) (*http.Response, []byte) {
	t.Helper()
	req, err := asn1.Marshal(struct {
		Version        int
		MessageImprint struct {
			HashAlgorithm pkix.AlgorithmIdentifier
			HashedMessage []byte
		}
		Nonce   *big.Int `asn1:"optional"`
		CertReq bool
	}{
		Version: 1,
		MessageImprint: struct {
			HashAlgorithm pkix.AlgorithmIdentifier
			HashedMessage []byte
		}{pkix.AlgorithmIdentifier{Algorithm: oidSHA256}, digest},
		Nonce:   nonce,
		CertReq: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(url, "application/timestamp-query", bytes.NewReader(req))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, body
}

func TestServer(t *testing.T) {
	srv := tsptest.NewServer(t, tsptest.Options{})
	digest := sha256.Sum256([]byte("autre"))
	resp, body := post(t, srv.URL, digest[:], big.NewInt(1234))
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/timestamp-reply" {
		t.Fatalf("HTTP %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	info, err := Verify(decodeResp(t, body).Token.FullBytes, srv.CA.Pool)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !bytes.Equal(info.Digest, digest[:]) || info.Nonce.Int64() != 1234 {
		t.Errorf("info = %+v", info)
	}
	if n := srv.Requests.Load(); n != 1 {
		t.Errorf("Requests = %d", n)
	}

	nonceSrv := tsptest.NewServer(t, tsptest.Options{Tamper: tsptest.Nonce})
	_, body = post(t, nonceSrv.URL, digest[:], big.NewInt(1234))
	if info, err := ParseToken(decodeResp(t, body).Token.FullBytes); err != nil || info.Nonce.Int64() == 1234 {
		t.Errorf("Tamper Nonce: err %v, nonce %v", err, info.Nonce)
	}

	htmlSrv := tsptest.NewServer(t, tsptest.Options{Tamper: tsptest.HTMLBody})
	resp, _ = post(t, htmlSrv.URL, digest[:], nil)
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		t.Errorf("HTMLBody: HTTP %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
}

func TestVerifyNilRoots(t *testing.T) {
	ca := tsptest.NewCA(t)
	tok := ca.Token(t, testDigest(), nil, tsptest.Options{})
	saved := goos
	t.Cleanup(func() { goos = saved })

	for _, os := range []string{"darwin", "ios", "windows"} {
		goos = os
		_, err := Verify(tok, nil)
		want := "no trust roots: system roots cannot verify timestamp certificates on " + os + "; configure a CA file"
		if !errors.Is(err, ErrToken) || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", os, err, want)
		}
	}

	goos = "linux"
	_, err := Verify(tok, nil)
	if !errors.Is(err, ErrToken) || strings.Contains(err.Error(), "no trust roots") ||
		!strings.Contains(err.Error(), "signer certificate:") {
		t.Errorf("linux: err = %v, want chain verification error", err)
	}
}
