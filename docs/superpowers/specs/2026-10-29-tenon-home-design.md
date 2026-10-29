# tenon : écran d'accueil, vue de session et identité visuelle (TUI et VS Code)

Date : 2026-10-29. Statut : conception validée en séance, à relire.
Specs liées : [tenon pour VS Code](2026-10-28-tenon-vscode-design.md), [tenon acp](2026-10-26-tenon-acp-design.md).

## 1. Objectif

Donner à tenon une identité visuelle forte et commune au terminal et à VS Code, inspirée d'opencode : un écran d'accueil centré (logo, slogan, saisie, raccourcis, liens), une vue de session avec un panneau d'informations, et quelques idées propres à tenon qui rendent la souveraineté visible à chaque tour.

Critères de succès :

- au lancement de `tenon`, l'accueil centré s'affiche, et disparaît au premier message ;
- pendant la session, un panneau repliable montre contexte, jetons, fichiers modifiés et souveraineté ;
- chaque réponse de l'agent porte le badge du fournisseur qui l'a produite ;
- le panneau VS Code offre le même accueil, une vue « Session » et les mêmes badges ;
- thèmes, palette de commandes et animation du logo, sans nouvelle dépendance ;
- `NO_COLOR`, les petits terminaux et la réduction des animations sont respectés.

## 2. Décisions

