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

- [ ] Créer le tag annoté : `git tag -a vX.Y.Z -m "tenon vX.Y.Z"`.
- [ ] Lancer `make release VERSION=vX.Y.Z` depuis un arbre propre.
- [ ] Vérifier dans `dist/` :
  - 4 archives `.tar.gz` (`linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`) contenant `tenon`, `LICENSE`, `README.md` et `docs/` ;
  - les paquets `.deb` et `.rpm` pour `linux/amd64` et `linux/arm64` ;
  - le SBOM CycloneDX `tenon_<version>.cdx.json` ;
  - `SHA256SUMS`, qui couvre tous les artefacts.
- [ ] `./dist/…/tenon --version` affiche `tenon vX.Y.Z`.
- [ ] Relancer `make release VERSION=vX.Y.Z` : `SHA256SUMS` est identique octet pour octet.

## 3. Signature

- [ ] Signer avec minisign : `MINISIGN_KEY=<chemin de la clé secrète> make release VERSION=vX.Y.Z`. Sans `MINISIGN_KEY`, la cible affiche « signature ignorée » : **ne pas publier** dans cet état.
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
| `LICENSE` (EUPL-1.2) | | | |
| `mistral` : URL, modèle, fenêtre | | | |
| `scaleway` : URL, modèle, fenêtre | | | |
| `ovhcloud` : URL, modèle, fenêtre | | | |
| `ollama` : URL, modèle, fenêtre | | | |
| `vllm` : URL, modèle, fenêtre | | | |
| Disponibilité du nom `tenon` | | | |
