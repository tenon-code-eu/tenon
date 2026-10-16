#!/bin/sh
# Lance un outil Go épinglé : go-tool.sh MODULE/CMD@VERSION [ARGUMENTS...]
# L'outil est installé dans .tools/<nom>@<version>/ (mode module, GOPROXY=direct),
# avec de nouveaux essais si le téléchargement échoue : les hôtes d'origine
# (go.googlesource.com, github.com) renvoient parfois une erreur passagère
# (503). Seule l'installation est réessayée ; le code de sortie de l'outil est
# transmis tel quel (govulncheck : 3 si des vulnérabilités sont trouvées).
set -eu

pkg="${1:?usage: go-tool.sh MODULE/CMD@VERSION [ARGUMENTS...]}"
shift
name="${pkg##*/}"
dir="$(pwd)/.tools/$name"
bin="$dir/${name%@*}"

if [ ! -x "$bin" ]; then
	n=1
	until GOFLAGS= GOPROXY=direct GOBIN="$dir" go install "$pkg"; do
		[ "$n" -lt 4 ] || { printf 'go-tool: cannot install %s after %s attempts.\n' "$pkg" "$n" >&2; exit 1; }
		printf 'go-tool: installing %s failed, retrying in %s s.\n' "$pkg" "$((n * 15))" >&2
		sleep "$((n * 15))"
		n=$((n + 1))
	done
fi
exec "$bin" "$@"
