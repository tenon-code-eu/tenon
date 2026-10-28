# tenon pour VS Code : extension à la marque tenon

Date : 2026-10-28. Statut : conception validée en séance, à relire.
Spec parente : [tenon acp](2026-10-26-tenon-acp-design.md) (agent ACP, plan 22). Specs liées : [isolation système](2026-10-22-tenon-sandbox-design.md), [masquage](2026-10-14-tenon-design.md).

## 1. Objectif

Faire adopter tenon par les développeurs VS Code : une extension publiée sur le VS Code Marketplace et sur Open VSX (VSCodium, Cursor, Windsurf), qui s'installe en un clic et donne un panneau de chat tenon dans l'éditeur. L'extension est un client ACP léger : tout le travail (outils, permissions, isolation, masquage, routage, audit) reste dans `tenon acp`.

Critères de succès :

- installer l'extension suffit : le binaire tenon de la plateforme est inclus ;
- dans le panneau : conversation en flux, demandes d'accord avec les options de tenon, annulation, diffs dans l'éditeur de diff natif, fichier ou sélection joints, `/skills` en autocomplétion, sessions passées reprises ;
- la barre d'état montre en permanence le fournisseur, sa juridiction et l'isolation ;
- une commande ouvre la TUI de tenon dans un terminal à côté du code, façon opencode ;
- l'extension ne fait aucun appel réseau et n'envoie aucune télémétrie ; seul tenon parle aux fournisseurs.

## 2. Décisions

| Sujet | Décision | Raison |
|---|---|---|
| Interface | Panneau webview tenon dans la barre latérale ; plus une commande « Ouvrir tenon dans le terminal » | Fonctionne partout (VSCodium inclus), sans dépendre de Copilot Chat |
| Style | Esthétique opencode : sombre, sobre, monospace, couleurs du thème VS Code | Demande explicite ; cohérent avec la TUI |
| Binaire | Inclus : un `.vsix` par plateforme (`darwin-arm64`, `darwin-x64`, `linux-x64`, `linux-arm64`) | Installation en un clic, aucun téléchargement au lancement |
| Emplacement | `editors/vscode/` dans le dépôt tenon | Versions alignées ; tests d'intégration contre le vrai `tenon acp` |
| Client ACP | `@agentclientprotocol/sdk` (Apache-2.0) | Schéma officiel ; pas de protocole maison |
| UI | TypeScript sans framework, CSS, bundle esbuild | UI de chat simple ; extension légère |
| Publication | Marketplace + Open VSX, lancée par le mainteneur avec ses jetons | Même modèle que les releases signées |
| Windows | Non | tenon ne le prend pas en charge |

## 3. Binaire et démarrage

Résolution du binaire, dans l'ordre :

1. réglage `tenon.path` (chemin absolu) ;
2. binaire inclus (`<extension>/bin/tenon`) ;
3. `tenon` dans le `PATH`.

