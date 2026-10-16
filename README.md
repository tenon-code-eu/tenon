<p align="center"><img src="docs/assets/logo-tenon.jpeg" alt="tenon — harness open-source européen et français" width="480"></p>

# tenon

tenon est un agent de code en terminal, écrit en Go.
Il lit votre projet, propose des modifications et exécute des commandes, sous votre contrôle.

Il est conçu pour l'Europe :

- les fournisseurs préconfigurés sont européens (Mistral AI, Scaleway, OVHcloud) ou locaux (Ollama, vLLM) ;
- tout envoi de données hors de l'UE demande votre accord explicite ;
- par défaut, les écritures, les commandes et les appels d'outils MCP attendent votre validation ;
- tout est tracé dans un journal d'audit chaîné et vérifiable ;
- les secrets et des données personnelles sont masqués avant tout envoi ;
- les commits qui contiennent du code écrit par l'agent peuvent être marqués.

Tout point d'accès compatible OpenAI est utilisable.
tenon fonctionne sous Linux et macOS.

## Installation

### Homebrew (macOS, Linux)

```
brew install tenon-code-eu/tap/tenon
```

### Script d'installation (Linux, macOS)

```
curl -fsSL https://raw.githubusercontent.com/tenon-code-eu/tenon/main/install.sh | sh
```

Le script installe la dernière version dans `~/.local/bin` (dossier modifiable avec `TENON_INSTALL_DIR`, version fixée avec `TENON_VERSION=vX.Y.Z`).
Il vérifie toujours l'empreinte SHA-256 de l'archive, et la signature minisign si `minisign` est installé (`TENON_REQUIRE_SIGNATURE=1` la rend obligatoire).
Lisez-le avant de l'exécuter : [install.sh](install.sh).

### Avec Go

```
go install github.com/tenon-code-eu/tenon/cmd/tenon@latest
```

### Binaires

Chaque version publiée sur la page [Releases GitHub](https://github.com/tenon-code-eu/tenon/releases) fournit :

- une archive `.tar.gz` par plateforme : `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64` ;
- des paquets `.deb` et `.rpm` pour Linux (`amd64`, `arm64`) ;
- un fichier `SHA256SUMS`, signé avec minisign (`SHA256SUMS.minisig`) ;
- un SBOM CycloneDX.

Vérifiez la signature avec la clé publique minisign du projet, fournie à la racine du dépôt (`tenon.pub`) :

```
RWR+vfCWjGO42uRKOxNSrKz5afZPCWJf8iJPO04kFdkhLldZHhxo532F
```

Puis vérifiez les empreintes :

```
minisign -Vm SHA256SUMS -p tenon.pub
sha256sum -c SHA256SUMS --ignore-missing   # macOS : shasum -a 256 -c SHA256SUMS --ignore-missing
```

Ensuite, copiez le binaire `tenon` dans un dossier du `PATH`, ou installez le paquet :

```
sudo apt install ./tenon_*.deb   # Debian, Ubuntu
sudo dnf install ./tenon_*.rpm   # Fedora, RHEL
```

### Depuis les sources

Go 1.26 ou plus récent est nécessaire ; utilisez le dernier correctif de Go 1.26 (≥ 1.26.9 au 2026-10-15), les précédents ont des failles connues de la bibliothèque standard.
Les dépendances sont incluses dans le dépôt : aucun proxy de modules n'est contacté.

```
git clone https://github.com/tenon-code-eu/tenon.git
cd tenon
GOFLAGS=-mod=vendor go build -o tenon ./cmd/tenon
```

## Démarrage rapide

```
tenon key set mistral                      # enregistre la clé dans le trousseau système
tenon                                      # interface interactive, dans le dossier du projet
tenon -p "explique la structure du projet" # un tour sans interface, pour les scripts
```

La clé peut aussi venir de la variable `MISTRAL_API_KEY`.
Sans clé, utilisez un modèle local : `tenon --provider ollama`.

Pour marquer les commits qui contiennent du code écrit par l'agent :

```
tenon git install-hook
```

## Documentation

- [Guide d'utilisation](docs/utilisation.md) : touches, validation, mode non interactif, sessions, configuration, fournisseurs, MCP, marquage git, audit.
- [Sécurité et limites](docs/securite.md) : critères de souveraineté, garanties, limites connues, récupération.
- [Exemple de configuration](docs/config.example.toml) : fichier commenté, sans secret.
- [Signaler une vulnérabilité](SECURITY.md) et [contribuer](CONTRIBUTING.md).
- [Liste de contrôle de publication](RELEASE.md) : pour les mainteneurs.
- [Spécification](docs/superpowers/specs/2026-10-14-tenon-design.md) : choix de conception.

## Licence

tenon est distribué sous licence [EUPL-1.2](LICENSE).

## In English

tenon is a terminal coding agent written in Go, for Linux and macOS.
It reads your project, proposes changes and runs commands under your control.

- Preconfigured providers are European (Mistral AI, Scaleway, OVHcloud) or local (Ollama, vLLM). Any OpenAI-compatible endpoint works.
- Sending data outside the EU requires explicit consent (`--allow-non-eu`, or a prompt in the interactive interface).
- By default, writes, commands and MCP tool calls wait for your approval. Everything is recorded in a hash-chained audit log (`tenon audit verify`).
- Secrets and some personal data are masked before anything leaves your machine.
- Commits containing agent-written code can carry `AI-Assisted-By` trailers (`tenon git install-hook`).

Install: `brew install tenon-code-eu/tap/tenon`, or `curl -fsSL https://raw.githubusercontent.com/tenon-code-eu/tenon/main/install.sh | sh`.
Quick start: `tenon key set mistral`, then `tenon` (interactive) or `tenon -p "<prompt>"` (scripts).
Use `--lang en` or `lang = "en"` in the configuration for an English interface.
Documentation: [usage](docs/en/usage.md), [security and limits](docs/en/security.md), [example configuration](docs/config.example.toml) (French reference: [utilisation](docs/utilisation.md), [sécurité](docs/securite.md)).

Licence: [EUPL-1.2](LICENSE).
