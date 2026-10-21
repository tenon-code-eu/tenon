// Package profile porte les règles des profils de conformité (eu-only,
// secnumcloud, strict-local). Paquet pur : aucune E/S, aucune résolution DNS.
package profile

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"strings"

	"github.com/tenon-code-eu/tenon/internal/llm"
)

// Name identifie un profil de conformité.
type Name string

const (
	None        Name = ""
	EUOnly      Name = "eu-only"
	SecNumCloud Name = "secnumcloud"
	StrictLocal Name = "strict-local"
)

// QualSecNumCloud est la seule qualification acceptée.
const QualSecNumCloud = "secnumcloud"

// Qualified dit si la liste déclare la qualification secnumcloud.
func Qualified(quals []string) bool {
	for _, q := range quals {
		if q == QualSecNumCloud {
			return true
		}
	}
	return false
}

// ErrProfile est enveloppée par toute violation de profil.
var ErrProfile = errors.New("profile violation")

// violation porte le message exact et satisfait errors.Is(err, ErrProfile).
type violation string

func (v violation) Error() string        { return string(v) }
func (v violation) Is(target error) bool { return target == ErrProfile }

// Parse valide un nom de profil ("" = aucun). Le paquet config ajoute son préfixe.
func Parse(s string) (Name, error) {
	switch Name(s) {
	case None, EUOnly, SecNumCloud, StrictLocal:
		return Name(s), nil
	}
	return None, fmt.Errorf("unknown profile %q (eu-only|secnumcloud|strict-local)", s)
}

func rank(n Name) int {
	switch n {
	case EUOnly:
		return 1
	case SecNumCloud:
		return 2
	case StrictLocal:
		return 3
	}
	return 0
}

// Stricter renvoie le plus strict des deux profils.
func Stricter(a, b Name) Name {
	if rank(b) > rank(a) {
		return b
	}
	return a
}

// juris rend la juridiction lisible ("unknown" si vide).
func juris(j string) string {
	if j == "" {
		return "unknown"
	}
	return j
}

// jurisdictionOK dit si la juridiction est admise par le profil (n != None).
func jurisdictionOK(n Name, j string) bool {
	switch n {
	case EUOnly:
		return j == string(llm.EU) || j == string(llm.Local)
	case SecNumCloud:
		return j == string(llm.EU) || j == string(llm.Local)
	case StrictLocal:
		return j == string(llm.Local)
	}
	return true
}

// hostOf extrait l'hôte d'une URL (vide si illisible).
func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// CheckProvider vérifie le fournisseur sélectionné contre le profil.
func CheckProvider(n Name, p llm.ProviderInfo) error {
	if n == None {
		return nil
	}
	if !jurisdictionOK(n, string(p.Jurisdiction)) {
		return violation(fmt.Sprintf("profile %s: provider %q (%s) not allowed", n, p.ID, juris(string(p.Jurisdiction))))
	}
	// secnumcloud : local avec hôte local, ou eu qualifié.
	if n == SecNumCloud && p.Jurisdiction == llm.EU && !Qualified(p.Qualifications) {
		return violation(fmt.Sprintf("profile %s: provider %q is not declared secnumcloud-qualified", n, p.ID))
	}
	if n == StrictLocal || (n == SecNumCloud && p.Jurisdiction == llm.Local) {
		if host := hostOf(p.BaseURL); !IsLocalHost(host) {
			return violation(fmt.Sprintf("profile %s: provider %q url host %q is not a local address", n, p.ID, host))
		}
	}
	return nil
}

// MCPTarget décrit un serveur MCP à contrôler.
type MCPTarget struct {
	ID, Transport, URL, Jurisdiction string
	Qualifications                   []string
}

// CheckMCP vérifie un serveur MCP contre le profil.
func CheckMCP(n Name, s MCPTarget) error {
	if n == None {
		return nil
	}
	skip := func(reason string) error {
		return violation(fmt.Sprintf("profile %s: MCP server %q (%s) skipped", n, s.ID, reason))
	}
	if !jurisdictionOK(n, s.Jurisdiction) {
		return skip(juris(s.Jurisdiction))
	}
	if n == SecNumCloud && s.Jurisdiction == string(llm.EU) && !Qualified(s.Qualifications) {
		return skip("not declared secnumcloud-qualified")
	}
	// Seul le transport stdio n'a pas d'URL ; tout autre transport est vérifié par hôte.
	if (n == StrictLocal || (n == SecNumCloud && s.Jurisdiction == string(llm.Local))) && s.Transport != "stdio" {
		if host := hostOf(s.URL); !IsLocalHost(host) {
			return skip(fmt.Sprintf("url host %q is not a local address", host))
		}
	}
	return nil
}

// Préfixes locaux : loopback, RFC 1918, ULA IPv6.
var localPrefixes = []netip.Prefix{
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("fc00::/7"),
}

// IsLocalHost dit si host (issu de url.URL.Hostname()) est local : "localhost"
// ou un littéral IP des préfixes locaux. Pas de résolution DNS.
func IsLocalHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if i := strings.IndexByte(host, '%'); i >= 0 {
		host = host[:i]
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	for _, p := range localPrefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}
