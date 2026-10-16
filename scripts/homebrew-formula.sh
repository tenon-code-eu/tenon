#!/bin/sh
# Formule Homebrew de tenon, pour le dépôt tenon-code-eu/homebrew-tap
# (fichier Formula/tenon.rb). Lit les empreintes dans dist/SHA256SUMS, produit
# par `make release`, et écrit la formule sur la sortie standard :
#
#   sh scripts/homebrew-formula.sh v0.1.0 > ../homebrew-tap/Formula/tenon.rb
#
# La formule installe les binaires publiés ; elle ne recompile pas.
set -eu

VERSION="${1:?usage : homebrew-formula.sh vX.Y.Z}"
SUMS="${SUMS:-dist/SHA256SUMS}"
REPO="tenon-code-eu/tenon"

die() { printf 'homebrew-formula : %s\n' "$*" >&2; exit 1; }

[ -f "$SUMS" ] || die "$SUMS introuvable (lancer make release VERSION=$VERSION)."

sum() { # sum OS ARCH
	s="$(awk -v f="tenon_${VERSION}_$1_$2.tar.gz" '$2 == f { print $1 }' "$SUMS")"
	[ -n "$s" ] || die "tenon_${VERSION}_$1_$2.tar.gz absent de $SUMS."
	printf '%s' "$s"
}

url="https://github.com/$REPO/releases/download/$VERSION"
cat <<EOF
# Fichier généré par scripts/homebrew-formula.sh (dépôt $REPO) : ne pas modifier.
class Tenon < Formula
  desc "Agent de code en terminal, souverain et auto-hébergeable"
  homepage "https://github.com/$REPO"
  version "${VERSION#v}"
  license "EUPL-1.2"

  on_macos do
    on_arm do
      url "$url/tenon_${VERSION}_darwin_arm64.tar.gz"
      sha256 "$(sum darwin arm64)"
    end
    on_intel do
      url "$url/tenon_${VERSION}_darwin_amd64.tar.gz"
      sha256 "$(sum darwin amd64)"
    end
  end

  on_linux do
    on_arm do
      url "$url/tenon_${VERSION}_linux_arm64.tar.gz"
      sha256 "$(sum linux arm64)"
    end
    on_intel do
      url "$url/tenon_${VERSION}_linux_amd64.tar.gz"
      sha256 "$(sum linux amd64)"
    end
  end

  def install
    bin.install "tenon"
    doc.install "README.md", "docs/utilisation.md", "docs/securite.md", "docs/config.example.toml"
  end

  test do
    assert_match "tenon $VERSION", shell_output("#{bin}/tenon --version")
  end
end
EOF
