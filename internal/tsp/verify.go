package tsp

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"fmt"
	"math/big"
	"time"
)

// ErrToken est l'erreur racine : toute erreur de ParseToken ou Verify
// l'enveloppe.
var ErrToken = errors.New("invalid timestamp token")

// Info résume un jeton d'horodatage. TSA est le CN du certificat signataire
// (le Subject complet si le CN est vide) ; Nonce vaut nil s'il est absent.
type Info struct {
	GenTime time.Time
	Digest  []byte
	Serial  *big.Int
	Nonce   *big.Int
	TSA     string
}

func tokenErr(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrToken, fmt.Sprintf(format, a...))
}

func unsupported(oid asn1.ObjectIdentifier) error {
	return tokenErr("unsupported algorithm %s", oid)
}

// unmarshalAll décode b entièrement dans v : tout octet en trop est refusé.
func unmarshalAll(b []byte, v any) error {
	rest, err := asn1.Unmarshal(b, v)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return errors.New("trailing data")
	}
	return nil
}

// token est un jeton décodé, sans aucune vérification cryptographique.
type token struct {
	sd     signedData
	si     signerInfo
	tst    tstInfo
	certs  []*x509.Certificate
	signer *x509.Certificate
}

// parse décode ContentInfo → SignedData → TSTInfo, les certificats et
// l'unique SignerInfo, et retrouve le certificat du signataire.
func parse(der []byte) (*token, error) {
	var ci contentInfo
	if err := unmarshalAll(der, &ci); err != nil {
		return nil, tokenErr("content info: %v", err)
	}
	if !ci.ContentType.Equal(oidSignedData) {
		return nil, tokenErr("content type %s, want signedData", ci.ContentType)
	}
	var t token
	// RawValue explicite : Bytes est le contenu du [0], soit la SignedData.
	if err := unmarshalAll(ci.Content.Bytes, &t.sd); err != nil {
		return nil, tokenErr("signed data: %v", err)
	}
	if !t.sd.EncapContentInfo.EContentType.Equal(oidTSTInfo) {
		return nil, tokenErr("encapsulated content type %s, want TSTInfo", t.sd.EncapContentInfo.EContentType)
	}
	if len(t.sd.EncapContentInfo.EContent) == 0 {
		return nil, tokenErr("missing TSTInfo")
	}
	if err := unmarshalAll(t.sd.EncapContentInfo.EContent, &t.tst); err != nil {
		return nil, tokenErr("TSTInfo: %v", err)
	}
	if t.tst.SerialNumber == nil {
		return nil, tokenErr("TSTInfo: missing serial number")
	}
	if n := len(t.sd.SignerInfos); n != 1 {
		return nil, tokenErr("%d signer infos, want exactly one", n)
	}
	t.si = t.sd.SignerInfos[0]
	if len(t.sd.Certificates.Bytes) > 0 {
		certs, err := x509.ParseCertificates(t.sd.Certificates.Bytes)
		if err != nil {
			return nil, tokenErr("certificates: %v", err)
		}
		t.certs = certs
	}
	signer, err := findSigner(t.si.SID, t.certs)
	if err != nil {
		return nil, err
	}
	t.signer = signer
	return &t, nil
}

// findSigner retrouve le certificat désigné par le SID : issuer+serial
// (SEQUENCE) ou subjectKeyIdentifier ([0] IMPLICIT OCTET STRING).
func findSigner(sid asn1.RawValue, certs []*x509.Certificate) (*x509.Certificate, error) {
	switch {
	case sid.Class == asn1.ClassUniversal && sid.Tag == asn1.TagSequence:
		var ias issuerAndSerial
		if err := unmarshalAll(sid.FullBytes, &ias); err != nil || ias.Serial == nil {
			return nil, tokenErr("signer identifier: invalid issuer and serial")
		}
		for _, c := range certs {
			if bytes.Equal(c.RawIssuer, ias.Issuer.FullBytes) && c.SerialNumber.Cmp(ias.Serial) == 0 {
				return c, nil
			}
		}
	case sid.Class == asn1.ClassContextSpecific && sid.Tag == 0 && !sid.IsCompound:
		for _, c := range certs {
			if len(c.SubjectKeyId) > 0 && bytes.Equal(c.SubjectKeyId, sid.Bytes) {
				return c, nil
			}
		}
	default:
		return nil, tokenErr("signer identifier: unknown form")
	}
	return nil, tokenErr("signer certificate not in token")
}

func (t *token) info() Info {
	tsa := t.signer.Subject.CommonName
	if tsa == "" {
		tsa = t.signer.Subject.String()
	}
	return Info{
		GenTime: t.tst.GenTime,
		Digest:  t.tst.MessageImprint.HashedMessage,
		Serial:  t.tst.SerialNumber,
		Nonce:   t.tst.Nonce,
		TSA:     tsa,
	}
}

