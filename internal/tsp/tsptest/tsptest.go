// Package tsptest fournit une fausse TSA RFC 3161 pour les tests : une CA et
// des signataires générés au test, de vrais jetons signés (ECDSA P-256 ou
// RSA 2048), des altérations ciblées et un serveur httptest. Il n'importe
// pas internal/tsp (ses tests internes l'importent).
package tsptest

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Tamper désigne une altération du jeton ou de la réponse.
type Tamper int

const (
	None             Tamper = iota
	Signature               // signature altérée
	Imprint                 // empreinte SHA-256 tronquée à 31 octets (jeton signé)
	MessageDigest           // attribut messageDigest faux (jeton signé)
	NoESSCertID             // pas d'attribut signingCertificate(V2)
	WrongESSCertID          // ESSCertID qui ne désigne pas le signataire
	NoEKU                   // signataire sans EKU
	NonCriticalEKU          // EKU timeStamping non critique
	SHA1                    // empreinte et signature en SHA-1
	PSS                     // signature RSA-PSS (signataire RSA forcé)
	Nonce                   // Response et Server : un autre nonce que celui demandé
	HTMLBody                // Server : 200 text/html
	EKUServerAuth           // EKU critique [timeStamping, serverAuth]
	WrongContentType        // attribut contentType = id-data au lieu de TSTInfo
	NoSignerCert            // certificat du signataire absent de certificates
	TwoSigners              // deux SignerInfo identiques
)

// Options règle le jeton produit. Les valeurs nulles donnent un jeton valide
// signé ECDSA P-256, genTime maintenant, signataire valide maintenant ± 1 an.
type Options struct {
	Status                          int // PKIStatus de Response ; >= 2 : pas de jeton
	GenTime                         time.Time
	SignerNotBefore, SignerNotAfter time.Time
	Tamper                          Tamper
	RSA                             bool // signataire RSA 2048 (SID par SKI, ESSCertID v1)
}

// CA est l'autorité de la fausse TSA. Pool et PEM contiennent son certificat.
type CA struct {
	Pool *x509.CertPool
	PEM  []byte

	cert *x509.Certificate
	key  *ecdsa.PrivateKey

	mu     sync.Mutex
	rsaKey *rsa.PrivateKey
}

var (
	oidSignedData    = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}
	oidTSTInfo       = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 16, 1, 4}
	oidContentType   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 3}
	oidMessageDigest = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 4}
	oidSigningCert   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 16, 2, 12}
	oidSigningCertV2 = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 16, 2, 47}
	oidExtKeyUsage   = asn1.ObjectIdentifier{2, 5, 29, 37}
	oidTimeStamping  = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 3, 8}
	oidServerAuth    = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 3, 1}
	oidData          = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 1}
	oidPolicy        = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 99999, 1}

	oidSHA1            = asn1.ObjectIdentifier{1, 3, 14, 3, 2, 26}
	oidSHA256          = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 1}
	oidRSAEncryption   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 1}
	oidSHA1WithRSA     = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 5}
	oidRSAPSS          = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 10}
	oidECDSAWithSHA1   = asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 1}
	oidECDSAWithSHA256 = asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 2}
)

// Structures ASN.1 d'émission (miroir de celles de internal/tsp).
type contentInfo struct {
	ContentType asn1.ObjectIdentifier
	Content     asn1.RawValue `asn1:"explicit,tag:0"`
}

type signedData struct {
	Version          int
	DigestAlgorithms []pkix.AlgorithmIdentifier `asn1:"set"`
	EncapContentInfo encapContentInfo
	Certificates     asn1.RawValue `asn1:"optional,tag:0"`
	SignerInfos      []signerInfo  `asn1:"set"`
}

type encapContentInfo struct {
	EContentType asn1.ObjectIdentifier
	EContent     []byte `asn1:"explicit,tag:0"`
}

type signerInfo struct {
	Version            int
	SID                asn1.RawValue
	DigestAlgorithm    pkix.AlgorithmIdentifier
	SignedAttrs        asn1.RawValue
	SignatureAlgorithm pkix.AlgorithmIdentifier
	Signature          []byte
}

