<p align="center"><img src="docs/assets/logo-tenon.jpeg" alt="tenon — European open-source coding agent harness" width="480"></p>

<p align="center">
  <a href="https://github.com/tenon-code-eu/tenon/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/tenon-code-eu/tenon/actions/workflows/ci.yml/badge.svg?branch=main"></a>
  <a href="https://github.com/tenon-code-eu/tenon/releases/latest"><img alt="Release" src="https://img.shields.io/github/v/release/tenon-code-eu/tenon?label=release&color=003399"></a>
  <a href="LICENSE"><img alt="Licence EUPL-1.2" src="https://img.shields.io/badge/licence-EUPL--1.2-003399"></a>
  <a href="go.mod"><img alt="Go 1.26" src="https://img.shields.io/github/go-mod/go-version/tenon-code-eu/tenon?color=003399"></a>
  <img alt="Linux · macOS" src="https://img.shields.io/badge/platform-Linux%20%C2%B7%20macOS-003399">
  <a href="https://tenon-code-eu.github.io/tenon/"><img alt="Documentation" src="https://img.shields.io/badge/docs-tenon--code--eu.github.io-FFCC00?labelColor=003399"></a>
  <img alt="Made in Europe" src="https://img.shields.io/badge/made%20in-Europe-FFCC00?labelColor=003399">
</p>

# tenon

**An open-source coding agent for your terminal and your editor, built for Europe.**
It reads your project, proposes changes and runs commands, under your control.

- **European or local models:** Mistral AI, Scaleway, OVHcloud, Albert API, Ollama, vLLM. Nothing leaves the EU without your consent.
- **You approve:** writes, commands and MCP calls wait for your answer by default.
- **Confined commands:** shell commands run in a system sandbox (Landlock, `sandbox-exec`): project-only writes, no network unless allowed.
- **Classified data stays home:** `DIFFUSION RESTREINTE` content goes only to a local model.
- **Proof, not promises:** a hash-chained audit log, RFC 3161 timestamps, and an HTML/JSON report for your DPO.
- **Organisation-ready:** signed policies, compliance profiles, a Cyber Resilience Act assistant, CI templates.
- **In your editor:** a VS Code extension, and Zed, JetBrains or Neovim through the Agent Client Protocol, with the same guarantees.

<p align="center"><img src="docs/assets/demo.gif" alt="Demo: tenon reads main.go, proposes a fix approved in the panel, runs go run . then fixes a second error until it prints 5" width="900"></p>

## Installation

**Homebrew** (macOS, Linux):

```
brew install tenon-code-eu/tap/tenon
```

**Install script** (Linux, macOS). It verifies the SHA-256 hash, and the minisign signature if `minisign` is installed. Read it first: [install.sh](install.sh).

```
curl -fsSL https://raw.githubusercontent.com/tenon-code-eu/tenon/main/install.sh | sh
```

