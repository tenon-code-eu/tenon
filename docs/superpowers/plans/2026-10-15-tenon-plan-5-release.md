# tenon — Plan 5 : marquage git, documentation, CI et release

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Terminer la v1 : trailers git sur le code produit par l'agent, documentation utilisateur, intégration continue sur une forge européenne, et chaîne de release reproductible et signée ; vérifier les points marqués `TODO(release)`.

**Architecture:** `internal/gitmark` lit les sessions du projet (fichiers de session déjà masqués, qui contiennent les chemins et empreintes des écritures) et ajoute des trailers via `git interpret-trailers`. Un `Makefile` porte toutes les étapes de CI et de release, appelées à l'identique par Forgejo Actions et Woodpecker. Les outils de build (staticcheck, govulncheck, cyclonedx-gomod, nfpm) sont lancés par `go run <module>@<version>` épinglé, jamais vendorés dans le binaire.

**Tech Stack:** Go 1.26, git ≥ 2.32, make, POSIX sh ; outils épinglés : `honnef.co/go/tools/cmd/staticcheck`, `golang.org/x/vuln/cmd/govulncheck`, `github.com/CycloneDX/cyclonedx-gomod/cmd/cyclonedx-gomod`, `github.com/goreleaser/nfpm/v2/cmd/nfpm` (dernières versions publiées depuis au moins deux semaines) ; `minisign` (binaire externe, optionnel en local).

**Spec:** `docs/superpowers/specs/2026-10-14-tenon-design.md` (§6.5 marquage, §11 build/CI/distribution, §12 risques). Plans 1 à 4 mergés.

## Global Constraints

- Contraintes des plans 1 à 4 en vigueur (vendoring, `GOFLAGS=-mod=vendor` pour le code du projet, Linux/macOS, aucun secret en clair, textes utilisateur via `i18n.T`).
- Le hook git ne doit **jamais** bloquer un commit : absence de `tenon`, erreur de lecture, timeout → le message est laissé intact et le hook sort avec 0.
- Les trailers ne contiennent aucun secret ni contenu de fichier : nom de l'outil, modèle, nom du fournisseur, pays, identifiant de session.
- Build reproductible : `CGO_ENABLED=0`, `-trimpath`, `-buildvcs=false`, `-ldflags "-s -w -buildid= -X main.version=<v>"`, `SOURCE_DATE_EPOCH` = date du dernier commit ; deux builds successifs du même commit donnent des binaires identiques octet pour octet.
- Cibles : `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`.
- CI et release n'appellent aucun service propriétaire : forge Codeberg/Forgejo, runners auto-hébergeables. `GOPROXY=direct` et `GOSUMDB=sum.golang.org` restent la seule dépendance réseau aux outils de build (documenté), `govulncheck` interroge `vuln.go.dev` (documenté comme exception connue).

## Review Focus

1. **Commit sans `tenon` installé ou avec un journal illisible** : le commit réussit, message inchangé (Task 1 : `TestHookNeverBlocks`).
2. **Hook déjà présent dans le dépôt** : `install-hook` ne l'écrase pas sans `--force`, et `--force` garde une copie `.bak` (Task 1 : `TestInstallKeepsExistingHook`).
3. **Fichier indexé dont le chemin contient des espaces, des guillemets ou des retours à la ligne** : bien détecté, aucune injection dans le message (Task 1 : `TestOddPaths`).
4. **Deux builds du même commit** : binaires identiques (Task 3 : `make repro-check`).
5. **Exemple de configuration de la documentation** : se charge sans erreur avec la version courante (Task 2 : `TestConfigExampleLoads`).

---

### Task 1: Marquage git du code généré

**Files:**
- Create: `internal/gitmark/gitmark.go`, `internal/gitmark/hook.go`
- Modify: `cmd/tenon/run.go`, `internal/i18n/catalog.go`
- Test: `internal/gitmark/gitmark_test.go`, `cmd/tenon/gitmark_e2e_test.go`

**Interfaces:**
- Produces :
  ```go
  type Write struct { Path, HashAfter, SessionID, Model, Provider, Country string; TS time.Time }
  func AgentWrites(sessionsDir, projectRoot string) ([]Write, error) // write_file/edit_file réussis (ToolResult non IsError avec Path), toutes sessions du projet
  func Trailers(staged []string, repoRoot string, writes []Write) []string // lignes "AI-Assisted-By: …" et "AI-Session: …", dédupliquées, ordonnées
  func Mark(msgFile, repoRoot, sessionsDir string) error
  func InstallHook(repoRoot string, force bool) (installedPath string, err error)
  ```