type issuerAndSerial struct {
	Issuer asn1.RawValue
	Serial *big.Int
}

type attribute struct {
	Type   asn1.ObjectIdentifier
	Values []asn1.RawValue `asn1:"set"`
}

type tstInfo struct {
	Version        int
	Policy         asn1.ObjectIdentifier
	MessageImprint messageImprint
	SerialNumber   *big.Int
	GenTime        time.Time `asn1:"generalized"`
	Nonce          *big.Int  `asn1:"optional"`
}

type messageImprint struct {
	HashAlgorithm pkix.AlgorithmIdentifier
	HashedMessage []byte
}

type essCertID struct {
	CertHash []byte
}

type essCertIDv2 struct {
	CertHash []byte // hashAlgorithm absent : SHA-256 par défaut
}

type signingCertificate struct {
	Certs []essCertID
}

type signingCertificateV2 struct {
	Certs []essCertIDv2
}

type pkiStatusInfo struct {
	Status int
}

type timeStampResp struct {
	Status pkiStatusInfo
	Token  asn1.RawValue `asn1:"optional"`
}

type timeStampReq struct {
	Version        int
	MessageImprint messageImprint
	ReqPolicy      asn1.ObjectIdentifier `asn1:"optional"`
	Nonce          *big.Int              `asn1:"optional"`
	CertReq        bool                  `asn1:"optional"`
	Extensions     asn1.RawValue         `asn1:"optional,tag:0"`
}

// must panique sur erreur : ces erreurs (génération de clé, encodage) ne
// dépendent pas du test, et panic reste sûr hors de la goroutine du test
// (gestionnaire HTTP).
func must[T any](v T, err error) T {
	if err != nil {
		panic("tsptest: " + err.Error())
	}
	return v
}

func randSerial(t testing.TB) *big.Int {
	t.Helper()
	return must(rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 64)))
}

// NewCA génère une CA ECDSA P-256 auto-signée valide maintenant ± 10 ans.
func NewCA(t testing.TB) *CA {
	t.Helper()
	key := must(ecdsa.GenerateKey(elliptic.P256(), rand.Reader))
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          randSerial(t),
		Subject:               pkix.Name{CommonName: "tsptest CA"},
		NotBefore:             now.AddDate(-10, 0, 0),
		NotAfter:              now.AddDate(10, 0, 0),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der := must(x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key))
	cert := must(x509.ParseCertificate(der))
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return &CA{
		Pool: pool,
		PEM:  pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		cert: cert,
		key:  key,
	}
}

// rsaSigner renvoie la clé RSA de signataire, générée une seule fois par CA.
func (ca *CA) rsaSigner(t testing.TB) *rsa.PrivateKey {
	t.Helper()
	ca.mu.Lock()
	defer ca.mu.Unlock()
	if ca.rsaKey == nil {
		ca.rsaKey = must(rsa.GenerateKey(rand.Reader, 2048))
	}
	return ca.rsaKey
}

// signer émet un certificat de TSA selon o et renvoie sa clé privée.
func (ca *CA) signer(t testing.TB, o Options, useRSA bool) (*x509.Certificate, crypto.Signer) {
	t.Helper()
	var key crypto.Signer
	if useRSA {
		key = ca.rsaSigner(t)
	} else {
		key = must(ecdsa.GenerateKey(elliptic.P256(), rand.Reader))
	}
	pubDER := must(x509.MarshalPKIXPublicKey(key.Public()))
	ski := sha1.Sum(pubDER)
	now := time.Now()
	notBefore, notAfter := o.SignerNotBefore, o.SignerNotAfter
	if notBefore.IsZero() {
		notBefore = now.AddDate(-1, 0, 0)
	}
	if notAfter.IsZero() {
		notAfter = now.AddDate(1, 0, 0)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          randSerial(t),
		Subject:               pkix.Name{CommonName: "tsptest TSA"},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		SubjectKeyId:          ski[:],
	}
	switch o.Tamper {
	case NoEKU:
	case NonCriticalEKU:
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageTimeStamping}
	default:
		// x509 émet l'EKU non critique : extension écrite à la main.
		usages := []asn1.ObjectIdentifier{oidTimeStamping}
		if o.Tamper == EKUServerAuth {
			usages = append(usages, oidServerAuth)
		}
		val := must(asn1.Marshal(usages))
		tmpl.ExtraExtensions = []pkix.Extension{{Id: oidExtKeyUsage, Critical: true, Value: val}}
	}
	der := must(x509.CreateCertificate(rand.Reader, tmpl, ca.cert, key.Public(), ca.key))
	return must(x509.ParseCertificate(der)), key
}

