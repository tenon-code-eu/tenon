#!/bin/sh
# Vérifie la reproductibilité : construit deux fois chaque cible, avec des
# dossiers de sortie et des caches Go distincts, puis compare les SHA-256.
# Sortie non nulle à la moindre différence.
set -eu

VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH:-$(git log -1 --format=%ct)}"
TARGETS="${TARGETS:-linux/amd64 linux/arm64 darwin/amd64 darwin/arm64}"
export SOURCE_DATE_EPOCH
export GOFLAGS="${GOFLAGS:--mod=vendor}"

if command -v sha256sum >/dev/null 2>&1; then
	hash_file() { sha256sum "$1" | cut -d ' ' -f 1; }
else
	hash_file() { shasum -a 256 "$1" | cut -d ' ' -f 1; }
fi

tmp="$(mktemp -d "${TMPDIR:-/tmp}/tenon-repro.XXXXXX")"
# Les caches Go sont en lecture seule : les rendre inscriptibles avant suppression.
trap 'chmod -R u+w "$tmp" 2>/dev/null || true; rm -rf "$tmp"' EXIT INT TERM

build() { # cible dossier_sortie dossier_cache
	os="${1%/*}"
	arch="${1#*/}"
	mkdir -p "$2" "$3"
	CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" GOCACHE="$3" \
		go build -trimpath -buildvcs=false \
		-ldflags "-s -w -buildid= -X main.version=$VERSION" \
		-o "$2/tenon" ./cmd/tenon
}

fail=0
printf '%-14s  %-64s  %s\n' TARGET SHA256 RESULT
for t in $TARGETS; do
	n="$(echo "$t" | tr '/' '-')"
	build "$t" "$tmp/out1/$n" "$tmp/cache1"
	build "$t" "$tmp/out2/$n" "$tmp/cache2"
	h1="$(hash_file "$tmp/out1/$n/tenon")"
	h2="$(hash_file "$tmp/out2/$n/tenon")"
	if [ "$h1" = "$h2" ]; then
		printf '%-14s  %-64s  %s\n' "$t" "$h1" OK
	else
		printf '%-14s  %-64s  %s\n' "$t" "$h1" FAIL
		printf '%-14s  %-64s  %s\n' "" "$h2" "(second build)"
		fail=1
	fi
done

if [ "$fail" -ne 0 ]; then
	echo "repro-check: FAIL, builds non reproductibles" >&2
	exit 1
fi
echo "repro-check: OK (version $VERSION, SOURCE_DATE_EPOCH $SOURCE_DATE_EPOCH)"