- CLI :
  - `tenon git install-hook [--force]` : installe `prepare-commit-msg` dans le dossier de hooks effectif (`git rev-parse --git-path hooks`, respecte `core.hooksPath`), mode `0755`. Script exact :
    ```sh
    #!/bin/sh
    # tenon : marquage du code produit par l'agent (ne bloque jamais le commit)
    case "$2" in merge) exit 0 ;; esac
    command -v tenon >/dev/null 2>&1 || exit 0
    tenon git mark "$1" >/dev/null 2>&1 || true
    exit 0
    ```
    Hook existant différent → erreur `git.hook_exists` (code 1) sauf `--force`, qui renomme l'existant en `prepare-commit-msg.bak` (écrase un `.bak` précédent). Hook identique → succès sans modification.
  - `tenon git mark <fichier-message>` : lit les fichiers indexés (`git diff --cached --name-only -z`), les écritures de l'agent, et ajoute les trailers avec `git interpret-trailers --in-place --if-exists addIfDifferent --trailer …`. Délai global 5 s. Toute erreur → code 0 sans modification en mode hook (l'erreur est écrite seulement si `TENON_DEBUG=1`).
- Correspondance : un fichier indexé correspond si son chemin absolu (racine du dépôt + chemin indexé, liens résolus) est égal au `Path` d'une écriture de l'agent (liens résolus). Trailers :
  ```
  AI-Assisted-By: tenon (<modèle>, <nom fournisseur>, <pays>)
  AI-Session: <session-id>
  ```
  Une ligne `AI-Assisted-By` par triplet distinct, une ligne `AI-Session` par session distincte, triées. Champs nettoyés : retours à la ligne, `:` en début de valeur et caractères de contrôle retirés ; pays vide → `—`.
- Clés i18n : `git.hook_installed` (FR « hook installé : %s », EN « hook installed: %s »), `git.hook_exists` (FR « un hook prepare-commit-msg existe déjà (%s) : relancez avec --force pour le remplacer », EN « a prepare-commit-msg hook already exists (%s): rerun with --force to replace it »).

- [ ] **Step 1: Écrire les tests qui échouent** (dépôts git réels dans `t.TempDir()`, sessions écrites avec `internal/session`)

```go
func TestAgentWritesFromSessions(t *testing.T) // ToolResult write_file/edit_file réussis seulement ; IsError ignoré ; autre projet ignoré
func TestTrailersMatchStaged(t *testing.T)     // 2 fichiers indexés dont 1 écrit par l'agent → 1 AI-Assisted-By + 1 AI-Session
func TestTrailersMultipleSessions(t *testing.T) // dédupliqués et triés
func TestNoMatchNoChange(t *testing.T)         // message inchangé octet pour octet
func TestOddPaths(t *testing.T)                // chemins avec espace, guillemet, retour à la ligne : détectés ; aucune ligne de trailer injectée
func TestInstallHook(t *testing.T)             // contenu exact, 0755, core.hooksPath respecté
func TestInstallKeepsExistingHook(t *testing.T) // hook différent → erreur ; --force → .bak + nouveau ; identique → succès
func TestHookNeverBlocks(t *testing.T)         // `git commit` avec le hook : PATH sans tenon → commit OK, message inchangé ; tenon factice qui échoue → commit OK
```
Plus, dans `cmd/tenon/gitmark_e2e_test.go` : `TestCommitGetsTrailers` — un tour headless (faux fournisseur) écrit `a.go`, `git add a.go`, `tenon git mark` sur un message → le message contient `AI-Assisted-By: tenon (` et `AI-Session: s_`.

- [ ] **Step 2: Vérifier l'échec** — Run: `GOFLAGS=-mod=vendor go test ./internal/gitmark/ ./cmd/tenon/` — Expected: FAIL.
- [ ] **Step 3: Implémenter.**
- [ ] **Step 4: Vérifier le succès** — Run: `GOFLAGS=-mod=vendor go test -race ./...` — Expected: PASS.
- [ ] **Step 5: Commit** — `git commit -m "feat(gitmark): trailers git sur le code produit par l'agent"`

---

### Task 2: Documentation utilisateur

**Files:**
- Create: `docs/utilisation.md`, `docs/securite.md`, `docs/config.example.toml`, `RELEASE.md`
- Modify: `README.md`
- Test: `internal/config/example_test.go`