// ParseToken décode un jeton (ContentInfo DER) sans vérifier ni signature
// ni chaîne de certification.
func ParseToken(der []byte) (Info, error) {
	t, err := parse(der)
	if err != nil {
		return Info{}, err
	}
	return t.info(), nil
}

// Verify décode le jeton et applique tous les contrôles bloquants : TSTInfo
// (version 1, empreinte SHA-256 de 32 octets), attributs signés
// (contentType, messageDigest, ESSCertID ou ESSCertIDv2), signature,
// EKU timeStamping seule et critique, chaîne vers roots à la date genTime.
// roots == nil : racines du système. La révocation n'est pas contrôlée.
func Verify(der []byte, roots *x509.CertPool) (Info, error) {
	t, err := parse(der)
	if err != nil {
		return Info{}, err
	}
	for _, check := range []func() error{
		t.checkTSTInfo,
		t.checkSignedAttrs,
		t.checkSignature,
		t.checkEKU,
		func() error { return t.checkChain(roots) },
	} {
		if err := check(); err != nil {
			return Info{}, err
		}
	}
	return t.info(), nil
}

func (t *token) checkTSTInfo() error {
	if t.tst.Version != 1 {
		return tokenErr("TSTInfo version %d, want 1", t.tst.Version)
	}
	mi := t.tst.MessageImprint
	if !mi.HashAlgorithm.Algorithm.Equal(oidSHA256) {
		return unsupported(mi.HashAlgorithm.Algorithm)
	}
	if len(mi.HashedMessage) != 32 {
		return tokenErr("message imprint of %d bytes, want 32", len(mi.HashedMessage))
	}
	return nil
}

// signedAttrs renvoie la valeur (unique) de chaque attribut signé, par OID.
func (t *token) signedAttrs() (map[string][]byte, error) {
	if len(t.si.SignedAttrs.FullBytes) == 0 {
		return nil, tokenErr("missing signed attributes")
	}
	attrs := map[string][]byte{}
	rest := t.si.SignedAttrs.Bytes
	for len(rest) > 0 {
		var a attribute
		var err error
		if rest, err = asn1.Unmarshal(rest, &a); err != nil {
			return nil, tokenErr("signed attributes: %v", err)
		}
		if a.Values.Class != asn1.ClassUniversal || a.Values.Tag != asn1.TagSet {
			return nil, tokenErr("signed attribute %s: values not a set", a.Type)
		}
		var v asn1.RawValue
		if err := unmarshalAll(a.Values.Bytes, &v); err != nil {
			return nil, tokenErr("signed attribute %s: want exactly one value", a.Type)
		}
		if _, dup := attrs[a.Type.String()]; dup {
			return nil, tokenErr("signed attribute %s: duplicated", a.Type)
		}
		attrs[a.Type.String()] = v.FullBytes
	}
	return attrs, nil
}

func (t *token) checkSignedAttrs() error {
	h, ok := digestHash(t.si.DigestAlgorithm.Algorithm)
	if !ok {
		return unsupported(t.si.DigestAlgorithm.Algorithm)
	}
	attrs, err := t.signedAttrs()
	if err != nil {
		return err
	}

	var ct asn1.ObjectIdentifier
	if raw, ok := attrs[oidContentType.String()]; !ok {
		return tokenErr("missing contentType attribute")
	} else if err := unmarshalAll(raw, &ct); err != nil || !ct.Equal(oidTSTInfo) {
		return tokenErr("contentType attribute is not TSTInfo")
	}

	var md []byte
	if raw, ok := attrs[oidMessageDigest.String()]; !ok {
		return tokenErr("missing messageDigest attribute")
	} else if err := unmarshalAll(raw, &md); err != nil {
		return tokenErr("messageDigest attribute: %v", err)
	}
	d := h.New()
	d.Write(t.sd.EncapContentInfo.EContent)
	if !bytes.Equal(md, d.Sum(nil)) {
		return tokenErr("messageDigest does not match TSTInfo")
	}

	if raw, ok := attrs[oidSigningCertV2.String()]; ok {
		return t.checkESSCertIDv2(raw)
	}
	if raw, ok := attrs[oidSigningCert.String()]; ok {
		return t.checkESSCertID(raw)
	}
	return tokenErr("missing signingCertificate attribute")
}

// checkESSCertID : ESSCertID (SHA-1) du premier certificat = signataire.
func (t *token) checkESSCertID(raw []byte) error {
	var sc signingCertificate
	if err := unmarshalAll(raw, &sc); err != nil || len(sc.Certs) == 0 {
		return tokenErr("invalid signingCertificate attribute")
	}
	id := sc.Certs[0]
	sum := sha1.Sum(t.signer.Raw)
	if !bytes.Equal(id.CertHash, sum[:]) {
		return tokenErr("signingCertificate does not match signer certificate")
	}
	return t.checkIssuerSerial(id.IssuerSerial)
}

