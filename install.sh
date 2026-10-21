#!/bin/sh
# Installs tenon from the versions published on GitHub.
#
#   curl -fsSL https://raw.githubusercontent.com/tenon-code-eu/tenon/main/install.sh | sh
#
# Variables:
#   TENON_VERSION            version to install (vX.Y.Z); default: the latest,
#                            pre-releases included;
#   TENON_INSTALL_DIR        installation directory; default: $HOME/.local/bin;
#   TENON_REQUIRE_SIGNATURE  1: refuse to install if minisign is missing;
#   TENON_DOWNLOAD_BASE      mirror base URL instead of GitHub (sovereign
#                            forges, air-gapped mirrors). Requires
#                            TENON_VERSION. Layout: <base>/<version>/<archive>,
#                            <base>/<version>/SHA256SUMS and
#                            <base>/<version>/SHA256SUMS.minisig. https only,
#                            except http://localhost and http://127.0.0.1.
#
# The SHA-256 hash of the archive is always verified. The minisign signature
# of SHA256SUMS is verified if minisign is installed: without it, the hash
# only protects against a corrupted download, not against a tampered release.
set -eu

REPO="tenon-code-eu/tenon"
# Project minisign public key (tenon.pub, key ID DAB8638C96F0BD7E).
PUBKEY="RWR+vfCWjGO42uRKOxNSrKz5afZPCWJf8iJPO04kFdkhLldZHhxo532F"

die() { printf 'tenon install: %s\n' "$*" >&2; exit 1; }
info() { printf 'tenon install: %s\n' "$*" >&2; }

# allow_http is set to 1 by main only for a local http mirror.
allow_http=0

fetch() { # fetch URL FILE
	if command -v curl >/dev/null 2>&1; then
		if [ "$allow_http" = 1 ]; then
			curl -fsSL --proto '=https,http' -o "$2" "$1"
		else
			curl -fsSL --proto '=https' --tlsv1.2 -o "$2" "$1"
		fi
	elif command -v wget >/dev/null 2>&1; then
		if [ "$allow_http" = 1 ]; then
			wget -q -O "$2" "$1"
		else
			wget -q --https-only -O "$2" "$1"
		fi
	else
		die "curl or wget is required."
	fi
}

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d ' ' -f 1
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | cut -d ' ' -f 1
	else
		die "sha256sum or shasum is required."
	fi
}

# All the work is in main, called on the last line: a script truncated
# during `curl | sh` does not run halfway.
main() {
	case "$(uname -s)" in
	Linux) os=linux ;;
	Darwin) os=darwin ;;
	*) die "unsupported system: $(uname -s) (Linux or macOS)." ;;
	esac
	case "$(uname -m)" in
	x86_64 | amd64) arch=amd64 ;;
	arm64 | aarch64) arch=arm64 ;;
	*) die "unsupported architecture: $(uname -m) (amd64 or arm64)." ;;
	esac

	mirror="${TENON_DOWNLOAD_BASE:-}"
	if [ -n "$mirror" ]; then
		[ -n "${TENON_VERSION:-}" ] || die "TENON_DOWNLOAD_BASE requires TENON_VERSION=vX.Y.Z."
		while [ "${mirror%/}" != "$mirror" ]; do mirror="${mirror%/}"; done
		case "$mirror" in
		https://*) ;;
		http://*)
			# Exact host match (any port): "@" would let a userinfo spoof it.
			hostport="${mirror#http://}"
			hostport="${hostport%%/*}"
			case "$hostport" in
			*@*) die "TENON_DOWNLOAD_BASE must be https." ;;
			esac
			case "${hostport%%:*}" in
			localhost | 127.0.0.1) allow_http=1 ;;
			*) die "TENON_DOWNLOAD_BASE must be https." ;;
			esac
			;;
		*) die "TENON_DOWNLOAD_BASE must be https." ;;
		esac
	fi

	tmp="$(mktemp -d)"
	trap 'rm -rf "$tmp"' EXIT INT TERM

	version="${TENON_VERSION:-}"
	if [ -z "$version" ]; then
		fetch "https://api.github.com/repos/$REPO/releases?per_page=1" "$tmp/releases.json" ||
			die "release list unreachable; set TENON_VERSION=vX.Y.Z."
		version="$(sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' "$tmp/releases.json" | head -n 1)"
	fi
	printf '%s\n' "$version" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$' ||
		die "invalid version: \"$version\"."

	name="tenon_${version}_${os}_${arch}"
	if [ -n "$mirror" ]; then
		base="$mirror/$version"
	else
		base="https://github.com/$REPO/releases/download/$version"
	fi
	info "downloading tenon $version ($os/$arch)"
	fetch "$base/$name.tar.gz" "$tmp/$name.tar.gz" || die "archive not found: $base/$name.tar.gz"
	fetch "$base/SHA256SUMS" "$tmp/SHA256SUMS" || die "SHA256SUMS not found."

	if command -v minisign >/dev/null 2>&1; then
		fetch "$base/SHA256SUMS.minisig" "$tmp/SHA256SUMS.minisig" || die "signature SHA256SUMS.minisig not found."
		minisign -Vq -P "$PUBKEY" -m "$tmp/SHA256SUMS" -x "$tmp/SHA256SUMS.minisig" ||
			die "invalid minisign signature: installation aborted."
		info "minisign signature verified"
	elif [ "${TENON_REQUIRE_SIGNATURE:-}" = 1 ]; then
		die "minisign not found and TENON_REQUIRE_SIGNATURE=1."
	else
		info "warning: minisign missing, signature not verified (only the hash is)."
	fi

	want="$(awk -v f="$name.tar.gz" '$2 == f { print $1 }' "$tmp/SHA256SUMS")"
	[ -n "$want" ] || die "$name.tar.gz missing from SHA256SUMS."
	got="$(sha256 "$tmp/$name.tar.gz")"
	[ "$want" = "$got" ] || die "wrong SHA-256 hash for $name.tar.gz: installation aborted."
	info "SHA-256 hash verified"

	tar -xzf "$tmp/$name.tar.gz" -C "$tmp"
	[ -f "$tmp/$name/tenon" ] || die "binary missing from the archive."

	dir="${TENON_INSTALL_DIR:-$HOME/.local/bin}"
	mkdir -p "$dir"
	cp "$tmp/$name/tenon" "$tmp/tenon.new"
	chmod 0755 "$tmp/tenon.new"
	mv -f "$tmp/tenon.new" "$dir/tenon" 2>/dev/null || cp "$tmp/tenon.new" "$dir/tenon" ||
		die "cannot write to $dir (set TENON_INSTALL_DIR)."

	info "installed: $dir/tenon ($("$dir/tenon" --version))"
	case ":$PATH:" in
	*":$dir:"*) ;;
	*) info "add $dir to your PATH, for example: export PATH=\"$dir:\$PATH\"" ;;
	esac
}

main "$@"
