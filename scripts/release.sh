#!/bin/sh
# Chaîne de release : binaires, archives déterministes, paquets deb/rpm, SBOM,
# SHA256SUMS et signature minisign. Lancé par `make release VERSION=vX.Y.Z`
# (le Makefile exécute `make vuln` avant ce script, sauf avec ALLOW_VULN=1).
set -eu

VERSION="${VERSION:?VERSION manquante (make release VERSION=vX.Y.Z)}"
TARGETS="${TARGETS:-linux/amd64 linux/arm64 darwin/amd64 darwin/arm64}"
CYCLONEDX_GOMOD_VERSION="${CYCLONEDX_GOMOD_VERSION:?version de cyclonedx-gomod manquante}"
NFPM_VERSION_TOOL="${NFPM_TOOL_VERSION:?version de nfpm manquante}"
# Le Makefile fait foi pour ces options ; valeurs identiques en lancement direct.
BUILD_FLAGS="${BUILD_FLAGS:--trimpath -buildvcs=false}"
LDFLAGS="${LDFLAGS:--s -w -buildid= -X main.version=$VERSION}"
export GOFLAGS="${GOFLAGS:--mod=vendor}"
dist=dist
root="$(pwd)"

die() { printf 'release : %s\n' "$*" >&2; exit 1; }

# --- 1. Garde-fous -----------------------------------------------------------
[ -z "$(git status --porcelain)" ] || die "l'arbre git n'est pas propre."
head_commit="$(git rev-parse HEAD)"
tag_commit="$(git rev-parse -q --verify "refs/tags/$VERSION^{commit}")" ||
	die "le tag $VERSION n'existe pas."
[ "$tag_commit" = "$head_commit" ] || die "le tag $VERSION ne pointe pas sur HEAD."

SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH:-$(git log -1 --format=%ct)}"
export SOURCE_DATE_EPOCH

# --- Portabilité (macOS / Linux) -----------------------------------------------
if command -v sha256sum >/dev/null 2>&1; then
	hash_file() { sha256sum "$1" | cut -d ' ' -f 1; }
else
	hash_file() { shasum -a 256 "$1" | cut -d ' ' -f 1; }
fi
# Horodatage pour touch -t (CCYYMMDDhhmm.SS, UTC), depuis SOURCE_DATE_EPOCH.
if stamp="$(date -u -d "@$SOURCE_DATE_EPOCH" +%Y%m%d%H%M.%S 2>/dev/null)"; then :
else stamp="$(date -u -r "$SOURCE_DATE_EPOCH" +%Y%m%d%H%M.%S)"; fi
# GNU tar (tar ou gtar) si disponible, sinon bsdtar.
gnutar=""
if tar --version 2>/dev/null | grep -q GNU; then gnutar=tar
elif command -v gtar >/dev/null 2>&1; then gnutar=gtar; fi

tmp="$(mktemp -d "${TMPDIR:-/tmp}/tenon-release.XXXXXX")"
trap 'chmod -R u+w "$tmp" 2>/dev/null || true; rm -rf "$tmp"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

rm -rf "$dist"
mkdir -p "$dist"
if [ "${ALLOW_VULN:-}" = 1 ]; then
	printf 'DRY RUN : ALLOW_VULN=1, contrôle des vulnérabilités ignoré. Ne pas publier.\n' >"$dist/UNSAFE-DRY-RUN"
fi

# --- 2. Binaires et archives ---------------------------------------------------
# Archive : <dist>/<nom>.tar.gz contenant le dossier <nom>/ (tenon, LICENSE,
# README.md, docs/ sans docs/superpowers, qui ne sert qu'au développement).
make_archive() { # nom
	name="$1"
	( cd "$dist" && find "$name" | LC_ALL=C sort >"$tmp/list" )
	if [ -n "$gnutar" ]; then
		"$gnutar" -C "$dist" --sort=name --mtime="@$SOURCE_DATE_EPOCH" \
			--owner=0 --group=0 --numeric-owner --format=gnu \
			--no-recursion -T "$tmp/list" -cf - | gzip -n >"$dist/$name.tar.gz"
	else
		# bsdtar : sans métadonnées macOS, propriétaire fixe, ordre de la liste triée.
		COPYFILE_DISABLE=1 tar -C "$dist" --format gnutar --no-recursion \
			--uid 0 --gid 0 --uname root --gname root --numeric-owner \
			-T "$tmp/list" -cf - | gzip -n >"$dist/$name.tar.gz"
	fi
}

