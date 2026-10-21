#!/usr/bin/env bash
# Génère les vecteurs RFC 3161 réels utilisés par les tests de internal/tsp.
# Lancé une seule fois, à la main, depuis ce dossier (jamais par les tests) :
#
#   cd internal/tsp/testdata && OPENSSL=/opt/homebrew/bin/openssl ./gen.sh
#
# Pour chaque famille (rsa, ec) : une CA auto-signée (3650 jours), un
# certificat de TSA signé par cette CA (extendedKeyUsage critique
# timeStamping), une requête sur l'empreinte SHA-256 de digest.hex, une
# réponse "openssl ts -reply -token_out" (jeton seul, ContentInfo DER), puis
# "openssl ts -verify" doit réussir. Les clés privées vivent dans un dossier
# temporaire supprimé à la fin : seuls {rsa,ec}.tsr, {rsa,ec}-ca.pem et
# digest.hex sont commités.
set -euo pipefail

OPENSSL="${OPENSSL:-openssl}"
here="$(pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# Empreinte fixe : sha256("tenon").
if [ ! -f digest.hex ]; then
	printf 'tenon' | "$OPENSSL" dgst -sha256 -r | cut -d' ' -f1 | tr -d '\n' >digest.hex
fi
digest="$(cat digest.hex)"

gen() {
	local name="$1"
	shift
	local d="$work/$name"
	mkdir -p "$d"

	# Clés (CA et signataire), selon la famille.
	if [ "$name" = rsa ]; then
		"$OPENSSL" genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out "$d/ca.key" 2>/dev/null
		"$OPENSSL" genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out "$d/tsa.key" 2>/dev/null
	else
		"$OPENSSL" genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-256 -out "$d/ca.key"
		"$OPENSSL" genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-256 -out "$d/tsa.key"
	fi

	# CA auto-signée.
	"$OPENSSL" req -x509 -new -key "$d/ca.key" -days 3650 -sha256 \
		-subj "/CN=tenon test TSA CA ($name)" \
		-addext "basicConstraints=critical,CA:TRUE" \
		-addext "keyUsage=critical,keyCertSign,cRLSign" \
		-addext "subjectKeyIdentifier=hash" \
		-out "$here/$name-ca.pem"

	# Certificat de la TSA : EKU timeStamping seule et critique.
	"$OPENSSL" req -new -key "$d/tsa.key" -subj "/CN=tenon test TSA ($name)" -out "$d/tsa.csr"
	cat >"$d/tsa.ext" <<-EOF
		basicConstraints=critical,CA:FALSE
		keyUsage=critical,digitalSignature
		extendedKeyUsage=critical,timeStamping
		subjectKeyIdentifier=hash
		authorityKeyIdentifier=keyid
	EOF
	"$OPENSSL" x509 -req -in "$d/tsa.csr" -CA "$here/$name-ca.pem" -CAkey "$d/ca.key" \
		-CAcreateserial -CAserial "$d/ca.srl" -days 3650 -sha256 -extfile "$d/tsa.ext" -out "$d/tsa.pem"

	# Configuration [tsa] d'openssl ts.
	echo 01 >"$d/serial"
	cat >"$d/openssl.cnf" <<-EOF
		[tsa]
		default_tsa = tsa_config

		[tsa_config]
		dir = $d
		serial = $d/serial
		signer_cert = $d/tsa.pem
		signer_key = $d/tsa.key
		signer_digest = sha256
		default_policy = 1.3.6.1.4.1.99999.1
		digests = sha256
		accuracy = secs:1
		ordering = no
		tsa_name = no
		ess_cert_id_chain = no
		ess_cert_id_alg = sha256
	EOF

	"$OPENSSL" ts -query -digest "$digest" -sha256 -cert -out "$d/req.tsq"
	"$OPENSSL" ts -reply -config "$d/openssl.cnf" -queryfile "$d/req.tsq" \
		-token_out -out "$here/$name.tsr"
	"$OPENSSL" ts -verify -token_in -in "$here/$name.tsr" -digest "$digest" \
		-CAfile "$here/$name-ca.pem" -untrusted "$d/tsa.pem"
}

gen rsa
gen ec
