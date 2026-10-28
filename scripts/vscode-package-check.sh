#!/bin/sh
# Vérifie scripts/vscode-package.sh avec de faux binaires : un dist/ temporaire
# qui contient les archives au format de scripts/release.sh, puis, pour chaque
# cible, un .vsix qui contient extension/bin/tenon exécutable, identique octet
# pour octet au binaire de l'archive, et dont targetPlatform est la bonne.
# Deux empaquetages successifs donnent les mêmes .vsix (SOURCE_DATE_EPOCH fixe).
# Nécessite node, npm, unzip et le réseau (npm ci).
set -eu

root="$(cd "$(dirname "$0")/.." && pwd)"
ext="$root/editors/vscode"
die() { printf 'vscode-package-check: %s\n' "$*" >&2; exit 1; }

ext_version="$(node -p 'require(process.argv[1]).version' "$ext/package.json")"
version="v$ext_version"
export SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH:-1700000000}"

if command -v sha256sum >/dev/null 2>&1; then
	hash_file() { sha256sum "$1" | cut -d ' ' -f 1; }
else
	hash_file() { shasum -a 256 "$1" | cut -d ' ' -f 1; }
fi

tmp="$(mktemp -d "${TMPDIR:-/tmp}/tenon-vscode-check.XXXXXX")"
trap 'rm -rf "$tmp"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# Archives factices : <nom>.tar.gz contenant <nom>/tenon, comme release.sh.
dist="$tmp/dist"
mkdir -p "$dist" "$tmp/stage"
for t in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64; do
	os="${t%/*}"
	arch="${t#*/}"
	name="tenon_${version}_${os}_${arch}"
	mkdir -p "$tmp/stage/$name"
	printf '#!/bin/sh\necho "tenon %s fake %s"\n' "$version" "$t" >"$tmp/stage/$name/tenon"
	chmod 755 "$tmp/stage/$name/tenon"
	tar -C "$tmp/stage" -czf "$dist/$name.tar.gz" "$name"
done

DIST="$dist" sh "$root/scripts/vscode-package.sh" "$version"

count="$(find "$dist" -maxdepth 1 -name '*.vsix' | wc -l | tr -d ' ')"
[ "$count" = 4 ] || die "expected 4 .vsix in dist/, found $count."
[ ! -e "$ext/bin/tenon" ] || die "editors/vscode/bin/tenon left behind."

for pair in darwin/arm64:darwin-arm64 darwin/amd64:darwin-x64 linux/amd64:linux-x64 linux/arm64:linux-arm64; do
	t="${pair%%:*}"
	target="${pair#*:}"
	os="${t%/*}"
	arch="${t#*/}"
	name="tenon_${version}_${os}_${arch}"
	vsix="$dist/tenon-vscode-$target-$ext_version.vsix"
	[ -f "$vsix" ] || die "missing $vsix."
	unzip -tq "$vsix" >/dev/null || die "$vsix: corrupt zip."
	unzip -l "$vsix" | grep -q ' extension/bin/tenon$' || die "$vsix: extension/bin/tenon missing."
	mode="$(unzip -Z "$vsix" extension/bin/tenon | cut -c 1-4)"
	[ "$mode" = "-rwx" ] || die "$vsix: extension/bin/tenon is not executable ($mode)."
	unzip -p "$vsix" extension/bin/tenon >"$tmp/bin"
	cmp -s "$tmp/bin" "$tmp/stage/$name/tenon" || die "$vsix: bin/tenon differs from $name.tar.gz."
	unzip -p "$vsix" extension.vsixmanifest | grep -q "TargetPlatform=\"$target\"" ||
		die "$vsix: targetPlatform is not $target."
	unzip -p "$vsix" extension/package.json | grep -q "\"version\": \"$ext_version\"" ||
		die "$vsix: version is not $ext_version."
	for f in extension/dist/extension.js extension/dist/webview.js extension/l10n/bundle.l10n.fr.json \
		extension/package.nls.fr.json extension/media/icon.png extension/LICENSE.txt; do
		unzip -l "$vsix" | grep -q " $f\$" || die "$vsix: $f missing."
	done
	if unzip -l "$vsix" | grep -q 'node_modules\|\.map$\|/src/\|/test/'; then
		die "$vsix: unexpected files (node_modules, source maps, sources or tests)."
	fi
	hash_file "$vsix" >"$tmp/$target.sum"
	printf 'OK %s\n' "$(basename "$vsix")"
done

# Reproductibilité : un second empaquetage donne les mêmes octets.
rm -f "$dist"/*.vsix
DIST="$dist" sh "$root/scripts/vscode-package.sh" "$version" >/dev/null
for target in darwin-arm64 darwin-x64 linux-x64 linux-arm64; do
	vsix="$dist/tenon-vscode-$target-$ext_version.vsix"
	[ "$(hash_file "$vsix")" = "$(cat "$tmp/$target.sum")" ] || die "$vsix: not reproducible."
done
printf 'vscode-package-check: OK (4 .vsix, version %s)\n' "$ext_version"