**Contenu exigé :**
- `README.md` (FR, puis un court résumé EN) : ce qu'est tenon, installation (binaires, `.deb`/`.rpm`, `go build`), démarrage rapide (`tenon key set mistral`, `tenon`, `tenon -p`), liens vers les docs, licence.
- `docs/utilisation.md` : TUI (touches, validation, délai de 500 ms, panneau sans ambiguïté et marqueurs `␠`, `↪`, `[U+XXXX]`), headless (`-p`, `--output json`, codes 0/1/3), sessions (`tenon sessions`, `--resume`), langue, configuration globale et projet (ce que le projet peut et ne peut pas faire), fournisseurs et juridictions, trousseau, MCP (déclaration, stdio lancé depuis `HOME`, `TENON_PROJECT_ROOT`, `--allow-non-eu` qui vaut pour fournisseurs et MCP), marquage git, `tenon audit verify`, `tenon config check`.
- `docs/securite.md` : critères de souveraineté (spec §1), ce que garantissent l'audit, les permissions et le masquage, et **toutes les limites connues** : permissions ≠ bac à sable, juridiction déclarative, masquage approximatif (y compris les secrets coupés entre deltas), troncature de fin du journal non détectable (ancrage manuel du dernier hash), sosies visuels et bidi du terminal, `jurisdiction = local` d'un serveur stdio, `govulncheck`/`proxy.golang.org`, procédure de récupération d'un journal d'audit ou d'une session corrompus.
- `docs/config.example.toml` : exemple complet et commenté (fournisseur personnalisé, règles de permission, masquage personnalisé, un serveur MCP stdio et un HTTP), sans aucun secret.
- `RELEASE.md` : liste de contrôle avant publication (vérifier `LICENSE`, identifiants de modèles et fenêtres de contexte, nom `tenon` disponible, `make release`, signature, publication sur la forge).

- [ ] **Step 1: Écrire le test qui échoue**

```go
func TestConfigExampleLoads(t *testing.T) // config.Load("../../docs/config.example.toml", "", getenv vide) → nil ; contient au moins 1 serveur MCP et 1 règle deny
```

- [ ] **Step 2: Vérifier l'échec** — Run: `GOFLAGS=-mod=vendor go test ./internal/config/` — Expected: FAIL (fichier absent).
- [ ] **Step 3: Écrire la documentation.**
- [ ] **Step 4: Vérifier le succès** — même commande — Expected: PASS ; relire les liens internes du README.
- [ ] **Step 5: Commit** — `git commit -m "docs: guide d'utilisation, sécurité, exemple de configuration"`

---

### Task 3: Build reproductible et CI

**Files:**
- Create: `Makefile`, `.forgejo/workflows/ci.yml`, `.woodpecker.yml`, `scripts/repro-check.sh`
- Modify: `cmd/tenon/main.go` (si besoin pour `version`)

**Cibles `make` :**
- `vet` : `go vet ./...` ; `test` : `go test -race ./...` ; `lint` : `go run honnef.co/go/tools/cmd/staticcheck@<v> ./...` ; `vuln` : `go run golang.org/x/vuln/cmd/govulncheck@<v> ./...` ; `ci` : `vet test lint vuln`.
- `build` : binaire local `dist/tenon` avec les options reproductibles des Global Constraints ; `VERSION` = `git describe --tags --always --dirty`.
- `repro-check` : `scripts/repro-check.sh` construit deux fois pour chaque cible dans deux dossiers temporaires distincts (caches Go séparés) et compare les SHA-256 ; sortie non nulle à la moindre différence.
- Toutes les commandes Go du projet utilisent `GOFLAGS=-mod=vendor` ; les `go run …@v` utilisent `GOFLAGS=` (mode module) et `GOPROXY=direct`.
- `.forgejo/workflows/ci.yml` et `.woodpecker.yml` : sur push et pull request, image `golang:1.26` (documenter l'alternative d'un miroir de registre européen), lancent `make ci` puis `make repro-check`.

- [ ] **Step 1: Vérifier l'échec** — Run: `make repro-check` — Expected: échec (cible absente).
- [ ] **Step 2: Implémenter** le Makefile, le script et les deux fichiers de CI.
- [ ] **Step 3: Vérifier** — Run: `make vet test build repro-check` — Expected: succès, binaires identiques. `make lint vuln` : succès si le réseau le permet, sinon noter l'échec réseau dans le rapport (pas un échec de tâche). `./dist/tenon --version` affiche la version.
- [ ] **Step 4: Vérifier la syntaxe des fichiers de CI** — Run: `python3 -c "import yaml,sys; [yaml.safe_load(open(f)) for f in sys.argv[1:]]" .forgejo/workflows/ci.yml .woodpecker.yml` — Expected: aucune erreur.
- [ ] **Step 5: Commit** — `git commit -m "build: Makefile, build reproductible, CI Forgejo et Woodpecker"`