La version est lue avec `tenon --version` ; en dessous de la version minimale (celle de l'extension, au moins 0.9.0), notification « tenon <v> est trop ancien » avec « Choisir le binaire ». Binaire introuvable : notification avec « Installer » (lien vers la doc) et « Choisir le binaire ».

Une connexion ACP par fenêtre VS Code : `tenon acp` en sous-processus ; stdin/stdout pour ACP ; stderr vers un canal Output « tenon ». `initialize` avec `clientInfo: { name: "tenon-vscode", version }`, `clientCapabilities` sans `fs` ni `terminal`. Une session par dossier racine de l'espace de travail (`session/new { cwd, mcpServers: [] }`) ; espace multi-racine : sélecteur de dossier dans le panneau.

Arrêt du processus : bandeau « tenon s'est arrêté » avec « Relancer » et « Voir les journaux ». Refus au démarrage (erreur JSON-RPC `-32603`) : message de tenon affiché tel quel dans le panneau.

## 4. Panneau de chat

- Fil : messages utilisateur, texte de l'agent en flux (`agent_message_chunk`), réflexions et notices de tenon (`agent_thought_chunk`, repliables), appels d'outils (`tool_call` / `tool_call_update` : titre, état, sortie repliable).
- Saisie : Entrée envoie, Maj+Entrée saut de ligne ; `/` ouvre l'autocomplétion des skills (`available_commands_update`) ; bouton Stop → `session/cancel`.
- Contexte : commandes « Joindre la sélection » et « Joindre le fichier » (menu contextuel, Cmd+Option+K dans le panneau) : sélection → bloc `resource` (texte intégré, `uri` avec `#L<a>-<b>`), fichier → `resource_link`. tenon masque et classe ce contenu comme un texte saisi.
- Sessions passées : liste depuis `session/list` (titre, date) ; un clic → `session/load`, le fil est reconstruit à partir des mises à jour rejouées.
- Rendu : Markdown sans HTML brut (texte échappé), blocs de code avec bouton Copier, liens `https` seulement, ouverts par VS Code.

## 5. Demandes d'accord

`session/request_permission` → carte dans le fil : titre de tenon, commande ou chemins, boutons dans l'ordre des options reçues (Autoriser, Toujours pour cette session, Refuser, Autoriser avec réseau). Panneau caché : notification VS Code avec les mêmes boutons. Le premier choix répond `{ outcome: "selected", optionId }` ; carte et notification se ferment. Fermeture de la notification sans choix, annulation ou arrêt du processus : `{ outcome: "cancelled" }` (refus chez tenon). Aucune réponse automatique, aucun réglage « toujours autoriser » côté extension.

## 6. Diffs

Un `tool_call_update` contenant `{ type: "diff", path, oldText, newText }` ajoute « Voir le diff » sous l'appel ; le clic ouvre `vscode.diff` entre deux documents virtuels en lecture seule (schéma `tenon-diff:`), ancien texte (vide pour un nouveau fichier) contre nouveau, titre `<fichier> (tenon)`. Les textes sont ceux envoyés par tenon, donc masqués. Pas de bouton « Appliquer » : les écritures passent par l'accord de tenon.

## 7. Barre d'état

`tenon · <fournisseur> · <UE|hors UE|local> · <isolé|non isolé>` ; clic → panneau. Source : `_meta.tenon` des réponses `session/new` / `session/load`, mis à jour par `_meta.tenon.provider` des `usage_update`. Hors UE en couleur d'avertissement du thème. « isolé » seulement si `sandbox.confined` vaut `true` ; sans ce champ (tenon plus ancien), « non isolé » quand `mechanism` est vide ou `none`, ou `mode` vaut `off`. Aucune session (ou tenon arrêté) : `tenon` seul.

## 8. Terminal façon opencode

Commande « tenon : Ouvrir dans le terminal » (Cmd+Échap) : terminal intégré à droite de l'éditeur, nommé « tenon », qui lance la TUI (`tenon`) dans le dossier de l'espace de travail ; réutilisé s'il est ouvert. Cmd+Option+K, quand ce terminal a le focus ou est visible, y insère `@<chemin relatif>#L<a>-<b>` pour la sélection courante.

## 9. Changements dans `tenon acp`

- `session/list` (méthode ACP stable) ; `initialize` annonce `agentCapabilities.sessionCapabilities: { list: {} }`. Paramètres `{ cwd?, cursor? }` ; résultat `{ sessions: [{ sessionId, cwd, title, updatedAt }] }`, sessions du `cwd` (racine de projet résolue comme `session/new`), les plus récentes d'abord ; `title` = premier message utilisateur masqué, coupé à 80 caractères (même règle que `tenon sessions`) ; `updatedAt` ISO 8601. Pas de pagination en v1 (`nextCursor` absent). `cwd` relatif : `-32602`.
- `_meta.tenon` dans les réponses `session/new` et `session/load` : `{ provider, model, jurisdiction, sandbox: { mode, mechanism, network, confined }, profile, policy }` (valeurs de `SessionStarted` : `Provider`, `Sandbox`, `Profile`, `Policy.ID`) ; `mechanism` vaut `none` (jamais vide) quand l'isolation est coupée ou indisponible ; `confined` (booléen) vaut `true` si et seulement si les commandes shell sont confinées (mode différent de `off` et sonde disponible) ; la barre affiche « isolé » quand `sandbox.confined` vaut `true` (ruling R8) ; `usage_update` gagne `_meta.tenon.provider` quand le fournisseur change (repli).
- Docs EN/FR `editors.md` / `editeurs.md` : section VS Code mise à jour (extension tenon d'abord, client tiers ensuite).

## 10. Langue, sécurité, confidentialité

- Interface EN et FR selon la langue de VS Code (`vscode.l10n`, `l10n/bundle.l10n.fr.json`, `package.nls.fr.json`).
- Webview : CSP stricte (`default-src 'none'`, scripts et styles locaux avec nonce), aucune ressource distante, messages extension ↔ webview typés et validés.
- Aucune télémétrie, aucun appel réseau depuis l'extension. Les clés restent dans la config et le trousseau de tenon.

## 11. Empaquetage et publication

- `scripts/vscode-package.sh <version>` : prend les binaires de `dist/` (produits par `scripts/release.sh`), produit `tenon-vscode-<plateforme>-<version>.vsix` pour les quatre plateformes (`vsce package --target`), binaire dans `bin/`, exécutable.
- Les `.vsix` rejoignent les fichiers de la release et `SHA256SUMS`, signé avec minisign comme le reste.
- Publication par le mainteneur : `vsce publish --packagePath` (Marketplace, éditeur `tenon-code-eu`) et `ovsx publish` (Open VSX) ; commandes décrites dans `RELEASE.md`. Création des comptes et jetons : le mainteneur.
- Licence de l'extension : EUPL-1.2 ; icône : la marque tenon.

## 12. Architecture

`editors/vscode/` :

- `src/extension.ts` (activation, commandes), `src/binary.ts` (résolution et version), `src/agent.ts` (sous-processus, connexion ACP), `src/session.ts` (sessions, prompts, modèle de conversation), `src/permissions.ts`, `src/diff.ts` (fournisseur `tenon-diff:`), `src/context.ts` (blocs `resource` / `resource_link`), `src/status.ts`, `src/terminal.ts` ;
- `webview/` : `main.ts`, `render.ts` (Markdown sûr), `style.css` ;
- `l10n/`, `package.json`, `package.nls*.json`, `media/` (icône) ;
- `test/unit/` (vitest), `test/integration/` (`@vscode/test-electron`).

Dépôt : `internal/acp` (`session/list`, `_meta.tenon`) ; `cmd/tenon/acp.go` (liste des sessions, métadonnées) ; `scripts/vscode-package.sh` ; job `vscode` dans `.github/workflows/ci.yml` (et équivalents `.forgejo`, `.woodpecker.yml` s'ils lancent les tests) ; `RELEASE.md`.

## 13. Tests

- Go : `session/list` (sessions du dossier seulement, ordre, titre masqué et coupé, `cwd` relatif `-32602`, capacité annoncée) ; `_meta.tenon` dans `session/new` / `session/load` ; `usage_update` après repli.
- Unitaires (vitest) : modèle de conversation à partir de suites de `session/update` ; construction des blocs du prompt (sélection, fichier, `/skill`) ; carte d'accord → `optionId`, fermeture → `cancelled` ; rendu Markdown (HTML échappé, liens non `https` neutralisés) ; résolution du binaire et comparaison de version.
- Intégration (`@vscode/test-electron`, Linux xvfb en CI) avec le vrai `tenon acp` construit depuis le dépôt et un faux fournisseur : prompt → réponse affichée ; écriture → accord → diff ouvert ; Stop pendant un accord → refus ; `session/list` puis reprise ; binaire absent → notification ; arrêt du processus → bandeau « Relancer ».
- CI : lint (eslint, tsc), unitaires, intégration.

## 14. Hors périmètre

Windows ; JetBrains (guide existant) ; complétion inline ; modes ACP ; images et audio ; bouton « Appliquer » dans le diff ; `fs/*` et `terminal/*` côté client ; serveurs MCP déclarés par l'extension ; pagination de `session/list`.
