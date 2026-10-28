#!/bin/sh
# Chaîne de release : binaires, archives déterministes, paquets deb/rpm, SBOM,
# paquets VS Code (.vsix), SHA256SUMS et signature minisign. Lancé par `make release VERSION=vX.Y.Z`
# (le Makefile exécute `make vuln` avant ce script, sauf avec ALLOW_VULN=1).
set -eu

VERSION="${VERSION:?VERSION missing (make release VERSION=vX.Y.Z)}"
TARGETS="${TARGETS:-linux/amd64 linux/arm64 darwin/amd64 darwin/arm64}"
CYCLONEDX_GOMOD_VERSION="${CYCLONEDX_GOMOD_VERSION:?cyclonedx-gomod version missing}"
NFPM_VERSION_TOOL="${NFPM_TOOL_VERSION:?nfpm version missing}"
# Le Makefile fait foi pour ces options ; valeurs identiques en lancement direct.
BUILD_FLAGS="${BUILD_FLAGS:--trimpath -buildvcs=false}"
LDFLAGS="${LDFLAGS:--s -w -buildid= -X main.version=$VERSION}"
export GOFLAGS="${GOFLAGS:--mod=vendor}"
dist=dist
root="$(pwd)"

die() { printf 'release: %s\n' "$*" >&2; exit 1; }

# --- 1. Garde-fous -----------------------------------------------------------
# Le contrôle des vulnérabilités ne se contourne pas en appelant le script :
# VULN_CHECKED=1 (posé par le Makefile après `make vuln`) ou ALLOW_VULN=1 (essai).
[ "${VULN_CHECKED:-}" = 1 ] || [ "${ALLOW_VULN:-}" = 1 ] ||
	die "refused: go through \`make release\` (govulncheck check is mandatory)."
[ -z "$(git status --porcelain)" ] || die "the git tree is not clean."
head_commit="$(git rev-parse HEAD)"
[ "$(git cat-file -t "refs/tags/$VERSION" 2>/dev/null)" = tag ] ||
	die "tag $VERSION is not an annotated tag (git tag -a)."
tag_commit="$(git rev-parse -q --verify "refs/tags/$VERSION^{commit}")" ||
	die "tag $VERSION does not exist."
[ "$tag_commit" = "$head_commit" ] || die "tag $VERSION does not point to HEAD."

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
	printf 'DRY RUN: ALLOW_VULN=1, vulnerability check skipped. Do not publish.\n' >"$dist/UNSAFE-DRY-RUN"
fi

