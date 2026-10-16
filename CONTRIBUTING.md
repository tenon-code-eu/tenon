# Contribuer à tenon

Merci de votre intérêt.
Les contributions sont acceptées en français ou en anglais.

## Avant de commencer

- Pour un bogue : ouvrez une issue avec les étapes pour reproduire et la sortie de `tenon --version`.
- Pour une nouvelle fonction : ouvrez d'abord une issue pour en discuter.
- Pour une vulnérabilité : ne passez pas par une issue, suivez [SECURITY.md](SECURITY.md).

## Environnement

- Go : dernier correctif de Go 1.26 (≥ 1.26.9).
- Les dépendances sont vendorées : compilez avec `GOFLAGS=-mod=vendor`.
- Linux ou macOS.

```
GOFLAGS=-mod=vendor go build -o tenon ./cmd/tenon
```

## Vérifications

Lancez avant chaque demande de fusion :

```
make ci            # vet, tests avec -race, staticcheck, govulncheck
make repro-check   # deux builds identiques octet pour octet
```

La CI lance les mêmes commandes.

## Règles du projet

- Toute nouvelle fonction ou correction vient avec ses tests.
- Une dépendance nouvelle doit être justifiée dans la demande de fusion, puis vendorée (`go mod vendor`).
- Ne relâchez aucune garantie décrite dans [docs/securite.md](docs/securite.md) : validation des actions, masquage des secrets, journal d'audit, refus des envois hors de l'UE sans accord.
- Les textes affichés passent par le catalogue i18n (français et anglais).
- Aucun secret réel dans le code ni dans les tests : construisez les exemples de jetons par concaténation (`"ghp_" + strings.Repeat("a", 36)`).

## Commits

- Un commit par changement cohérent, message au format `type(portée): résumé` (par exemple `fix(tui): …`).
- Le code écrit avec un agent est signalé par le trailer `AI-Assisted-By` (voir `tenon git install-hook`).

## Licence

tenon est distribué sous licence [EUPL-1.2](LICENSE).
En proposant une contribution, vous acceptez qu'elle soit distribuée sous cette licence.
