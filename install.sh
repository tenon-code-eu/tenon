#!/bin/sh
# Installation de tenon depuis les versions publiées sur GitHub.
#
#   curl -fsSL https://raw.githubusercontent.com/tenon-code-eu/tenon/main/install.sh | sh
#
# Variables :
#   TENON_VERSION            version à installer (vX.Y.Z) ; défaut : la plus récente,
#                            préversions comprises ;
#   TENON_INSTALL_DIR        dossier d'installation ; défaut : $HOME/.local/bin ;
#   TENON_REQUIRE_SIGNATURE  1 : refuser l'installation si minisign est absent.
#
# L'empreinte SHA-256 de l'archive est toujours vérifiée. La signature minisign
# de SHA256SUMS l'est si minisign est installé : sans elle, l'empreinte ne
# protège que contre un téléchargement corrompu, pas contre une version altérée.
set -eu

REPO="tenon-code-eu/tenon"
# Clé publique minisign du projet (tenon.pub, identifiant DAB8638C96F0BD7E).
PUBKEY="RWR+vfCWjGO42uRKOxNSrKz5afZPCWJf8iJPO04kFdkhLldZHhxo532F"

die() { printf 'tenon install : %s\n' "$*" >&2; exit 1; }
info() { printf 'tenon install : %s\n' "$*" >&2; }

fetch() { # fetch URL FICHIER
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL --proto '=https' --tlsv1.2 -o "$2" "$1"
	elif command -v wget >/dev/null 2>&1; then
		wget -q --https-only -O "$2" "$1"
	else
		die "curl ou wget est nécessaire."
	fi
}

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d ' ' -f 1
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | cut -d ' ' -f 1
	else
		die "sha256sum ou shasum est nécessaire."
	fi
}

# Tout le travail est dans main, appelée à la dernière ligne : un script
# tronqué pendant `curl | sh` ne s'exécute pas à moitié.
main() {
	case "$(uname -s)" in
	Linux) os=linux ;;
	Darwin) os=darwin ;;
	*) die "système non pris en charge : $(uname -s) (Linux ou macOS)." ;;
	esac
	case "$(uname -m)" in
	x86_64 | amd64) arch=amd64 ;;
	arm64 | aarch64) arch=arm64 ;;
	*) die "architecture non prise en charge : $(uname -m) (amd64 ou arm64)." ;;
	esac

	tmp="$(mktemp -d)"
	trap 'rm -rf "$tmp"' EXIT INT TERM

	version="${TENON_VERSION:-}"
	if [ -z "$version" ]; then
		fetch "https://api.github.com/repos/$REPO/releases?per_page=1" "$tmp/releases.json" ||
			die "liste des versions inaccessible ; fixez TENON_VERSION=vX.Y.Z."
		version="$(sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' "$tmp/releases.json" | head -n 1)"
	fi
	printf '%s\n' "$version" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$' ||
		die "version invalide : « $version »."

	name="tenon_${version}_${os}_${arch}"
	base="https://github.com/$REPO/releases/download/$version"
	info "téléchargement de tenon $version ($os/$arch)"
	fetch "$base/$name.tar.gz" "$tmp/$name.tar.gz" || die "archive introuvable : $base/$name.tar.gz"
	fetch "$base/SHA256SUMS" "$tmp/SHA256SUMS" || die "SHA256SUMS introuvable."

	if command -v minisign >/dev/null 2>&1; then
		fetch "$base/SHA256SUMS.minisig" "$tmp/SHA256SUMS.minisig" || die "signature SHA256SUMS.minisig introuvable."
		minisign -Vq -P "$PUBKEY" -m "$tmp/SHA256SUMS" -x "$tmp/SHA256SUMS.minisig" ||
			die "signature minisign invalide : installation annulée."
		info "signature minisign vérifiée"
	elif [ "${TENON_REQUIRE_SIGNATURE:-}" = 1 ]; then
		die "minisign est introuvable et TENON_REQUIRE_SIGNATURE=1."
	else
		info "attention : minisign absent, signature non vérifiée (seule l'empreinte l'est)."
	fi

	want="$(awk -v f="$name.tar.gz" '$2 == f { print $1 }' "$tmp/SHA256SUMS")"
	[ -n "$want" ] || die "$name.tar.gz absent de SHA256SUMS."
	got="$(sha256 "$tmp/$name.tar.gz")"
	[ "$want" = "$got" ] || die "empreinte SHA-256 incorrecte pour $name.tar.gz : installation annulée."
	info "empreinte SHA-256 vérifiée"

	tar -xzf "$tmp/$name.tar.gz" -C "$tmp"
	[ -f "$tmp/$name/tenon" ] || die "binaire absent de l'archive."

	dir="${TENON_INSTALL_DIR:-$HOME/.local/bin}"
	mkdir -p "$dir"
	cp "$tmp/$name/tenon" "$tmp/tenon.new"
	chmod 0755 "$tmp/tenon.new"
	mv -f "$tmp/tenon.new" "$dir/tenon" 2>/dev/null || cp "$tmp/tenon.new" "$dir/tenon" ||
		die "écriture impossible dans $dir (fixez TENON_INSTALL_DIR)."

	info "installé : $dir/tenon ($("$dir/tenon" --version))"
	case ":$PATH:" in
	*":$dir:"*) ;;
	*) info "ajoutez $dir à votre PATH, par exemple : export PATH=\"$dir:\$PATH\"" ;;
	esac
}

main "$@"
