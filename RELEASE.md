# Publier une version

Liste de contrôle pour les mainteneurs.
Cochez chaque point avant de publier.
Une version qui ne remplit pas un point n'est pas publiée.

## 1. Avant le tag

- [ ] La branche à publier est à jour et la CI est verte (`make ci`, `make repro-check`).
- [ ] `GOFLAGS=-mod=vendor go test -race ./...` passe en local.
- [ ] `LICENSE` est le texte officiel anglais de l'EUPL-1.2 publié par la Commission européenne. Source et date de la comparaison consignées dans la section [Sources vérifiées](#sources-vérifiées).
- [ ] Pour chaque préréglage de `internal/config/presets.go` (`mistral`, `scaleway`, `ovhcloud`, `ollama`, `vllm`) :
  - URL de base compatible OpenAI vérifiée dans la documentation officielle du fournisseur ;
  - identifiant de modèle de code actuel vérifié ;
  - fenêtre de contexte (`ContextWindow`) vérifiée ;
  - source (URL et date) consignée ci-dessous.
- [ ] Plus aucun `TODO(release)` dans `internal/config/presets.go`, ou chaque valeur restante est listée ci-dessous avec la raison.
- [ ] Le nom `tenon` est disponible : organisation Codeberg, domaines `.fr` et `.eu`, marques INPI et EUIPO dans les classes logicielles.
- [ ] `docs/config.example.toml` se charge (`TestConfigExampleLoads`) et la documentation (`README.md`, `docs/`) décrit le comportement de la version.

## 2. Construction

- [ ] Créer le tag annoté : `git tag -a vX.Y.Z -m "tenon vX.Y.Z"`. Un tag léger est refusé. Pour un essai à blanc, créer de même un tag annoté jetable (`git tag -a v0.0.0-test -m essai`) dans un clone, avec `ALLOW_VULN=1`.
- [ ] Lancer `make release VERSION=vX.Y.Z` depuis un arbre propre, avec Go 1.26.6 ou plus récent. La cible exécute d'abord `make vuln` : si `govulncheck` échoue, la release s'arrête.
- [ ] **Ne jamais** utiliser `ALLOW_VULN=1` pour une vraie release. Cette variable ne sert qu'aux essais locaux : elle saute `make vuln`, écrit `dist/UNSAFE-DRY-RUN` et le script refuse alors de signer. Un `dist/` contenant ce fichier ne se publie pas.
- [ ] Archives : générées avec GNU tar si `tar` ou `gtar` est GNU, sinon avec bsdtar (macOS) depuis un dossier d'assemblage à dates fixes. Les deux chemins sont déterministes, mais leurs octets peuvent différer : publier les archives d'une seule machine, et refaire la vérification de reproductibilité avec le même outil. Les implémentations de gzip diffèrent elles aussi : publier depuis un seul environnement documenté (système, versions de tar et de gzip).
- [ ] Vérifier dans `dist/` :
  - 4 archives `.tar.gz` (`linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`) contenant `tenon`, `LICENSE`, `README.md` et `docs/` ;
  - les paquets `.deb` et `.rpm` pour `linux/amd64` et `linux/arm64` ;
  - le SBOM CycloneDX `tenon_<version>.cdx.json` ;
  - `SHA256SUMS`, qui couvre tous les artefacts.
- [ ] `./dist/…/tenon --version` affiche `tenon vX.Y.Z`.
- [ ] Relancer `make release VERSION=vX.Y.Z` : `SHA256SUMS` est identique octet pour octet.

## 3. Signature

- [ ] Signer avec minisign : `MINISIGN_KEY=<chemin de la clé secrète> make release VERSION=vX.Y.Z`. Sans `MINISIGN_KEY`, la cible affiche « signature ignorée » : **ne pas publier** dans cet état. Avec `MINISIGN_KEY` mais sans `minisign` installé, la release échoue.
- [ ] Vérifier la signature avec la clé publique publiée : `minisign -Vm dist/SHA256SUMS -p tenon.pub`.
- [ ] La clé secrète n'a jamais quitté le poste du mainteneur ; elle n'est pas dans le dépôt ni dans la CI.

## 4. Publication sur la forge

- [ ] Pousser le tag sur Codeberg : `git push origin vX.Y.Z`.
- [ ] Créer la version sur la forge à partir du tag.
- [ ] Joindre les archives, les paquets, le SBOM, `SHA256SUMS` et `SHA256SUMS.minisig`.
- [ ] Notes de version : changements visibles, changements de configuration, limites connues nouvelles ou levées.
- [ ] Télécharger un artefact depuis la forge et refaire la vérification (signature, empreinte, `tenon --version`).

## Sources vérifiées

À remplir à chaque version : élément, source (URL), date de consultation, valeur retenue.

