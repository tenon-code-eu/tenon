package tsp_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tenon-code-eu/tenon/internal/tsp"
	"github.com/tenon-code-eu/tenon/internal/tsp/tsptest"
)

func testDigest() []byte {
	d := sha256.Sum256([]byte("audit"))
	return d[:]
}

func TestNewRequestRoundTrip(t *testing.T) {
	digest := testDigest()
	der, nonce, err := tsp.NewRequest(digest)
	if err != nil {
		t.Fatal(err)
	}
	var req struct {
		Version        int
		MessageImprint struct {
			Alg  pkix.AlgorithmIdentifier
			Hash []byte
		}
		Nonce   *big.Int
		CertReq bool
	}
	if rest, err := asn1.Unmarshal(der, &req); err != nil || len(rest) > 0 {
		t.Fatalf("decode: %v rest=%d", err, len(rest))
	}
	if req.Version != 1 || !bytes.Equal(req.MessageImprint.Hash, digest) || !req.CertReq {
		t.Fatalf("bad request: %+v", req)
	}
	if !req.MessageImprint.Alg.Algorithm.Equal(asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 1}) {
		t.Fatalf("alg %v", req.MessageImprint.Alg.Algorithm)
	}
	if req.Nonce.Cmp(nonce) != 0 || nonce.BitLen() > 64 {
		t.Fatalf("nonce %v vs %v", req.Nonce, nonce)
	}
	_, nonce2, _ := tsp.NewRequest(digest)
	if nonce.Cmp(nonce2) == 0 {
		t.Fatal("nonces identical")
	}
	if _, _, err := tsp.NewRequest(digest[:31]); err == nil {
		t.Fatal("want error for 31-byte digest")
	}
}

func TestRequestOK(t *testing.T) {
	srv := tsptest.NewServer(t, tsptest.Options{})
	digest := testDigest()
	token, info, err := tsp.Request(context.Background(), http.DefaultClient, srv.URL, digest, srv.CA.Pool)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(info.Digest, digest) {
		t.Fatal("digest differs")
	}
	if _, err := tsp.Verify(token, srv.CA.Pool); err != nil {
		t.Fatal(err)
	}
}

func TestRequestContentType(t *testing.T) {
	var got string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Content-Type")
		if r.Method != http.MethodPost {
			t.Errorf("method %s", r.Method)
		}
		w.WriteHeader(500)
	}))
	defer ts.Close()
	_, _, _ = tsp.Request(context.Background(), ts.Client(), ts.URL, testDigest(), nil)
	if got != "application/timestamp-query" {
		t.Fatalf("content-type %q", got)
	}
}

func TestRequestErrors(t *testing.T) {
	digest := testDigest()
	other := sha256.Sum256([]byte("other"))
	cases := []struct {
		name string
		o    tsptest.Options
		want string
	}{
		{"rejected", tsptest.Options{Status: 2}, "rejected (status 2)"},
		{"nonce", tsptest.Options{Tamper: tsptest.Nonce}, "nonce mismatch"},
		{"html", tsptest.Options{Tamper: tsptest.HTMLBody}, "invalid response"},
		{"imprint", tsptest.Options{Digest: other[:]}, "imprint mismatch"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := tsptest.NewServer(t, c.o)
			_, _, err := tsp.Request(context.Background(), http.DefaultClient, srv.URL, digest, srv.CA.Pool)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err %v, want %q", err, c.want)
			}
		})
	}

	t.Run("http500", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
		defer ts.Close()
		_, _, err := tsp.Request(context.Background(), ts.Client(), ts.URL, digest, nil)
		if err == nil || !strings.Contains(err.Error(), "HTTP 500") {
			t.Fatalf("err %v", err)
		}
	})

	t.Run("wrong roots", func(t *testing.T) {
		srv := tsptest.NewServer(t, tsptest.Options{})
		wrong := tsptest.NewCA(t)
		_, _, err := tsp.Request(context.Background(), http.DefaultClient, srv.URL, digest, wrong.Pool)
		if !errors.Is(err, tsp.ErrToken) {
			t.Fatalf("err %v", err)
		}
	})

	t.Run("empty roots pool", func(t *testing.T) {
		srv := tsptest.NewServer(t, tsptest.Options{})
		_, _, err := tsp.Request(context.Background(), http.DefaultClient, srv.URL, digest, x509.NewCertPool())
		if !errors.Is(err, tsp.ErrToken) {
			t.Fatalf("err %v", err)
		}
	})

	t.Run("too big", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(make([]byte, 2<<20))
		}))
		defer ts.Close()
		_, _, err := tsp.Request(context.Background(), ts.Client(), ts.URL, digest, nil)
		if err == nil || !strings.Contains(err.Error(), "too large") {
			t.Fatalf("err %v", err)
		}
	})
}

func TestRequestTimeout(t *testing.T) {
	release := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer ts.Close()
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, err := tsp.Request(ctx, ts.Client(), ts.URL, testDigest(), nil)
	if err == nil || time.Since(start) > 5*time.Second {
		t.Fatalf("err %v after %v", err, time.Since(start))
	}
}

func TestCheckURL(t *testing.T) {
	const msg = "config: audit.timestamp.url must be https (http only for local hosts)"
	for _, u := range []string{"https://tsa.example.eu/tsr", "http://127.0.0.1:8318/", "http://localhost/"} {
		if err := tsp.CheckURL(u); err != nil {
			t.Errorf("%s: %v", u, err)
		}
	}
	for _, u := range []string{"http://tsa.example.eu/", "ftp://x", "", "https://", "http://:80/"} {
		if err := tsp.CheckURL(u); err == nil || err.Error() != msg {
			t.Errorf("%q: %v", u, err)
		}
	}
}

func TestHost(t *testing.T) {
	if h := tsp.Host("https://TSA.Example.EU:8443/tsr"); h != "tsa.example.eu" {
		t.Fatalf("host %q", h)
	}
}