func marshal(t testing.TB, v any) []byte {
	t.Helper()
	return must(asn1.Marshal(v))
}

func attr(t testing.TB, oid asn1.ObjectIdentifier, v any) attribute {
	t.Helper()
	return attribute{Type: oid, Values: []asn1.RawValue{{FullBytes: marshal(t, v)}}}
}

// Token renvoie un jeton (ContentInfo DER) sur digest, avec nonce s'il est
// non nil. Tamper Nonce et HTMLBody n'ont pas d'effet ici.
func (ca *CA) Token(t testing.TB, digest []byte, nonce *big.Int, o Options) []byte {
	t.Helper()
	useRSA := o.RSA || o.Tamper == PSS
	cert, key := ca.signer(t, o, useRSA)

	genTime := o.GenTime
	if genTime.IsZero() {
		genTime = time.Now()
	}
	imprint := append([]byte(nil), digest...)
	if o.Tamper == Imprint && len(imprint) > 0 {
		imprint = imprint[:len(imprint)-1]
	}
	eContent := marshal(t, tstInfo{
		Version: 1,
		Policy:  oidPolicy,
		MessageImprint: messageImprint{
			HashAlgorithm: pkix.AlgorithmIdentifier{Algorithm: oidSHA256, Parameters: asn1.NullRawValue},
			HashedMessage: imprint,
		},
		SerialNumber: randSerial(t),
		GenTime:      genTime.UTC(),
		Nonce:        nonce,
	})

	// Empreinte des attributs et du contenu.
	h, digestOID := crypto.SHA256, oidSHA256
	if o.Tamper == SHA1 {
		h, digestOID = crypto.SHA1, oidSHA1
	}
	sum := func(b []byte) []byte {
		d := h.New()
		d.Write(b)
		return d.Sum(nil)
	}
	md := sum(eContent)
	if o.Tamper == MessageDigest {
		md[0] ^= 0xff
	}
	contentType := oidTSTInfo
	if o.Tamper == WrongContentType {
		contentType = oidData
	}
	attrs := []attribute{
		attr(t, oidContentType, contentType),
		attr(t, oidMessageDigest, md),
	}
	certForESS := cert.Raw
	if o.Tamper == WrongESSCertID {
		certForESS = ca.cert.Raw
	}
	switch {
	case o.Tamper == NoESSCertID:
	case useRSA:
		s := sha1.Sum(certForESS)
		attrs = append(attrs, attr(t, oidSigningCert, signingCertificate{Certs: []essCertID{{CertHash: s[:]}}}))
	default:
		s := sha256.Sum256(certForESS)
		attrs = append(attrs, attr(t, oidSigningCertV2, signingCertificateV2{Certs: []essCertIDv2{{CertHash: s[:]}}}))
	}
	// SET OF trié par encoding/asn1 ; c'est ce DER (tag 0x31) qui est signé.
	setDER := must(asn1.MarshalWithParams(attrs, "set"))
	attrsHash := sum(setDER)

	var sig []byte
	var sigAlg pkix.AlgorithmIdentifier
	switch k := key.(type) {
	case *rsa.PrivateKey:
		switch o.Tamper {
		case PSS:
			sig = must(rsa.SignPSS(rand.Reader, k, h, attrsHash, nil))
			sigAlg = pkix.AlgorithmIdentifier{Algorithm: oidRSAPSS}
		case SHA1:
			sig = must(rsa.SignPKCS1v15(rand.Reader, k, h, attrsHash))
			sigAlg = pkix.AlgorithmIdentifier{Algorithm: oidSHA1WithRSA, Parameters: asn1.NullRawValue}
		default:
			sig = must(rsa.SignPKCS1v15(rand.Reader, k, h, attrsHash))
			sigAlg = pkix.AlgorithmIdentifier{Algorithm: oidRSAEncryption, Parameters: asn1.NullRawValue}
		}
	case *ecdsa.PrivateKey:
		sig = must(ecdsa.SignASN1(rand.Reader, k, attrsHash))
		sigAlg = pkix.AlgorithmIdentifier{Algorithm: oidECDSAWithSHA256}
		if o.Tamper == SHA1 {
			sigAlg.Algorithm = oidECDSAWithSHA1
		}
	}
	if o.Tamper == Signature {
		sig[len(sig)/2] ^= 0xff
	}

	// SID : SKI pour RSA (version 3), issuer+serial pour ECDSA (version 1).
	si := signerInfo{
		DigestAlgorithm:    pkix.AlgorithmIdentifier{Algorithm: digestOID},
		SignatureAlgorithm: sigAlg,
		Signature:          sig,
	}
	if useRSA {
		si.Version = 3
		si.SID = asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 0, Bytes: cert.SubjectKeyId}
	} else {
		si.Version = 1
		si.SID = asn1.RawValue{FullBytes: marshal(t, issuerAndSerial{
			Issuer: asn1.RawValue{FullBytes: cert.RawIssuer},
			Serial: cert.SerialNumber,
		})}
	}
	signedAttrs := append([]byte(nil), setDER...)
	signedAttrs[0] = 0xa0 // SET OF → [0] IMPLICIT
	si.SignedAttrs = asn1.RawValue{FullBytes: signedAttrs}

	certs, signers := cert.Raw, []signerInfo{si}
	switch o.Tamper {
	case NoSignerCert:
		certs = ca.cert.Raw
	case TwoSigners:
		signers = append(signers, si)
	}
	sd := signedData{
		Version:          3,
		DigestAlgorithms: []pkix.AlgorithmIdentifier{{Algorithm: digestOID}},
		EncapContentInfo: encapContentInfo{EContentType: oidTSTInfo, EContent: eContent},
		Certificates:     asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 0, IsCompound: true, Bytes: certs},
		SignerInfos:      signers,
	}
	return marshal(t, contentInfo{
		ContentType: oidSignedData,
		// encoding/asn1 n'ajoute pas le tag explicite d'un RawValue : [0] écrit ici.
		Content: asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 0, IsCompound: true, Bytes: marshal(t, sd)},
	})
}