# --- 2. Binaires et archives ---------------------------------------------------
# Archive : <dist>/<nom>.tar.gz contenant le dossier <nom>/ (tenon, LICENSE,
# README.md, docs/ sans docs/superpowers, qui ne sert qu'au développement).
make_archive() { # nom
	name="$1"
	( cd "$dist" && find "$name" >"$tmp/list.raw" )
	LC_ALL=C sort "$tmp/list.raw" >"$tmp/list"
	[ -s "$tmp/list" ] || die "empty file list for $name."
	if [ -n "$gnutar" ]; then
		"$gnutar" -C "$dist" --sort=name --mtime="@$SOURCE_DATE_EPOCH" \
			--owner=0 --group=0 --numeric-owner --format=gnu \
			--no-recursion -T "$tmp/list" -cf "$tmp/a.tar"
	else
		# bsdtar : sans métadonnées macOS, propriétaire fixe, ordre de la liste triée.
		COPYFILE_DISABLE=1 tar -C "$dist" --format gnutar --no-recursion \
			--uid 0 --gid 0 --uname root --gname root --numeric-owner \
			-T "$tmp/list" -cf "$tmp/a.tar"
	fi
	# Pas de pipefail en POSIX sh : une étape par commande, contrôlée par set -e.
	gzip -n <"$tmp/a.tar" >"$dist/$name.tar.gz"
	gzip -t "$dist/$name.tar.gz"
	tar -tzf "$dist/$name.tar.gz" >"$tmp/check"
	[ -s "$tmp/check" ] || die "empty archive: $name.tar.gz."
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
	git archive --format=tar -o "$tmp/src.tar" HEAD LICENSE README.md docs ':!docs/superpowers'
	tar -xf "$tmp/src.tar" -C "$stage"
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

# --- 4 bis. Paquets VS Code -------------------------------------------------------
# Un .vsix par cible, avec le binaire tenon pris dans l'archive ci-dessus (jamais
# reconstruit). Avant les sommes : SHA256SUMS et sa signature couvrent les .vsix.
TARGETS="$TARGETS" DIST="$dist" sh scripts/vscode-package.sh "$VERSION"

# --- 5. Sommes de contrôle ---------------------------------------------------------
( cd "$dist" && find . -maxdepth 1 -type f \
	\( -name '*.tar.gz' -o -name '*.deb' -o -name '*.rpm' -o -name '*.cdx.json' -o -name '*.vsix' \) \
	>"$tmp/artefacts.raw" )
sed 's|^\./||' "$tmp/artefacts.raw" >"$tmp/artefacts.nom"
LC_ALL=C sort "$tmp/artefacts.nom" >"$tmp/artefacts"
# Jeu exact attendu : une archive et un .vsix par cible, deb et rpm par cible
# linux, un SBOM. Plateforme VS Code : amd64 s'appelle x64.
expected=""
for t in $TARGETS; do
	os="${t%/*}"; arch="${t#*/}"; name="tenon_${VERSION}_${os}_${arch}"
	vsarch="$arch"; [ "$arch" = amd64 ] && vsarch=x64
	expected="$expected$name.tar.gz
tenon-vscode-$os-$vsarch-${VERSION#v}.vsix
"
	if [ "$os" = linux ]; then expected="$expected$name.deb
$name.rpm
"; fi
done
expected="$expected""tenon_${VERSION}.cdx.json
"
printf '%s' "$expected" | LC_ALL=C sort >"$tmp/expected"
cmp -s "$tmp/expected" "$tmp/artefacts" || die "unexpected set of artifacts in $dist/."
: >"$tmp/SHA256SUMS"
# Une somme vide ou mal formée (outil absent, lecture échouée) arrête la release :
# la substitution de commande ne propage pas toujours l'échec à set -e.
while IFS= read -r f; do
	h="$(hash_file "$dist/$f")"
	case "$h" in
	*[!0-9a-f]* | "") die "invalid SHA-256 sum for $f: '$h'." ;;
	esac
	[ "${#h}" -eq 64 ] || die "invalid SHA-256 sum for $f: '$h'."
	printf '%s  %s\n' "$h" "$f" >>"$tmp/SHA256SUMS"
done <"$tmp/artefacts"
mv "$tmp/SHA256SUMS" "$dist/SHA256SUMS"

# --- 6. Signature --------------------------------------------------------------------
if [ -f "$dist/UNSAFE-DRY-RUN" ]; then
	if [ -n "${MINISIGN_KEY:-}" ]; then
		die "signing refused: $dist/UNSAFE-DRY-RUN exists (ALLOW_VULN=1). Never publish this output."
	fi
	printf 'WARNING: unsafe dry run (ALLOW_VULN=1), never publish this output.\n'
fi
if [ -n "${MINISIGN_KEY:-}" ]; then
	# Clé fournie : la signature est attendue, son absence est une erreur.
	command -v minisign >/dev/null 2>&1 ||
		die "MINISIGN_KEY is set but minisign was not found: cannot sign."
	minisign -S -s "$MINISIGN_KEY" -m "$dist/SHA256SUMS"
	printf 'Signed: %s/SHA256SUMS.minisig\n' "$dist"
else
	printf 'signature skipped: MINISIGN_KEY not set. Do not publish without a signature.\n'
fi
printf 'Release %s ready in %s/\n' "$VERSION" "$dist"
