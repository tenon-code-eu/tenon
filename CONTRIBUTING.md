# Contributing to tenon

Thank you for your interest.
Contributions are accepted in English or in French.

## Before you start

- For a bug: open an issue with the steps to reproduce and the output of `tenon --version`.
- For a new feature: open an issue first to discuss it.
- For a vulnerability: do not use an issue, follow [SECURITY.md](SECURITY.md).

## Environment

- Go: latest Go 1.26 patch release (≥ 1.26.10).
- The dependencies are vendored: build with `GOFLAGS=-mod=vendor`.
- Linux or macOS.

```
GOFLAGS=-mod=vendor go build -o tenon ./cmd/tenon
```

## Checks

Run before each pull request:

```
make ci            # vet, tests with -race, staticcheck, govulncheck
make repro-check   # two builds, identical byte for byte
```

The CI runs the same commands.

## Project rules

- Each new feature or fix comes with its tests.
- A new dependency must be justified in the pull request, then vendored (`go mod vendor`).
- Do not weaken any guarantee described in [docs/security.md](docs/security.md): approval of actions, redaction of secrets, audit log, refusal of data sent outside the EU without consent.
- Displayed texts go through the i18n catalog (English and French).
- No real secret in the code or in the tests: build example tokens by concatenation (`"ghp_" + strings.Repeat("a", 36)`).

## Commits

- One commit per coherent change, with a message in the format `type(scope): summary` (for example `fix(tui): …`).
- Code written with an agent is flagged with the `AI-Assisted-By` trailer (see `tenon git install-hook`).

## License

tenon is distributed under the [EUPL-1.2](LICENSE) license.
By submitting a contribution, you agree that it is distributed under this license.
