package tsp

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"

	"github.com/tenon-code-eu/tenon/internal/profile"
)

// maxResponse borne la taille lue d'une réponse de TSA.
const maxResponse = 1 << 20

// Statuts PKIStatus acceptés (RFC 3161 §2.4.2).
const (
	statusGranted         = 0
	statusGrantedWithMods = 1
)

const configMsg = "config: audit.timestamp.url must be https (http only for local hosts)"

type timeStampReq struct {
	Version        int
	MessageImprint messageImprint
	Nonce          *big.Int
	CertReq        bool
}

// pkiStatusInfo : seuls status est utilisé ; statusString et failInfo sont
// facultatifs.
type pkiStatusInfo struct {
	Status       int
	StatusString asn1.RawValue  `asn1:"optional"`
	FailInfo     asn1.BitString `asn1:"optional"`
}

type timeStampResp struct {
	Status pkiStatusInfo
	Token  asn1.RawValue `asn1:"optional"`
}

// NewRequest encode une TimeStampReq (version 1, SHA-256, nonce aléatoire de
// 64 bits, certReq true) pour un condensat de 32 octets.
func NewRequest(digest []byte) (der []byte, nonce *big.Int, err error) {
	if len(digest) != 32 {
		return nil, nil, fmt.Errorf("digest of %d bytes, want 32", len(digest))
	}
	nonce, err = rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 64))
	if err != nil {
		return nil, nil, err
	}
	der, err = asn1.Marshal(timeStampReq{
		Version: 1,
		MessageImprint: messageImprint{
			HashAlgorithm: pkix.AlgorithmIdentifier{Algorithm: oidSHA256, Parameters: asn1.NullRawValue},
			HashedMessage: digest,
		},
		Nonce:   nonce,
		CertReq: true,
	})
	if err != nil {
		return nil, nil, err
	}
	return der, nonce, nil
}

// ParseResponse décode une TimeStampResp : le PKIStatus et, s'il est
// présent, le jeton (ContentInfo DER, non vérifié).
func ParseResponse(der []byte) (status int, token []byte, err error) {
	var r timeStampResp
	if err := unmarshalAll(der, &r); err != nil {
		return 0, nil, fmt.Errorf("invalid response: %v", err)
	}
	return r.Status.Status, r.Token.FullBytes, nil
}

// Request demande à la TSA de url un jeton sur digest et le vérifie contre
// roots. Le délai est celui de ctx. Une réponse acceptée a le statut 0 ou 1,
// le nonce et le condensat demandés et un jeton valide.
func Request(ctx context.Context, c *http.Client, url string, digest []byte, roots *x509.CertPool) (token []byte, info Info, err error) {
	body, nonce, err := NewRequest(digest)
	if err != nil {
		return nil, Info{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, Info{}, err
	}
	req.Header.Set("Content-Type", "application/timestamp-query")
	resp, err := c.Do(req)
	if err != nil {
		return nil, Info{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, Info{}, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		return nil, Info{}, err
	}
	if len(data) > maxResponse {
		return nil, Info{}, errors.New("response too large")
	}
	status, token, err := ParseResponse(data)
	if err != nil {
		return nil, Info{}, err
	}
	if status != statusGranted && status != statusGrantedWithMods {
		return nil, Info{}, fmt.Errorf("rejected (status %d)", status)
	}
	if len(token) == 0 {
		return nil, Info{}, errors.New("invalid response: missing token")
	}
	info, err = Verify(token, roots)
	if err != nil {
		return nil, Info{}, err
	}
	if info.Nonce == nil || info.Nonce.Cmp(nonce) != 0 {
		return nil, Info{}, errors.New("nonce mismatch")
	}
	if !bytes.Equal(info.Digest, digest) {
		return nil, Info{}, errors.New("imprint mismatch")
	}
	return token, info, nil
}

// CheckURL accepte https pour tout hôte et http pour les seuls hôtes
// locaux ; le message d'erreur est celui de la configuration.
func CheckURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return errors.New(configMsg)
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return nil
	case "http":
		if profile.IsLocalHost(u.Hostname()) {
			return nil
		}
	}
	return errors.New(configMsg)
}

// Host renvoie le nom d'hôte de raw en minuscules ("" si illisible).
func Host(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}