---

### Task 4: Chaîne de release

**Files:**
- Create: `scripts/release.sh`, `packaging/nfpm.yaml`
- Modify: `Makefile`

**Comportement de `make release VERSION=vX.Y.Z` :**
1. Refuse si l'arbre n'est pas propre ou si `VERSION` n'est pas un tag existant pointant sur `HEAD`.
2. Construit les 4 cibles (options reproductibles) dans `dist/tenon_<version>_<os>_<arch>/tenon` et des archives `.tar.gz` déterministes (`tar --sort=name --mtime=@$SOURCE_DATE_EPOCH --owner=0 --group=0 --numeric-owner`, `gzip -n`) incluant `LICENSE`, `README.md`, `docs/`.
3. Paquets `.deb` et `.rpm` pour `linux/amd64` et `linux/arm64` via `nfpm` (`packaging/nfpm.yaml` : binaire dans `/usr/bin/tenon`, licence `EUPL-1.2`, docs dans `/usr/share/doc/tenon/`).
4. SBOM CycloneDX (`cyclonedx-gomod app -json -licenses`) : `dist/tenon_<version>.cdx.json`.
5. `dist/SHA256SUMS` sur tous les artefacts.
6. Signature : si `MINISIGN_KEY` est défini et `minisign` présent → `minisign -S -s "$MINISIGN_KEY" -m dist/SHA256SUMS` ; sinon message explicite « signature ignorée » et code 0 (la publication réelle exige la signature : vérifiée dans `RELEASE.md`).

- [ ] **Step 1: Vérifier l'échec** — Run: `make release VERSION=v0.0.0-test` — Expected: échec (cible absente).
- [ ] **Step 2: Implémenter.**
- [ ] **Step 3: Vérifier sur un dépôt jetable** — cloner le dépôt dans un dossier temporaire, y créer le tag `v0.0.0-test`, lancer `make release VERSION=v0.0.0-test` → les 4 archives, 4 paquets, le SBOM et `SHA256SUMS` existent ; `sha256sum -c` (ou `shasum -a 256 -c`) passe ; relancer → `SHA256SUMS` identique (archives reproductibles). Si `nfpm` ou `cyclonedx-gomod` ne peuvent pas être téléchargés (réseau), le noter dans le rapport.
- [ ] **Step 4: Commit** — `git commit -m "build: chaîne de release (archives, deb/rpm, SBOM, sommes, signature)"`

---

### Task 5: Vérifications avant publication

**Files:**
- Modify: `LICENSE` (si différent du texte officiel), `internal/config/presets.go`, `RELEASE.md`
- Test: `internal/config/config_test.go` (valeurs mises à jour)

**Travail :**
1. Comparer `LICENSE` au texte officiel anglais de l'EUPL-1.2 publié par la Commission européenne (site interoperable-europe / joinup, ou Journal officiel). Remplacer par le texte officiel exact s'il diffère (différences de mise en forme pure : normaliser vers le texte officiel). Consigner la source et la date dans `RELEASE.md`.
2. Pour chaque préréglage (Mistral, Scaleway, OVHcloud, Ollama, vLLM) : vérifier dans la documentation officielle du fournisseur l'URL de base compatible OpenAI, un identifiant de modèle de code actuel et sa fenêtre de contexte. Mettre à jour `presets.go`, retirer le commentaire `TODO(release)` des valeurs vérifiées, consigner les sources (URL + date) dans `RELEASE.md`. Une valeur non vérifiable garde son `TODO(release)` et est listée dans `RELEASE.md`.
3. Ne rien inventer : chaque valeur modifiée doit venir d'une source officielle citée.

- [ ] **Step 1:** Recherche et comparaison (lecture seule).
- [ ] **Step 2:** Mettre à jour les tests de préréglages pour les nouvelles valeurs, vérifier l'échec, puis modifier `presets.go`.
- [ ] **Step 3: Vérifier** — Run: `GOFLAGS=-mod=vendor go test -race ./...` — Expected: PASS.
- [ ] **Step 4: Commit** — `git commit -m "chore(release): texte EUPL officiel, préréglages vérifiés"`
