#!/bin/sh
# Paquets .vsix de l'extension VS Code, un par plateforme :
#   scripts/vscode-package.sh vX.Y.Z
# Le binaire tenon de chaque paquet est pris dans l'archive de la release
# (<dist>/tenon_<version>_<os>_<arch>.tar.gz, produite par scripts/release.sh) :
# il n'est jamais reconstruit, il est identique octet pour octet au binaire
# signé. Sortie : <dist>/tenon-vscode-<plateforme>-<X.Y.Z>.vsix.
# scripts/release.sh appelle ce script avant de calculer SHA256SUMS, qui couvre
# donc les .vsix. Aucune signature ici : le mainteneur signe SHA256SUMS.
set -eu

die() { printf 'vscode-package: %s\n' "$*" >&2; exit 1; }

VERSION="${1:?usage: scripts/vscode-package.sh vX.Y.Z}"
TARGETS="${TARGETS:-linux/amd64 linux/arm64 darwin/amd64 darwin/arm64}"
root="$(cd "$(dirname "$0")/.." && pwd)"
ext="$root/editors/vscode"
dist="${DIST:-dist}"
mkdir -p "$dist"
dist="$(cd "$dist" && pwd)"

# Le .vsix porte la version de editors/vscode/package.json, qui est aussi celle
# que l'extension annonce à tenon (clientInfo) : elle doit être celle de la release.
ext_version="$(node -p 'require(process.argv[1]).version' "$ext/package.json")"
[ "$ext_version" = "${VERSION#v}" ] ||
	die "editors/vscode/package.json has version $ext_version, release is $VERSION: update package.json first."

# Zip reproductible : vsce trie les fichiers et fixe leur date à SOURCE_DATE_EPOCH.
SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH:-$(git -C "$root" log -1 --format=%ct)}"
export SOURCE_DATE_EPOCH

vsce_target() { # os/arch -> plateforme VS Code
	case "$1" in
	darwin/arm64) echo darwin-arm64 ;;
	darwin/amd64) echo darwin-x64 ;;
	linux/amd64) echo linux-x64 ;;
	linux/arm64) echo linux-arm64 ;;
	*) die "unsupported target $1 (VS Code: darwin/arm64, darwin/amd64, linux/amd64, linux/arm64)." ;;
	esac
}

tmp="$(mktemp -d "${TMPDIR:-/tmp}/tenon-vscode.XXXXXX")"
trap 'rm -rf "$tmp" "$ext/bin"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# Dépendances exactes du package-lock.json ; vsce lance vscode:prepublish
# (construction de production) pour chaque paquet.
( cd "$ext" && npm ci --no-audit --no-fund )

for t in $TARGETS; do
	os="${t%/*}"
	arch="${t#*/}"
	target="$(vsce_target "$t")"
	name="tenon_${VERSION}_${os}_${arch}"
	archive="$dist/$name.tar.gz"
	[ -f "$archive" ] || die "missing $archive (run scripts/release.sh first)."
	rm -rf "$tmp/x" "$ext/bin"
	mkdir -p "$tmp/x" "$ext/bin"
	tar -xzf "$archive" -C "$tmp/x" "$name/tenon"
	[ -f "$tmp/x/$name/tenon" ] || die "$archive does not contain $name/tenon."
	cp "$tmp/x/$name/tenon" "$ext/bin/tenon"
	chmod 755 "$ext/bin/tenon"
	out="$dist/tenon-vscode-$target-$ext_version.vsix"
	( cd "$ext" && npx --no-install vsce package --target "$target" --no-dependencies --out "$out" )
	[ -s "$out" ] || die "vsce produced no $out."
done
printf 'VS Code packages %s ready in %s/\n' "$ext_version" "$dist"
