# Releasing a version

Checklist for maintainers.
Check each item before you publish.
A version that fails an item is not published.

## 1. Before the tag

- [ ] The branch to release is up to date and the CI is green (`make ci`, `make repro-check`).
- [ ] Go = latest 1.26.x patch release: the release machine and the CI images (`golang:1.26.N` in `.github/workflows/ci.yml`, `.forgejo/workflows/ci.yml` and `.woodpecker.yml`) use the latest published Go 1.26 patch release (≥ 1.26.10 as of 2026-10-22). `govulncheck` reports the standard library vulnerabilities of earlier patch releases.
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

- [ ] Create the annotated tag: `git tag -a vX.Y.Z -m "tenon vX.Y.Z"`. A lightweight tag is refused. For a dry run, create a throwaway annotated tag the same way (`git tag -a v0.0.0-test -m essai`) in a clone, with `ALLOW_VULN=1`.
- [ ] Run `make release VERSION=vX.Y.Z` from a clean tree, with the latest published Go 1.26 patch release (≥ 1.26.10 as of 2026-10-22). The target first runs `make vuln`: if `govulncheck` fails, the release stops.
- [ ] **Never** use `ALLOW_VULN=1` for a real release. This variable is only for local tests: it skips `make vuln`, writes `dist/UNSAFE-DRY-RUN`, and the script then refuses to sign. A `dist/` that contains this file is not published.
- [ ] Archives: generated with GNU tar if `tar` or `gtar` is GNU, otherwise with bsdtar (macOS) from a staging directory with fixed dates. Both paths are deterministic, but their bytes can differ: publish the archives of a single machine, and redo the reproducibility check with the same tool. gzip implementations also differ: publish from a single documented environment (system, tar and gzip versions).
- [ ] Check in `dist/`:
  - 4 `.tar.gz` archives (`linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`) that contain `tenon`, `LICENSE`, `README.md` and `docs/`;
  - the `.deb` and `.rpm` packages for `linux/amd64` and `linux/arm64`;
  - the CycloneDX SBOM `tenon_<version>.cdx.json`;
  - `SHA256SUMS`, which covers all artifacts.
- [ ] `./dist/…/tenon --version` prints `tenon vX.Y.Z`.
- [ ] Run `make release VERSION=vX.Y.Z` again: `SHA256SUMS` is identical byte for byte.

## 3. Signature

- [ ] Sign with minisign: `MINISIGN_KEY=<path to the secret key> make release VERSION=vX.Y.Z`. Without `MINISIGN_KEY`, the target prints "signature skipped": **do not publish** in this state. With `MINISIGN_KEY` but without `minisign` installed, the release fails.
- [ ] Verify the signature with the published public key: `minisign -Vm dist/SHA256SUMS -p tenon.pub`.
- [ ] The secret key has never left the maintainer's machine; it is not in the repository nor in the CI.

## 4. Publishing on the forge

- [ ] Push the tag to GitHub: `git push origin vX.Y.Z`.
- [ ] Create the release on the forge from the tag.
- [ ] Attach the archives, the packages, the SBOM, `SHA256SUMS` and `SHA256SUMS.minisig`.
- [ ] Release notes: visible changes, configuration changes, new or lifted known limits.
- [ ] Download an artifact from the forge and redo the verification (signature, hash, `tenon --version`).
- [ ] Update the Homebrew tap (repository `tenon-code-eu/homebrew-tap`): `sh scripts/homebrew-formula.sh vX.Y.Z > <tap>/Formula/tenon.rb`, commit, push; then `brew update && brew upgrade tenon && tenon --version`.
- [ ] Check the install script: `TENON_INSTALL_DIR=$(mktemp -d) sh install.sh` installs the new version (signature and hash verified).

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