for t in $TARGETS; do
	os="${t%/*}"
	arch="${t#*/}"
	name="tenon_${VERSION}_${os}_${arch}"
	stage="$dist/$name"
	mkdir -p "$stage"
	CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
		go build $BUILD_FLAGS -ldflags "$LDFLAGS" -o "$stage/tenon" ./cmd/tenon
	# Fichiers suivis par git uniquement, à l'état du commit.
	git archive --format=tar HEAD LICENSE README.md docs ':!docs/superpowers' | tar -x -C "$stage"
	# Modes et dates fixes, indépendants de l'umask et de l'horloge.
	find "$stage" -type d -exec chmod 755 {} +
	find "$stage" -type f -exec chmod 644 {} +
	chmod 755 "$stage/tenon"
	find "$stage" -exec env TZ=UTC touch -h -t "$stamp" {} +
	make_archive "$name"
done

# --- 3. Paquets deb et rpm (nfpm) ----------------------------------------------
for t in $TARGETS; do
	os="${t%/*}"
	arch="${t#*/}"
	[ "$os" = linux ] || continue
	name="tenon_${VERSION}_${os}_${arch}"
	for fmt in deb rpm; do
		# nfpm résout `src` depuis le dossier courant : se placer dans l'assemblage.
		( cd "$dist/$name" &&
			NFPM_ARCH="$arch" NFPM_VERSION="${VERSION#v}" GOFLAGS= GOPROXY=direct \
			go run "github.com/goreleaser/nfpm/v2/cmd/nfpm@$NFPM_VERSION_TOOL" package \
			--packager "$fmt" --config "$root/packaging/nfpm.yaml" \
			--target "$root/$dist/$name.$fmt" )
	done
done

# --- 4. SBOM CycloneDX -----------------------------------------------------------
# -noserial -notimestamp : pas de numéro de série ni d'horodatage (reproductible).
GOFLAGS= GOPROXY=direct \
	go run "github.com/CycloneDX/cyclonedx-gomod/cmd/cyclonedx-gomod@$CYCLONEDX_GOMOD_VERSION" \
	app -json -licenses -noserial -notimestamp -main cmd/tenon \
	-output "$dist/tenon_${VERSION}.cdx.json" .

# --- 5. Sommes de contrôle ---------------------------------------------------------
( cd "$dist" && find . -maxdepth 1 -type f \
	\( -name '*.tar.gz' -o -name '*.deb' -o -name '*.rpm' -o -name '*.cdx.json' \) |
	sed 's|^\./||' | LC_ALL=C sort >"$tmp/artefacts" )
: >"$dist/SHA256SUMS"
while IFS= read -r f; do
	printf '%s  %s\n' "$(hash_file "$dist/$f")" "$f" >>"$dist/SHA256SUMS"
done <"$tmp/artefacts"

# --- 6. Signature --------------------------------------------------------------------
if [ -f "$dist/UNSAFE-DRY-RUN" ]; then
	if [ -n "${MINISIGN_KEY:-}" ]; then
		die "signature refusée : $dist/UNSAFE-DRY-RUN existe (ALLOW_VULN=1). Ne jamais publier ce résultat."
	fi
	printf 'ATTENTION : dry run non sûr (ALLOW_VULN=1), ne jamais publier ce résultat.\n'
fi
if [ -n "${MINISIGN_KEY:-}" ] && command -v minisign >/dev/null 2>&1; then
	minisign -S -s "$MINISIGN_KEY" -m "$dist/SHA256SUMS"
	printf 'Signé : %s/SHA256SUMS.minisig\n' "$dist"
else
	printf 'signature ignorée : MINISIGN_KEY non défini ou minisign absent. Ne pas publier sans signature.\n'
fi
printf 'Release %s prête dans %s/\n' "$VERSION" "$dist"
