# Releasing a version

Checklist for maintainers.
Check each item before you publish.
A version that fails an item is not published.

## 1. Before the tag

- [ ] The branch to release is up to date and the CI is green (`make ci`, `make repro-check`).
- [ ] Go = latest 1.26.x patch release: the release machine and the CI images (`golang:1.26.N` in `.github/workflows/ci.yml`, `.forgejo/workflows/ci.yml` and `.woodpecker.yml`) use the latest published Go 1.26 patch release (≥ 1.26.10 as of 2026-10-22). `govulncheck` reports the standard library vulnerabilities of earlier patch releases. The `vscode` CI job installs Go from the official archive: update its URL and its two SHA-256 sums (amd64, arm64) from <https://go.dev/dl/?mode=json> at the same time.
- [ ] VS Code extension: `version` in `editors/vscode/package.json` is the version of the release, without the `v` (`scripts/vscode-package.sh` refuses any other value). `editors/vscode/CHANGELOG.md` has its entry. The release machine has Node.js 22, npm and `unzip`.
- [ ] The package maintainer (`maintainer` in `packaging/nfpm.yaml`) is the real name and a real contact address, not the placeholder value "Projet tenon <noreply@digicactus.com>".
- [ ] `GOFLAGS=-mod=vendor go test -race ./...` passes locally.
- [ ] `LICENSE` is the official English text of the EUPL-1.2 published by the European Commission. Source and date of the comparison recorded in the [Verified sources](#verified-sources) section.
- [ ] For each preset in `internal/config/presets.go` (`mistral`, `scaleway`, `ovhcloud`, `ollama`, `vllm`):
  - OpenAI-compatible base URL verified in the provider's official documentation;
  - current coding model ID verified;
  - context window (`ContextWindow`) verified;
  - source (URL and date) recorded below.
- [ ] No `TODO(release)` left in `internal/config/presets.go`, or each remaining value is listed below with the reason.
- [ ] The name `tenon` is available: GitHub organization (`tenon-code-eu`), `.fr` and `.eu` domains, INPI and EUIPO trademarks in the software classes.
- [ ] `docs/config.example.toml` loads (`TestConfigExampleLoads`) and the documentation (`README.md`, `docs/`) describes the behavior of the version.

## 2. Build

- [ ] Create the annotated tag: `git tag -a vX.Y.Z -m "tenon vX.Y.Z"`. A lightweight tag is refused. For a dry run, create a throwaway annotated tag the same way in a clone, with `ALLOW_VULN=1`. The tag must carry the version of `editors/vscode/package.json` (for example `git tag -a v0.9.0 -m essai`), or the VS Code packaging step stops.
- [ ] Run `make release VERSION=vX.Y.Z` from a clean tree, with the latest published Go 1.26 patch release (≥ 1.26.10 as of 2026-10-22). The target first runs `make vuln`: if `govulncheck` fails, the release stops.
- [ ] **Never** use `ALLOW_VULN=1` for a real release. This variable is only for local tests: it skips `make vuln`, writes `dist/UNSAFE-DRY-RUN`, and the script then refuses to sign. A `dist/` that contains this file is not published.
- [ ] Archives: generated with GNU tar if `tar` or `gtar` is GNU, otherwise with bsdtar (macOS) from a staging directory with fixed dates. Both paths are deterministic, but their bytes can differ: publish the archives of a single machine, and redo the reproducibility check with the same tool. gzip implementations also differ: publish from a single documented environment (system, tar and gzip versions).
- [ ] Check in `dist/`:
  - 4 `.tar.gz` archives (`linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`) that contain `tenon`, `LICENSE`, `README.md` and `docs/`;
  - the `.deb` and `.rpm` packages for `linux/amd64` and `linux/arm64`;
  - the CycloneDX SBOM `tenon_<version>.cdx.json`;
  - 4 VS Code packages `tenon-vscode-<platform>-<X.Y.Z>.vsix` (`darwin-arm64`, `darwin-x64`, `linux-x64`, `linux-arm64`). `scripts/release.sh` builds them with `scripts/vscode-package.sh` before the sums: each one contains `extension/bin/tenon`, taken from the archive of its target, never rebuilt (`unzip -Z <file>.vsix extension/bin/tenon` shows `-rwx`);
  - `SHA256SUMS`, which covers all artifacts, the `.vsix` included.
- [ ] `./dist/…/tenon --version` prints `tenon vX.Y.Z`.
- [ ] Run `make release VERSION=vX.Y.Z` again: `SHA256SUMS` is identical byte for byte.

## 3. Signature

- [ ] Sign with minisign: `MINISIGN_KEY=<path to the secret key> make release VERSION=vX.Y.Z`. Without `MINISIGN_KEY`, the target prints "signature skipped": **do not publish** in this state. With `MINISIGN_KEY` but without `minisign` installed, the release fails.
- [ ] Verify the signature with the published public key: `minisign -Vm dist/SHA256SUMS -p tenon.pub`.
- [ ] The secret key has never left the maintainer's machine; it is not in the repository nor in the CI.

## 4. Publishing

- [ ] Push the tag to GitHub: `git push origin vX.Y.Z`.
- [ ] Create the release on the forge from the tag.
- [ ] Attach the archives, the packages, the SBOM, `SHA256SUMS` and `SHA256SUMS.minisig`.
- [ ] Release notes: visible changes, configuration changes, new or lifted known limits.
- [ ] Download an artifact from the forge and redo the verification (signature, hash, `tenon --version`).
- [ ] Update the Homebrew tap (repository `tenon-code-eu/homebrew-tap`): `sh scripts/homebrew-formula.sh vX.Y.Z > <tap>/Formula/tenon.rb`, commit, push; then `brew update && brew upgrade tenon && tenon --version`.
- [ ] Check the install script: `TENON_INSTALL_DIR=$(mktemp -d) sh install.sh` installs the new version (signature and hash verified).

### VS Code extension

Publish only the `.vsix` files covered by the signed `SHA256SUMS`, after the signature check above. Never repackage them at this step.

- [ ] Manual, once:
  - Visual Studio Marketplace: sign in with a Microsoft account on <https://marketplace.visualstudio.com/manage> and create the `tenon-code-eu` publisher. No Azure DevOps token is needed: the extension is uploaded through the web page.
  - Open VSX: sign in on <https://open-vsx.org> with GitHub, link an Eclipse account and sign the publisher agreement, create an access token (`OVSX_PAT`), then `npx ovsx create-namespace tenon-code-eu -p $OVSX_PAT` from `editors/vscode`. The token stays on the maintainer's machine, never in the repository or the CI.
- [ ] Optional, not done for v0.9.0 (needs an Eclipse account and the publisher agreement): Open VSX, from `editors/vscode` (so that `npx` runs the version pinned in `package-lock.json`, after `npm ci`): `npx ovsx publish --packagePath ../../dist/tenon-vscode-*.vsix -p $OVSX_PAT`. Use `--packagePath`: the positional argument takes one file only.
- [ ] Marketplace, in the browser: on <https://marketplace.visualstudio.com/manage/publishers/tenon-code-eu>, upload one `dist/tenon-vscode-<platform>-X.Y.Z.vsix` (first release: **New extension > Visual Studio Code**; later releases: **… > Update** on the `tenon` extension), then **… > Update** with each of the other three platform files. Each `.vsix` carries its target platform. Wait for the verification to finish before uploading the next file.
- [ ] Install `tenon-code-eu.tenon` from each registry on macOS and on Linux. **tenon: Show Logs** shows the bundled binary and its version `X.Y.Z`.
- [ ] Known `npm audit` reports in `editors/vscode`: high and moderate issues in dev-only dependencies, as of 2026-10-28: the packaging tools (`@vscode/vsce` 4.0.0: `@azure/msal-node`, `@azure/identity`, `qs`; `ovsx` 1.2.0 and its own `@vscode/vsce` 3.9.2: `undici`, `markdown-it`, `fast-uri`) and the unit-test DOM `happy-dom`. None of them is in the extension: the `.vsix` contains only the esbuild bundle, whose single runtime dependency is `@agentclientprotocol/sdk`. The fixed versions were less than two weeks old on 2026-10-28. Check again at each release and update when a fix is old enough.
- [ ] `@vscode/vsce-sign`: npm 11 may skip its install script (`allowScripts`). `vsce package` does not use it: only Marketplace signing (`--sign-tool`, `--sigzipPath`) and `vsce verify-signature` do, and this release does not use them.

## Verified sources

Fill in for each version: item, source (URL), date of consultation, chosen value.

| Item | Source | Date | Value |
|---|---|---|---|
| `LICENSE` (EUPL-1.2) | European Commission, text version ("TXT-80") linked from <https://interoperable-europe.ec.europa.eu/collection/eupl/eupl-text-eupl-12>: <https://joinup.ec.europa.eu/sites/default/files/custom-page/attachment/2020-03/EUPL-1.2%20EN.txt> | 2026-10-15 | byte-for-byte copy, SHA-256 `6fc9e709ccbfe0d77fbffa2427a983282be2eb88e47b1cdb49f21a83b4d1e665` |
| `mistral`: URL | <https://docs.mistral.ai/api> (examples `https://api.mistral.ai/v1/chat/completions`) | 2026-10-15 | `https://api.mistral.ai/v1` |
| `mistral`: model, window | <https://docs.mistral.ai/getting-started/models/models_overview/> and <https://docs.mistral.ai/models/mistral-medium-3-5-26-04>: "optimized for agentic and coding use cases", API names `mistral-medium-3-5`, `mistral-medium-3`, `mistral-medium-latest`, context "256k". Devstral 2 and Devstral Medium/Small are deprecated, replaced by Mistral Medium 3.5; Codestral 25.08 is for completion | 2026-10-15 | `mistral-medium-3-5`, `256000` |
| `scaleway`: URL | <https://www.scaleway.com/en/docs/generative-apis/quickstart/> (`base_url = "https://api.scaleway.ai/v1"`) | 2026-10-15 | `https://api.scaleway.ai/v1` |
| `scaleway`: model, window | <https://www.scaleway.com/en/docs/generative-apis/reference-content/supported-models/>: `mistral-medium-3.5-128b`, Serverless "Yes", "180k" ("During the preview stage, the context is limited to 180k"); tool calling "Yes". `devstral-small-2505` is end of life (EOL 14/11/2025); so are `devstral-2-123b-instruct-2512` (EOL 01/08/2026) and `qwen3-coder-30b-a3b-instruct` (EOL 01/10/2026) | 2026-10-15 | `mistral-medium-3.5-128b`, `180000` |
| `ovhcloud`: URL, model, window | Official catalog <https://catalog.endpoints.ai.ovh.net/rest/v1/models_v2> (documented by <https://docs.ovhcloud.com/en/guides/public-cloud/ai-machine-learning/ai-endpoints-catalog-api>): `name` `Qwen3.8-27B`, `metadata.model_base_url` `https://oai.endpoints.kepler.ai.cloud.ovh.net/v1`, `metadata.context` "262k", `metadata.model_specs.capabilities.function_calling: true`, `metadata.model_specs.capabilities.use_case_tag` containing `code_chat` (verified on 2026-10-15), `available: true`, `metadata.deployment_information.datacenters`: FR. The `name` is the value passed as `model` in the examples of <https://docs.ovhcloud.com/en/guides/public-cloud/ai-machine-learning/ai-endpoints-responses-api>. `Mistral-Small-3.2-24B-Instruct-2506` is marked `available: false` | 2026-10-15 | `https://oai.endpoints.kepler.ai.cloud.ovh.net/v1`, `Qwen3.8-27B`, `262000` |
| `albert`: URL, key, model, window | <https://guides.ia.numerique.gouv.fr/albert-api/guides/code>: `base_url` `https://albert.api.etalab.gouv.fr/v1`, `{env:ALBERT_API_KEY}`, "Les exemples ci-dessous utilisent `deepseek-v4-flash-0731` (131 072 tokens de contexte au 16 septembre 2026)"; <https://guides.ia.numerique.gouv.fr/albert-api/guides/models>: identifiers change, read `GET /v1/models` (`max_context_length`). Keys reserved for public-sector staff | 2026-10-20 | `https://albert.api.etalab.gouv.fr/v1`, `ALBERT_API_KEY`, `deepseek-v4-flash-0731`, `131072` |
| `codestral`: URL, key, model, window | <https://docs.mistral.ai/models/codestral-25-08>: API name `codestral-2508`, context "128k"; <https://docs.mistral.ai/capabilities/code_generation/>: domain `codestral.mistral.ai` with its own key, `api.mistral.ai` with a La Plateforme key. Key variable `CODESTRAL_API_KEY`: common usage (<https://docs.litellm.ai/docs/providers/codestral>), not an official Mistral name. Tool calling support not stated in the docs | 2026-10-20 | `https://codestral.mistral.ai/v1`, `CODESTRAL_API_KEY`, `codestral-2508`, `128000` |
| `ollama`: URL, model | <https://docs.ollama.com/api/openai-compatibility> (`http://localhost:11434/v1/`); <https://ollama.com/library/devstral> ("the best open source model for coding agents", `ollama run devstral`, 128K) | 2026-10-15 | `http://localhost:11434/v1`, `devstral` |
| `ollama`: window | <https://docs.ollama.com/context-length>: 4k (< 24 GiB VRAM), 32k (24-48 GiB), 256k (≥ 48 GiB); at least 64000 recommended for coding tools | 2026-10-15 | **not verifiable**: `32000` kept, see below |
| `vllm`: URL | <https://docs.vllm.ai/en/latest/cli/serve/> (`--port` "Default: 8000"); <https://docs.vllm.ai/en/latest/serving/online_serving/openai_compatible_server/> (`base_url = "http://localhost:8000/v1"`) | 2026-10-15 | `http://localhost:8000/v1` |
| `vllm`: model, window | <https://docs.vllm.ai/en/latest/cli/serve/>: `--served-model-name` ("If not specified, the model name will be the same as the --model argument"), `--max-model-len` ("If unspecified, will be automatically derived from the model config") | 2026-10-15 | **not verifiable**: `default` and `32000` kept, see below |
| Availability of the name `tenon` | | | |

### Values read from the server (`ollama`, `vllm`)

The served model and the applied window depend on the user's local server: no universal value exists.
tenon reads them at startup (`internal/provider/detect.go`); the configuration (`model`, `context_window`) takes precedence.

- `ollama`: most recently installed model that supports tools (`/api/tags`, `capabilities` field), window of the loaded model (`/api/ps`, `context_length`). Verified on 2026-10-16 with Ollama 0.32.15 and `qwen3:8b`: 4096-token window by default on 18 GiB of unified memory.
- `vllm`: `GET /v1/models` (`id`, `max_model_len`).

## Checks performed on 2026-10-16

- **Dry run on Linux** (container `golang:1.26.9`, GNU tar 1.35, linux/arm64): `make ci` succeeds; `make release VERSION=v0.0.0-test` run twice gives a `SHA256SUMS` identical byte for byte (4 archives, 2 `.deb`, 2 `.rpm`, SBOM); `sha256sum -c` succeeds. The GNU tar path is therefore tested.
- **Name `tenon`**:
  - Codeberg: the `tenon` organization and the `tenon/tenon` repository are free.
  - GitHub: the `tenon-code-eu` organization and the `tenon-code-eu/tenon` repository are created (main repository).
  - Debian, Fedora, Arch, Homebrew packages: name free.
  - Name already taken on npm, PyPI and crates.io (unrelated, inactive projects): do not publish on these registries under this name.
  - Unrelated namesakes: the Tenon.io accessibility service, a small Go library `github.com/kmoneil/tenon` (the module path `github.com/tenon-code-eu/tenon` does not conflict with it).
  - Domains `tenon.fr`, `tenon.eu`, `tenon.dev` (and `.io`, `.com`): already registered. Use GitHub Pages or another domain.
  - **Trademarks (EUIPO/TMview, INPI): search not done** (services unreachable from the tooling). Do it by hand for "TENON", classes 9 and 42, before any public announcement: https://www.tmdn.org/tmview/ and https://data.inpi.fr/.