| Élément | Source | Date | Valeur |
|---|---|---|---|
| `LICENSE` (EUPL-1.2) | Commission européenne, version texte (« TXT-80 ») liée depuis <https://interoperable-europe.ec.europa.eu/collection/eupl/eupl-text-eupl-12> : <https://joinup.ec.europa.eu/sites/default/files/custom-page/attachment/2020-03/EUPL-1.2%20EN.txt> | 2026-10-15 | copie octet pour octet, SHA-256 `6fc9e709ccbfe0d77fbffa2427a983282be2eb88e47b1cdb49f21a83b4d1e665` |
| `mistral` : URL | <https://docs.mistral.ai/api> (exemples `https://api.mistral.ai/v1/chat/completions`) | 2026-10-15 | `https://api.mistral.ai/v1` |
| `mistral` : modèle, fenêtre | <https://docs.mistral.ai/getting-started/models/models_overview/> et <https://docs.mistral.ai/models/mistral-medium-3-5-26-04> : « optimized for agentic and coding use cases », noms d'API `mistral-medium-3-5`, `mistral-medium-3`, `mistral-medium-latest`, contexte « 256k ». Devstral 2 et Devstral Medium/Small sont dépréciés, remplacés par Mistral Medium 3.5 ; Codestral 25.08 sert à la complétion | 2026-10-15 | `mistral-medium-3-5`, `256000` |
| `scaleway` : URL | <https://www.scaleway.com/en/docs/generative-apis/quickstart/> (`base_url = "https://api.scaleway.ai/v1"`) | 2026-10-15 | `https://api.scaleway.ai/v1` |
| `scaleway` : modèle, fenêtre | <https://www.scaleway.com/en/docs/generative-apis/reference-content/supported-models/> : `mistral-medium-3.5-128b`, Serverless « Yes », « 180k » (« During the preview stage, the context is limited to 180k ») ; fonction d'appel d'outils « Yes ». `devstral-small-2505` est en fin de vie (EOL 14/11/2025) ; `devstral-2-123b-instruct-2512` (EOL 01/08/2026) et `qwen3-coder-30b-a3b-instruct` (EOL 01/10/2026) aussi | 2026-10-15 | `mistral-medium-3.5-128b`, `180000` |
| `ovhcloud` : URL, modèle, fenêtre | Catalogue officiel <https://catalog.endpoints.ai.ovh.net/rest/v1/models_v2> (documenté par <https://docs.ovhcloud.com/en/guides/public-cloud/ai-machine-learning/ai-endpoints-catalog-api>) : `name` `Qwen3.8-27B`, `router_base_url` `https://oai.endpoints.kepler.ai.cloud.ovh.net/v1`, `context` « 262k », `function_calling: true`, cas d'usage `code_chat`, statut `live`, centre de données FR. Le nom `name` est celui passé en `model` dans les exemples de <https://docs.ovhcloud.com/en/guides/public-cloud/ai-machine-learning/ai-endpoints-responses-api>. `Mistral-Small-3.2-24B-Instruct-2506` est marqué `available: false` | 2026-10-15 | `https://oai.endpoints.kepler.ai.cloud.ovh.net/v1`, `Qwen3.8-27B`, `262000` |
| `ollama` : URL, modèle | <https://docs.ollama.com/api/openai-compatibility> (`http://localhost:11434/v1/`) ; <https://ollama.com/library/devstral> (« the best open source model for coding agents », `ollama run devstral`, 128K) | 2026-10-15 | `http://localhost:11434/v1`, `devstral` |
| `ollama` : fenêtre | <https://docs.ollama.com/context-length> : 4k (< 24 GiB VRAM), 32k (24-48 GiB), 256k (≥ 48 GiB) ; 64000 au moins conseillé pour les outils de code | 2026-10-15 | **non vérifiable** : `32000` conservé, voir ci-dessous |
| `vllm` : URL | <https://docs.vllm.ai/en/latest/cli/serve/> (`--port` « Default: 8000 ») ; <https://docs.vllm.ai/en/latest/serving/online_serving/openai_compatible_server/> (`base_url = "http://localhost:8000/v1"`) | 2026-10-15 | `http://localhost:8000/v1` |
| `vllm` : modèle, fenêtre | <https://docs.vllm.ai/en/latest/cli/serve/> : `--served-model-name` (« If not specified, the model name will be the same as the --model argument »), `--max-model-len` (« If unspecified, will be automatically derived from the model config ») | 2026-10-15 | **non vérifiable** : `default` et `32000` conservés, voir ci-dessous |
| Disponibilité du nom `tenon` | | | |

### Valeurs non vérifiées (`TODO(release)` restants)

Ces valeurs dépendent du serveur local de l'utilisateur. Aucune documentation ne fixe une valeur universelle.
L'utilisateur les règle par un bloc `[[providers]]` (`model`, `context_window`).

- `ollama`, `ContextWindow = 32000` : la fenêtre effective est celle du serveur (`OLLAMA_CONTEXT_LENGTH`, par défaut 4k, 32k ou 256k selon la VRAM), pas celle du modèle (128K). Une valeur supérieure à celle du serveur ferait tronquer la conversation sans compaction.
- `vllm`, `Model = "default"` : vLLM répond au nom donné par `--served-model-name`, sinon au chemin passé à `--model`. Aucun nom par défaut n'est documenté.
- `vllm`, `ContextWindow = 32000` : la fenêtre effective est `--max-model-len`, sinon celle de la configuration du modèle servi.
