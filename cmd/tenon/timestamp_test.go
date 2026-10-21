package main

import (
	"net/http"
	"testing"

	"github.com/tenon-code-eu/tenon/internal/profile"
)

func TestTimestampClient(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://tsa.example.eu/tsr", nil)
	for _, p := range []profile.Name{profile.StrictLocal, profile.EUOnly, profile.SecNumCloud} {
		c := timestampClient(p)
		tr := c.Transport.(*http.Transport)
		if (tr.Proxy == nil) != (p == profile.StrictLocal) {
			t.Fatalf("profil %s : Proxy nil = %v", p, tr.Proxy == nil)
		}
		if err := c.CheckRedirect(req, nil); err != http.ErrUseLastResponse {
			t.Fatalf("profil %s : CheckRedirect = %v", p, err)
		}
	}
}