| Sujet | Décision | Raison |
|---|---|---|
| Accueil | Centré façon opencode : logo, slogan + version, saisie, raccourcis, astuce, liens ; barre d'état en bas | Choix en séance |
| Infos de session (TUI) | Panneau latéral droit repliable, 26 colonnes, masqué sous 100 colonnes, `Ctrl+B` | Comme opencode, sans gêner les petits terminaux |
| Infos de session (VS Code) | Seconde vue « Session » empilée sous le chat | Le panneau latéral de VS Code est trop étroit pour une colonne |
| Badge par réponse | `<fournisseur> · <pays> · <UE|hors UE|local>` sur chaque réponse | Rendre la souveraineté visible tour par tour |
| Thèmes TUI | `tenon` (défaut), `tokyonight`, `catppuccin`, `gruvbox`, `light` | Choix en séance |
| Thème VS Code | `tenon.theme` : `auto` (défaut, thème de l'éditeur) ou `tenon` | VS Code a déjà ses thèmes |
| Palette | TUI : `Ctrl+P`, recherche floue maison ; VS Code : QuickPick natif | Pas de dépendance |
| Changement de fournisseur | Hors périmètre ; la palette montre le fournisseur actif et rappelle `tenon --provider <id>` | Le moteur ne change pas de fournisseur en cours de session |
| Animation | Emboîtement tenon-mortaise < 700 ms ; sautée sans TTY, avec `NO_COLOR`, avec `[ui] animation = false` ou `prefers-reduced-motion` | Effet léger, jamais bloquant |
| Dépendances | Aucune nouvelle (bubbletea, lipgloss, bubbles déjà vendorisés) | Règle du projet |

## 3. Langage visuel commun

- Logo : bouclier tenon et mot « T E N O N » espacé ; slogan « agent de code souverain » (EN « sovereign coding agent ») suivi de la version.
- Thème `tenon` : bleu Europe `#003399` (structure, cadres), or `#FFCC00` (accent, curseur, sélection), vert (UE, local, isolé), ambre (hors UE, juridiction inconnue, non isolé), gris atténué pour le texte secondaire.
- Pastilles communes : juridiction, isolation, profil, mêmes libellés que la barre d'état VS Code (`EU|non-EU|local`, `sandboxed|not sandboxed`, FR `UE|hors UE|local`, `isolé|non isolé`).
- Liens (constantes) : docs `https://tenon-code-eu.github.io/tenon/`, github `https://github.com/tenon-code-eu/tenon`, signaler un bug `https://github.com/tenon-code-eu/tenon/issues/new`.

## 4. TUI

### Accueil

- Écran vide, contenu centré verticalement et horizontalement : logo (6 lignes) ; slogan + version ; zone de saisie encadrée (largeur `min(72, largeur − 8)`) ; ligne de raccourcis `/help /skills /rewind /memory · Ctrl+P palette · Tab agents` ; astuce du jour (tirée au hasard dans une liste i18n d'au moins 8 astuces) ; liens `docs · github · signaler un bug` en hyperliens OSC 8 quand le terminal les gère, texte sinon.
- Barre d'état en bas : dossier du projet (`~` abrégé, coupé au milieu), modèle, fournisseur, pastilles juridiction et isolation, profil s'il est actif, jetons de la session.
- Moins de 70 colonnes : logo compact `⬢ TENON` sur une ligne.
- Remplace le cadre actuel (`welcome.go`) ; les infos qu'il portait (projet, profil, politique, nombre de skills/agents/hooks) passent dans la barre d'état et le panneau.
- Disparaît au premier message envoyé ; une session reprise (`--resume`) affiche directement le fil.

### Vue de session

- Fil à gauche ; panneau droit de 26 colonnes, séparé par un trait, ouvert d'office à partir de 100 colonnes, fermé en dessous ; `Ctrl+B` bascule (le choix manuel prime jusqu'à la fin de la session).
- Contenu du panneau, dans l'ordre : titre de la session (premier message masqué, coupé) ; contexte : jauge sur 10 cellules et pourcentage (jetons du dernier appel / fenêtre du fournisseur), jetons entrants et sortants de la session ; fichiers modifiés (`M`, `A`, `D`, chemin relatif, `+x −y` quand connu), au plus 8 puis « +N » ; souveraineté : envois par fournisseur et juridiction (`● 4 → Mistral · FR`), envois hors UE en ambre, état de l'isolation, profil et politique.
- Badge par réponse : sous chaque réponse terminée de l'agent principal, en texte atténué, celui du fournisseur du dernier appel du tour ; hors UE et inconnu en ambre.

### Palette `Ctrl+P`

- Fenêtre centrée : champ de recherche, liste filtrée par correspondance de sous-séquence insensible à la casse (meilleur score : correspondances contiguës et en début de mot), flèches et Entrée, Échap ferme.
- Entrées : commandes intégrées (`skills.Builtins`), skills du projet et utilisateur, sessions passées du dossier (reprise : comme `/resume` ou relance avec `--resume`, selon ce que la TUI permet déjà ; à défaut la palette affiche la commande à lancer), thèmes, et une entrée informative « Fournisseur : <actif> — changer avec `tenon --provider <id>` ».

### Thèmes

- `internal/ui/tui/theme.go` : une palette de rôles (structure, accent, ok, alerte, atténué, texte, fond de sélection) par thème. `[ui] theme` (`tenon` par défaut) et `[ui] animation` (booléen, `true` par défaut) dans la config globale ; valeur inconnue : erreur `config: unknown ui.theme "<v>"`. Refusés dans la config projet (`config: ui not allowed in project configuration`). `/theme <nom>` change le thème pour la session ; `/theme` seul liste les thèmes. `NO_COLOR` désactive toute couleur quel que soit le thème.

### Animation

- `intro.go` : 6 à 8 images à environ 80 ms, les deux pièces du logo qui glissent l'une vers l'autre, puis l'accueil. Sautée si la sortie n'est pas un TTY, si `NO_COLOR` est défini, si `[ui] animation = false`, ou si le terminal fait moins de 70 colonnes ; toute touche l'interrompt et la touche est conservée pour la saisie.

## 5. Extension VS Code

- **Accueil** (`webview/home.ts`) : tant que la session n'a aucun message, même contenu que la TUI, logo en SVG inclus dans le bundle, zone de saisie centrée ; liens envoyés à l'extension par un message `openLink` portant un identifiant (`docs` | `github` | `bug`), jamais une URL ; l'extension ouvre la constante correspondante avec `vscode.env.openExternal`. Disparaît au premier message ; une session reprise affiche le fil.
- **Vue Session** (`src/sessionView.ts`) : second `WebviewViewProvider` (`tenon.session`) dans le conteneur tenon, sous `tenon.chat`, alimenté par le même `Conversation` ; même contenu que le panneau TUI ; un fichier modifié est cliquable et ouvre son diff natif (même chemin que « Voir le diff »).
- **Badge par réponse** : `Item` de type `agent` gagne `provider?: { name: string; country?: string; jurisdiction?: string }`, rempli par le dernier `usage_update` du tour (`_meta.tenon.providerName`, `jurisdiction`) ; hors UE ou inconnu en couleur d'alerte.
- **Palette** : commande `tenon.palette` (« tenon: Palette »), Cmd+Shift+; / Ctrl+Shift+; quand le chat a le focus ; QuickPick : commandes tenon, skills, sessions passées (reprise par `session/load`), thème (`auto` / `tenon`), entrée informative sur le fournisseur.
- **Thème** : réglage `tenon.theme` (`auto` | `tenon`) ; `tenon` applique les couleurs du §3 via des variables CSS, `auto` reprend les variables `--vscode-*`.
- **Animation** : emboîtement CSS < 700 ms à l'affichage de l'accueil, désactivé sous `prefers-reduced-motion: reduce`.
- CSP, validation stricte des messages et absence d'appel réseau inchangées ; les nouveaux messages webview (`openLink`, `palette`, `openFileDiff`) sont validés par `isFromWebview`.

## 6. Données

Aucun changement du moteur ni du protocole : `Usage` (fournisseur, juridiction, jetons, fenêtre), `ToolResult` (fichiers, hachages), `UserMessage` (titre), `TurnCompleted` (fin de tour) côté TUI ; `usage_update`, `tool_call_update` (diffs, chemins) et `_meta.tenon` côté extension.

## 7. Langues

Tous les textes en EN et FR : `internal/i18n` (TUI, astuces comprises), `vscode.l10n` et `package.nls*.json` (extension) ; le test de complétude FR de l'extension couvre les nouvelles chaînes.

## 8. Tests

- TUI : instantanés de l'accueil à 60, 80 et 120 colonnes, avec et sans `NO_COLOR` ; panneau absent sous 100 colonnes, présent au-dessus, `Ctrl+B` ; agrégats du panneau (envois par juridiction, fichiers, jauge) ; badge hors UE en ambre ; recherche de la palette (ordre des résultats) ; thème inconnu et `[ui]` en projet refusés ; animation sautée sans TTY et interrompue par une touche ; liens OSC 8 présents seulement quand le terminal les annonce.
- Extension : unitaires — badge renseigné par `usage_update`, agrégats de la vue Session, rendu de l'accueil sans `innerHTML`, `openLink` limité aux trois identifiants ; intégration — accueil puis premier message, vue Session remplie après un tour, clic sur un fichier modifié ouvre le diff.

## 9. Hors périmètre

Changement de fournisseur en cours de session ; thèmes personnalisés par fichier ; images dans le terminal ; page d'accueil du site (sous-projet suivant).