// Response renvoie une TimeStampResp DER. Status >= 2 : pas de jeton.
// Tamper Nonce : le jeton porte un autre nonce que celui demandé.
func (ca *CA) Response(t testing.TB, digest []byte, nonce *big.Int, o Options) []byte {
	t.Helper()
	resp := timeStampResp{Status: pkiStatusInfo{Status: o.Status}}
	if o.Status < 2 {
		if o.Tamper == Nonce {
			if nonce == nil {
				nonce = big.NewInt(0)
			}
			nonce = new(big.Int).Add(nonce, big.NewInt(1))
		}
		resp.Token = asn1.RawValue{FullBytes: ca.Token(t, digest, nonce, o)}
	}
	return marshal(t, resp)
}

// Server est une fausse TSA HTTP ; Requests compte les requêtes reçues.
type Server struct {
	URL      string
	CA       *CA
	Requests atomic.Int64
}

// NewServer démarre une fausse TSA (fermée en fin de test) qui décode la
// TimeStampReq et répond Response sur son empreinte et son nonce.
func NewServer(t testing.TB, o Options) *Server {
	t.Helper()
	s := &Server{CA: NewCA(t)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.Requests.Add(1)
		if o.Tamper == HTMLBody {
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, "<html><body>proxy login</body></html>")
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		var req timeStampReq
		if err == nil {
			var rest []byte
			if rest, err = asn1.Unmarshal(body, &req); err == nil && len(rest) > 0 {
				err = asn1.SyntaxError{Msg: "trailing data"}
			}
		}
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/timestamp-reply")
		_, _ = w.Write(s.CA.Response(t, req.MessageImprint.HashedMessage, req.Nonce, o))
	}))
	t.Cleanup(srv.Close)
	s.URL = srv.URL
	return s
}
