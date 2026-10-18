<p align="center"><img src="docs/assets/logo-tenon.jpeg" alt="tenon — European open-source coding agent harness" width="480"></p>

# tenon

tenon is a terminal coding agent, written in Go.
It reads your project, proposes changes and runs commands, under your control.

It is designed for Europe:

- the preconfigured providers are European (Mistral AI, Scaleway, OVHcloud) or local (Ollama, vLLM);
- any data sent outside the EU requires your explicit consent;
- by default, writes, commands and MCP tool calls wait for your approval;
- everything is recorded in a hash-chained, verifiable audit log;
- secrets and some personal data are masked before anything is sent;
- commits that contain code written by the agent can be marked;
- reusable skills in the open Agent Skills format (`SKILL.md`), loaded by the model or with `/<name>`, never able to widen permissions.

Any OpenAI-compatible endpoint works.
tenon runs on Linux and macOS.

<p align="center"><img src="docs/assets/demo.gif" alt="Demo: tenon reads main.go, proposes a fix approved in the panel, runs go run . then fixes a second error until it prints 5" width="900"></p>

## Installation

### Homebrew (macOS, Linux)

```
brew install tenon-code-eu/tap/tenon
```

### Install script (Linux, macOS)

```
curl -fsSL https://raw.githubusercontent.com/tenon-code-eu/tenon/main/install.sh | sh
```

The script installs the latest version in `~/.local/bin` (change the directory with `TENON_INSTALL_DIR`, pin a version with `TENON_VERSION=vX.Y.Z`).
It always verifies the SHA-256 hash of the archive, and the minisign signature if `minisign` is installed (`TENON_REQUIRE_SIGNATURE=1` makes it mandatory).
Read it before you run it: [install.sh](install.sh).

### With Go

```
go install github.com/tenon-code-eu/tenon/cmd/tenon@latest
```

### Binaries

Each version published on the [GitHub Releases](https://github.com/tenon-code-eu/tenon/releases) page provides:

- one `.tar.gz` archive per platform: `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`;
- `.deb` and `.rpm` packages for Linux (`amd64`, `arm64`);
- a `SHA256SUMS` file, signed with minisign (`SHA256SUMS.minisig`);
- a CycloneDX SBOM.

Verify the signature with the project's minisign public key, provided at the root of the repository (`tenon.pub`):

```
RWR+vfCWjGO42uRKOxNSrKz5afZPCWJf8iJPO04kFdkhLldZHhxo532F
```

Then verify the hashes:

```
minisign -Vm SHA256SUMS -p tenon.pub
sha256sum -c SHA256SUMS --ignore-missing   # macOS: shasum -a 256 -c SHA256SUMS --ignore-missing
```

Then copy the `tenon` binary to a directory in your `PATH`, or install the package:

```
sudo apt install ./tenon_*.deb   # Debian, Ubuntu
sudo dnf install ./tenon_*.rpm   # Fedora, RHEL
```

### From source

You need Go 1.26 or later. Use the latest Go 1.26 patch release (≥ 1.26.9 as of 2026-10-15): earlier ones have known vulnerabilities in the standard library.
The dependencies are included in the repository: no module proxy is contacted.

```
git clone https://github.com/tenon-code-eu/tenon.git
cd tenon
GOFLAGS=-mod=vendor go build -o tenon ./cmd/tenon
```

## Quick start

```
tenon key set mistral                  # stores the key in the system keyring
tenon                                  # interactive interface, in the project directory
tenon -p "explain the project layout"  # one turn without the interface, for scripts
tenon skills                           # lists the skills found for this project
```

The key can also come from the `MISTRAL_API_KEY` variable.
Without a key, use a local model: `tenon --provider ollama`.

The interface is in English by default.
For French, run with `--lang fr` or set `lang = "fr"` in the configuration.

To mark the commits that contain code written by the agent:

```
tenon git install-hook
```

## Documentation

- [Usage guide](docs/usage.md): keys, approval, non-interactive mode, sessions, configuration, providers, MCP, git marking, audit.
- [Security and limits](docs/security.md): sovereignty criteria, guarantees, known limits, recovery.
- [Example configuration](docs/config.example.toml): commented file, without secrets.
- [Report a vulnerability](SECURITY.md) and [contribute](CONTRIBUTING.md).
- [Release checklist](RELEASE.md): for maintainers.
- [Specification](docs/superpowers/specs/2026-10-14-tenon-design.md): design choices (in French).

## License

tenon is distributed under the [EUPL-1.2](LICENSE) license.

## En français

tenon est un agent de code en terminal, écrit en Go, pour Linux et macOS.
Il lit votre projet, propose des modifications et exécute des commandes, sous votre contrôle.
Les fournisseurs préconfigurés sont européens (Mistral AI, Scaleway, OVHcloud) ou locaux (Ollama, vLLM).
Tout envoi hors de l'UE demande votre accord explicite, et tout est tracé dans un journal d'audit chaîné.
Par défaut, les écritures, les commandes et les appels d'outils MCP attendent votre validation.
Les skills au format ouvert Agent Skills (`SKILL.md`) sont chargés par le modèle ou avec `/<nom>`, sans jamais élargir les permissions.
L'interface est en anglais par défaut : lancez `tenon --lang fr` ou mettez `lang = "fr"` dans la configuration.

Documentation en français : [guide d'utilisation](docs/fr/utilisation.md), [sécurité et limites](docs/fr/securite.md).