// checkESSCertIDv2 : ESSCertIDv2 (SHA-256 par défaut) du premier
// certificat = signataire.
func (t *token) checkESSCertIDv2(raw []byte) error {
	var sc signingCertificateV2
	if err := unmarshalAll(raw, &sc); err != nil || len(sc.Certs) == 0 {
		return tokenErr("invalid signingCertificateV2 attribute")
	}
	id := sc.Certs[0]
	h := crypto.SHA256
	if alg := id.HashAlgorithm.Algorithm; len(alg) > 0 {
		var ok bool
		if h, ok = digestHash(alg); !ok {
			return unsupported(alg)
		}
	}
	d := h.New()
	d.Write(t.signer.Raw)
	if !bytes.Equal(id.CertHash, d.Sum(nil)) {
		return tokenErr("signingCertificateV2 does not match signer certificate")
	}
	return t.checkIssuerSerial(id.IssuerSerial)
}

// checkIssuerSerial : issuerSerial est facultatif ; présent, son numéro de
// série doit être celui du signataire.
func (t *token) checkIssuerSerial(is issuerSerial) error {
	if is.Serial != nil && is.Serial.Cmp(t.signer.SerialNumber) != 0 {
		return tokenErr("signing certificate serial does not match signer certificate")
	}
	return nil
}

// checkSignature vérifie la signature sur le DER des attributs signés
// retagués en SET (0x31). Seuls RSA PKCS#1 v1.5 et ECDSA avec SHA-2 sont
// acceptés.
func (t *token) checkSignature() error {
	digestH, _ := digestHash(t.si.DigestAlgorithm.Algorithm) // déjà contrôlé
	alg := t.si.SignatureAlgorithm.Algorithm
	var h crypto.Hash
	var isRSA bool
	switch {
	case alg.Equal(oidRSAEncryption):
		h, isRSA = digestH, true
	case alg.Equal(oidSHA256WithRSA):
		h, isRSA = crypto.SHA256, true
	case alg.Equal(oidSHA384WithRSA):
		h, isRSA = crypto.SHA384, true
	case alg.Equal(oidSHA512WithRSA):
		h, isRSA = crypto.SHA512, true
	case alg.Equal(oidECDSAWithSHA256):
		h = crypto.SHA256
	case alg.Equal(oidECDSAWithSHA384):
		h = crypto.SHA384
	case alg.Equal(oidECDSAWithSHA512):
		h = crypto.SHA512
	default:
		return unsupported(alg)
	}
	if h != digestH {
		return tokenErr("signature algorithm %s does not match digest algorithm %s", alg, t.si.DigestAlgorithm.Algorithm)
	}

	signed := append([]byte(nil), t.si.SignedAttrs.FullBytes...)
	signed[0] = 0x31 // [0] IMPLICIT → SET OF
	d := h.New()
	d.Write(signed)
	sum := d.Sum(nil)

	switch pub := t.signer.PublicKey.(type) {
	case *rsa.PublicKey:
		if !isRSA {
			return tokenErr("signature algorithm %s does not match RSA key", alg)
		}
		if err := rsa.VerifyPKCS1v15(pub, h, sum, t.si.Signature); err != nil {
			return tokenErr("bad signature")
		}
	case *ecdsa.PublicKey:
		if isRSA {
			return tokenErr("signature algorithm %s does not match ECDSA key", alg)
		}
		if !ecdsa.VerifyASN1(pub, sum, t.si.Signature) {
			return tokenErr("bad signature")
		}
	default:
		return tokenErr("unsupported signer key %T", pub)
	}
	return nil
}

// checkEKU : l'extension EKU du signataire est présente, critique et ne
// contient que timeStamping (RFC 3161 §2.3). x509.Verify ne contrôle ni la
// criticité ni l'exclusivité, d'où l'examen direct de l'extension.
func (t *token) checkEKU() error {
	found := false
	for _, e := range t.signer.Extensions {
		if !e.Id.Equal(oidExtKeyUsage) {
			continue
		}
		found = true
		if !e.Critical {
			return tokenErr("signer extended key usage is not critical")
		}
		var usages []asn1.ObjectIdentifier
		if err := unmarshalAll(e.Value, &usages); err != nil ||
			len(usages) != 1 || !usages[0].Equal(oidEKUTimeStamping) {
			return tokenErr("signer extended key usage must be timeStamping only")
		}
	}
	if !found {
		return tokenErr("signer certificate without extended key usage")
	}
	return nil
}

// checkChain vérifie la chaîne du signataire vers roots à la date genTime :
// un certificat expiré depuis reste valide.
func (t *token) checkChain(roots *x509.CertPool) error {
	inter := x509.NewCertPool()
	for _, c := range t.certs {
		if c != t.signer {
			inter.AddCert(c)
		}
	}
	_, err := t.signer.Verify(x509.VerifyOptions{
		Roots:         roots,
		Intermediates: inter,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageTimeStamping},
		CurrentTime:   t.tst.GenTime,
	})
	if err != nil {
		return tokenErr("signer certificate: %v", err)
	}
	return nil
}