**VS Code:** install **tenon** (`tenon-code-eu.tenon`) from the Marketplace. The extension bundles the tenon binary. For VSCodium and Cursor, install the `.vsix` of your platform from the [latest release](https://github.com/tenon-code-eu/tenon/releases/latest) with **Extensions: Install from VSIX…**.

**Other editors:** see [Editors](docs/editors.md) to run `tenon acp` from Zed, JetBrains or Neovim.

<details>
<summary>Install script options, Go, signed binaries, packages, from source</summary>

**Install script options.** The script installs the latest version in `~/.local/bin`: change the directory with `TENON_INSTALL_DIR`, pin a version with `TENON_VERSION=vX.Y.Z`. `TENON_REQUIRE_SIGNATURE=1` makes the minisign check mandatory.
To install from a mirror (sovereign forge, air-gapped network), set `TENON_DOWNLOAD_BASE` and `TENON_VERSION=vX.Y.Z`. The mirror serves `<base>/<version>/<archive>`, `<base>/<version>/SHA256SUMS` and `<base>/<version>/SHA256SUMS.minisig`; the base must be `https` (`http` only for `localhost` and `127.0.0.1`).

**With Go:**

```
go install github.com/tenon-code-eu/tenon/cmd/tenon@latest
```

**Signed binaries.** Each [release](https://github.com/tenon-code-eu/tenon/releases) provides a `.tar.gz` archive per platform (`linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`), `.deb` and `.rpm` packages, the VS Code `.vsix` packages, a CycloneDX SBOM, and `SHA256SUMS` signed with minisign. The public key is `tenon.pub` at the root of the repository:

```
RWR+vfCWjGO42uRKOxNSrKz5afZPCWJf8iJPO04kFdkhLldZHhxo532F
```

```
minisign -Vm SHA256SUMS -p tenon.pub
sha256sum -c SHA256SUMS --ignore-missing   # macOS: shasum -a 256 -c SHA256SUMS --ignore-missing
sudo apt install ./tenon_*.deb             # Debian, Ubuntu
sudo dnf install ./tenon_*.rpm             # Fedora, RHEL
```

**From source.** You need the latest Go 1.26 patch release (≥ 1.26.10 as of 2026-10-22): earlier ones have known vulnerabilities in the standard library. The dependencies are in the repository; no module proxy is contacted.

```
git clone https://github.com/tenon-code-eu/tenon.git
cd tenon
GOFLAGS=-mod=vendor go build -o tenon ./cmd/tenon
```

</details>

## Quick start

In the terminal, from your project directory:

```
tenon key set mistral                  # stores the key in the system keyring
tenon                                  # interactive interface
tenon -p "explain the project layout"  # one turn without the interface, for scripts and CI
```

The key can also come from the `MISTRAL_API_KEY` variable. Without a key, use a local model: `tenon --provider ollama`.
The interface is in English by default: run `tenon --lang fr` or set `lang = "fr"` in the configuration.

In VS Code, open a folder, then click the tenon icon in the activity bar. The status bar shows the provider, its jurisdiction and the sandbox: `tenon · Mistral · EU · sandboxed`. Cmd+Esc (Ctrl+Esc on Linux) opens the terminal interface beside the editor.

## What tenon gives you

### Sovereignty
- Preconfigured European providers (Mistral AI, Codestral, Scaleway, OVHcloud, the French State's Albert API) or local ones (Ollama, vLLM). Any OpenAI-compatible endpoint works.
- Any send outside the EU needs your explicit consent.
- Data-sensitivity routing: paths and markers (`DIFFUSION RESTREINTE`, `EU RESTRICTED`, your own) make a session sensitive, and it then goes only to a local model ([routing](docs/usage.md#data-sensitivity-routing)).
- Compliance profiles (`eu-only`, `secnumcloud`, `strict-local`) and a provider host allowlist, checked at startup and before each send.
- A signed organisation policy (minisign): a floor that users cannot loosen ([policy](docs/policy.md)).

### Control and safety
- Writes, commands and MCP tool calls wait for your approval by default.
- System sandbox: shell commands and stdio MCP servers are confined (Landlock on Linux, `sandbox-exec` on macOS). `[sandbox] mode = "required"` refuses to run without it ([sandbox](docs/usage.md#system-sandbox)).
- Secrets and some personal data are masked before anything is sent, to the model and to your editor.
- Each prompt is a restore point: `/rewind` (or Esc twice) restores the files, the conversation, or both.

### Proof and compliance
- A hash-chained audit log, checked by `tenon audit verify`, with optional RFC 3161 timestamps.
- `tenon audit export`: an HTML and JSON report for your DPO (providers, consents, permissions, modified files, editor used, incidents), without conversation data unless you ask.
- Cyber Resilience Act assistant: `tenon cra` keeps an SBOM, a VEX, the reporting deadlines and a technical dossier, with an offline OSV scan for CI ([CRA](docs/cra.md)).
- Commits that contain code written by the agent can be marked (`tenon git install-hook`).

### Productivity
- Skills in the open Agent Skills format (`SKILL.md`), loaded by the model or with `/<name>`.
- Sub-agents (`.claude/agents/*.md`) for self-contained tasks, under the same permissions, masking and audit.
- Hooks (Claude Code format) before and after tools and at session start and end, without widening permissions.
- Governed memory between sessions (`project`, `user`, `global`), written with your approval ([memory](docs/usage.md#memory)).
- Token sobriety: measured usage, a lighter context view, a session budget, small models for compaction and sub-agents ([sobriety](docs/usage.md#token-sobriety)).
- MCP servers (stdio and HTTP), under the same jurisdiction, sandbox and audit rules.

### Everywhere you work
- Terminal interface, and a non-interactive mode for scripts and CI, with templates for GitLab CI, Forgejo/Gitea and GitHub Actions ([CI](docs/ci.md)).
- `tenon acp`: an [Agent Client Protocol](https://agentclientprotocol.com) agent for Zed, JetBrains, Neovim and VS Code. tenon's own tools do the work, so every guarantee above still applies ([editors](docs/editors.md)).
- The tenon extension for VS Code: chat panel, permission cards, native diffs, attached selection, `/` skills, past sessions, a sovereignty status bar and a terminal mode ([extension](editors/vscode/README.md)).

## Why tenon

| | **tenon** | Claude Code | Aider | OpenCode | Mistral Vibe |
|---|---|---|---|---|---|
| Licence | EUPL-1.2 | Proprietary | Apache-2.0 | MIT | Apache-2.0 |
| European and local providers built in | ✅ | — | via LiteLLM | via 75+ providers | Mistral |
| Consent before data leaves the EU | ✅ | — | — | — | — |
| Approval before writes and commands | ✅ by default | ✅ | partial | configurable | ✅ |
| System sandbox for commands | ✅ Landlock, sandbox-exec | ✅ bubblewrap, Seatbelt | — | — | via Docker Sandboxes |
| Classified data kept on a local model | ✅ | — | — | — | — |
| Hash-chained, verifiable audit log | ✅ | — | — | — | — |
| Compliance report and RFC 3161 timestamps | ✅ | — | — | — | — |
| Signed organisation policy | ✅ minisign | managed settings | — | — | — |
| Editor integration | ✅ VS Code extension, ACP | ✅ VS Code, JetBrains | community extensions | ✅ VS Code extension, ACP | ✅ VS Code extension, ACP |

“—” means not documented, to our knowledge, on 2026-10-29. Corrections are welcome: open an issue.
Sources: [Claude Code sandboxing](https://code.claude.com/docs/en/sandboxing), [Claude Code in VS Code](https://code.claude.com/docs/en/ide-integrations), [Claude Code in JetBrains](https://code.claude.com/docs/en/jetbrains), [Aider](https://aider.chat/), [OpenCode](https://opencode.ai/), [OpenCode IDE](https://opencode.ai/docs/ide/), [Zed external agents](https://zed.dev/docs/ai/external-agents), [Mistral Vibe](https://mistral.ai/news/devstral-2-vibe-cli), [Vibe in other IDEs](https://docs.mistral.ai/vibe/code/use-vibe-in-other-ides), [Docker Sandboxes for Vibe](https://docs.docker.com/guides/mistral-vibe-sandbox/).

## Documentation

- [Usage guide](docs/usage.md): keys, approval, non-interactive mode, sessions, configuration, providers, MCP, git marking, token sobriety, memory, audit, compliance export.
- [Editors](docs/editors.md): `tenon acp` for Zed, JetBrains, Neovim and VS Code, and the tenon extension.
- [VS Code extension](editors/vscode/README.md): panel, permissions, diffs, status bar, terminal mode, settings.
- [tenon in CI](docs/ci.md): GitLab CI, Forgejo/Gitea and GitHub Actions templates, `--strict`, mirror install.
- [Cyber Resilience Act assistant](docs/cra.md): dossier, offline OSV scan, deadlines, built-in skills.
- [Organisation policy](docs/policy.md): signed floor imposed by an administrator.
- [Security and limits](docs/security.md): sovereignty criteria, guarantees, known limits, recovery.
- [Example configuration](docs/config.example.toml): commented file, without secrets.
- [Report a vulnerability](SECURITY.md) and [contribute](CONTRIBUTING.md).
- [Release checklist](RELEASE.md): for maintainers.
- [Specification](docs/superpowers/specs/2026-10-14-tenon-design.md): design choices (in French).

## License

tenon is distributed under the [EUPL-1.2](LICENSE) license.

## En français

**Un agent de code open source, dans votre terminal et votre éditeur, conçu pour l'Europe.**
Il lit votre projet, propose des modifications et exécute des commandes, sous votre contrôle.

**Installation :** `brew install tenon-code-eu/tap/tenon`, ou le [script d'installation](install.sh). Dans VS Code, installez **tenon** (`tenon-code-eu.tenon`) depuis le Marketplace ; pour VSCodium et Cursor, le `.vsix` de la [dernière release](https://github.com/tenon-code-eu/tenon/releases/latest). Autres éditeurs : [Éditeurs](docs/fr/editeurs.md).

**Démarrage :** `tenon key set mistral`, puis `tenon` dans le dossier du projet. L'interface est en anglais par défaut : `tenon --lang fr` ou `lang = "fr"` dans la configuration.

**Souveraineté.** Fournisseurs européens (Mistral AI, Codestral, Scaleway, OVHcloud, Albert API de l'État) ou locaux (Ollama, vLLM). Tout envoi hors UE demande votre accord. Une session marquée `DIFFUSION RESTREINTE` ne part que vers un modèle local ([routage](docs/fr/utilisation.md#routage-par-sensibilité-des-données)). Profils de conformité (`eu-only`, `secnumcloud`, `strict-local`) et politique d'organisation signée ([politique](docs/fr/politique.md)).

**Contrôle et sécurité.** Écritures, commandes et appels MCP attendent votre validation. Les commandes s'exécutent dans l'isolation système (Landlock, `sandbox-exec`) ([isolation](docs/fr/utilisation.md#isolation-système)). Secrets et données personnelles masqués avant tout envoi, au modèle comme à l'éditeur. Chaque prompt est un point de reprise (`/rewind`).

**Preuve et conformité.** Journal d'audit chaîné et vérifiable, horodatage RFC 3161, rapport HTML et JSON pour votre DPO (`tenon audit export`), assistant Cyber Resilience Act (`tenon cra`) avec scan OSV hors ligne ([CRA](docs/fr/cra.md)).

**Productivité.** Skills (`SKILL.md`), sous-agents, hooks, mémoire gouvernée entre les sessions ([mémoire](docs/fr/utilisation.md#mémoire)), sobriété en tokens ([sobriété](docs/fr/utilisation.md#sobriété-en-tokens)), serveurs MCP.

**Partout.** Interface terminal, mode sans interface pour les scripts et la CI ([CI](docs/fr/ci.md)), `tenon acp` pour Zed, JetBrains et Neovim, et l'extension VS Code : panneau de chat, cartes d'accord, diffs natifs, barre d'état souveraine, mode terminal ([éditeurs](docs/fr/editeurs.md)).

Documentation en français : [guide d'utilisation](docs/fr/utilisation.md), [éditeurs](docs/fr/editeurs.md), [sécurité et limites](docs/fr/securite.md), [politique d'organisation](docs/fr/politique.md), [tenon en CI](docs/fr/ci.md), [assistant CRA](docs/fr/cra.md).
