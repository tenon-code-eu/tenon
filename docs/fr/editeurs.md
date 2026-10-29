# tenon dans votre éditeur

`tenon acp` fait de tenon un agent [Agent Client Protocol](https://agentclientprotocol.com) (ACP). L'éditeur lance `tenon acp` en sous-processus et lui parle sur l'entrée et la sortie standard (JSON-RPC 2.0, un message par ligne). Vous discutez avec tenon dans le panneau de l'éditeur ; tenon travaille avec ses propres outils et ses propres règles.

## Sommaire

- [Avant de commencer](#avant-de-commencer)
- [Zed](#zed)
- [IDE JetBrains](#ide-jetbrains)
- [Neovim](#neovim)
- [VS Code](#vs-code)
- [Ce qui reste appliqué](#ce-qui-reste-appliqué)
- [Serveurs MCP de l'éditeur](#serveurs-mcp-de-léditeur)
- [Limites](#limites)

## Avant de commencer

1. Installez tenon et vérifiez que `tenon config check` réussit dans votre projet.
2. Enregistrez la clé du fournisseur avec `tenon key set <fournisseur>` ou dans sa variable d'environnement. L'éditeur ne voit jamais la clé : tenon n'annonce aucune méthode d'authentification (`authMethods: []`).
3. Indiquez le chemin complet du binaire si l'éditeur ne voit pas le `PATH` de votre shell (par exemple `/opt/homebrew/bin/tenon`). `which tenon` l'affiche.

L'éditeur transmet le dossier du projet (`cwd`). tenon démarre alors exactement comme `tenon` lancé dans ce dossier : configuration globale et projet, politique signée, profil, listes blanches, sonde d'isolation, routage, serveurs MCP. Un refus au démarrage (par exemple `[sandbox] mode = "required"` sans isolation disponible) s'affiche dans l'éditeur comme une erreur, avec le message de tenon.

## Zed

Ajoutez tenon sous `agent_servers` dans le `settings.json` de Zed :

```json
{
  "agent_servers": {
    "tenon": {
      "type": "custom",
      "command": "tenon",
      "args": ["acp"],
      "env": {}
    }
  }
}
```

Ouvrez ensuite le panneau Agent et démarrez une conversation avec tenon.

Source : [Zed, External Agents](https://zed.dev/docs/ai/external-agents) (section « Custom Agents »).

## IDE JetBrains

AI Assistant lit les agents personnalisés dans `~/.jetbrains/acp.json`. Dans la fenêtre AI Chat, cliquez sur le bouton « plus » (⋯), puis **Add Custom Agent** : l'IDE crée et ouvre le fichier. Ajoutez tenon sous `agent_servers` :

```json
{
  "agent_servers": {
    "tenon": {
      "command": "/chemin/complet/vers/tenon",
      "args": ["acp"],
      "env": {}
    }
  }
}
```

La clé (`tenon`) est le nom affiché dans AI Chat. Redémarrez l'IDE si l'agent n'apparaît pas.

Source : [JetBrains AI Assistant, Agent Client Protocol](https://www.jetbrains.com/help/ai-assistant/acp.html).

## Neovim

Deux extensions jouent le rôle de client ACP.

**avante.nvim** lit les agents ACP dans `acp_providers` :

```lua
vim.g.avante = {
  acp_providers = {
    ["tenon"] = {
      command = "tenon",
      args = { "acp" },
      env = {},
    },
  },
}
```

Choisissez-le avec `:AvanteSwitchProvider`.

Source : [README d'avante.nvim](https://github.com/yetone/avante.nvim) (section ACP) et l'exemple `acp_providers` de [lua/avante/config.lua](https://github.com/yetone/avante.nvim/blob/main/lua/avante/config.lua).

**CodeCompanion.nvim** définit les adaptateurs ACP sous `adapters.acp`. Un adaptateur personnalisé a `type = "acp"`, la commande dans `commands.default` et `defaults.mcpServers` ; il lui faut aussi le bloc `handlers` du modèle documenté, que nous ne recopions pas ici. Partez de l'exemple d'adaptateur personnalisé de la page ci-dessous, avec :

```lua
-- extrait ; voir le modèle complet dans la documentation de CodeCompanion
name = "tenon",
formatted_name = "tenon",
type = "acp",
commands = {
  default = { "tenon", "acp" },
},
defaults = {
  mcpServers = {},
},
```

Choisissez-le ensuite avec `interactions = { chat = { adapter = "tenon" } }`.

Source : [CodeCompanion, Configuring ACP adapters](https://codecompanion.olimorris.dev/configuration/adapters-acp).

## VS Code

### L'extension tenon

**Installation.** Cherchez **tenon** dans la vue Extensions, ou installez `tenon-code-eu.tenon` depuis le [Marketplace VS Code](https://marketplace.visualstudio.com/items?itemName=tenon-code-eu.tenon). VSCodium, Cursor et les autres éditeurs qui utilisent Open VSX : téléchargez le `.vsix` de votre plateforme depuis la [release GitHub](https://github.com/tenon-code-eu/tenon/releases/latest), vérifiez-le avec le `SHA256SUMS` signé, puis lancez **Extensions : Installer depuis un VSIX…**. Il existe un paquet par plateforme : macOS et Linux, arm64 et x64. Chaque paquet contient le binaire tenon : rien d'autre à installer. Windows n'est pas pris en charge.

**Le binaire.** L'extension cherche tenon dans cet ordre :

1. le réglage `tenon.path` (un chemin absolu, dans vos réglages utilisateur) ;
2. le binaire fourni avec l'extension ;
3. `tenon` dans votre `PATH`.

tenon 0.9.0 ou plus récent est requis. Si tenon est absent ou trop ancien, une notification propose de l'installer ou de choisir le binaire.

**Le panneau.** Cliquez sur l'icône tenon de la barre d'activité, ou lancez **tenon : Ouvrir la discussion**. Ouvrez d'abord un dossier : tenon travaille dans le dossier. Tapez votre demande et appuyez sur Entrée (Maj+Entrée pour un saut de ligne). Arrêter met fin au tour. Tapez `/` pour voir les skills et commandes du projet. La liste Sessions reprend une session précédente du dossier. Le bouton `+` ouvre une nouvelle session.

**Accueil.** Jusqu'au premier message d'une session, la discussion affiche l'accueil de tenon : le logo, `agent de code souverain` et la version de tenon, la zone de saisie au centre, les raccourcis (`/ skills · Maj+Entrée nouvelle ligne · Tab compléter · Cmd+Maj+; palette`, Ctrl+Maj+; sous Linux et Windows), une astuce et trois liens : docs, github, signaler un bug. Les liens s'ouvrent dans votre navigateur ; la page de discussion elle-même ne contient aucune URL et ne fait aucune requête. Une session reprise affiche directement son fil. L'animation du logo est coupée quand le système demande de réduire les animations.

**Badges des réponses.** Sous chaque réponse terminée, un badge indique le fournisseur du dernier appel du tour et sa juridiction : `<fournisseur> · <UE|hors UE|local>`. Un fournisseur hors UE, ou une juridiction inconnue, s'affiche en couleur d'alerte. Après un repli du routage, le badge montre le fournisseur qui a répondu. tenon n'envoie pas de pays à l'éditeur : le badge n'en affiche pas.

**Vue Session.** La vue **Session**, sous la discussion dans la barre latérale tenon, montre la session ouverte comme le panneau latéral de l'interface terminal :

- le titre (celui de la session, sinon son premier message) ;
- la jauge de contexte (jetons d'entrée du dernier appel rapportés à la fenêtre de son fournisseur) et les jetons d'entrée et de sortie de la session ;
- les fichiers modifiés (`A` créé, `M` modifié). Un clic sur un fichier ouvre son dernier diff, comme **Voir le diff** ;
- la souveraineté : le nombre d'appels par fournisseur et juridiction, hors UE et inconnu en couleur d'alerte, puis l'isolation, le profil et la politique.

La vue compte tous les appels de la session, comme le panneau latéral du terminal : agent principal, sous-agents, compaction et hooks. Chaque `usage_update` de `tenon acp` porte les totaux de la session (`_meta.tenon.sends`, `sessionIn`, `sessionOut`) ; avec un tenon plus ancien, la vue ne compte que les appels de l'agent principal, un par `usage_update`. Pour une session reprise, `session/load` rejoue le `usage_update` de chaque appel : les appels et les jetons incluent donc l'historique. Les diffs ne sont pas rejoués : une session reprise n'affiche aucun fichier modifié avant sa prochaine écriture.

**Palette.** **tenon : Palette** (Cmd+Maj+;, Ctrl+Maj+; sous Linux et Windows, quand la discussion a le focus, ou le bouton `⋯` à côté d'Envoyer) ouvre une liste de choix avec :

- les commandes : nouvelle session, joindre la sélection, joindre le fichier, ouvrir dans le terminal, relancer, journaux ;
- les skills de la session : un choix place `/<skill> ` dans la zone de saisie ;
- les sessions passées du dossier : un choix reprend la session ;
- les thèmes, `auto` et `tenon` : un choix règle `tenon.theme`, dans les paramètres de l'espace de travail s'ils le règlent déjà (ils priment sur vos paramètres utilisateur), sinon dans vos paramètres utilisateur ;
- le fournisseur actif. L'extension ne peut pas le changer : réglez `default_provider` dans la configuration de tenon, puis relancez tenon.

Sur un clavier AZERTY, `;` se tape sans Maj : le raccourci peut ne pas fonctionner. Utilisez le bouton `⋯` ou lancez **tenon : Palette** depuis la palette de commandes.

**Thème.** Le réglage `tenon.theme` colore la discussion et la vue Session. `auto` (par défaut) reprend les couleurs de votre thème VS Code. `tenon` applique les couleurs de tenon : bleu Europe `#003399`, accent or `#FFCC00`, vert pour UE et local, ambre pour hors UE. Sur un thème sombre, le texte bleu est plus clair, pour le contraste.

**Contexte.** Sélectionnez du code et appuyez sur Cmd+Option+K (Ctrl+Alt+K sous Linux), ou faites un clic droit puis **Joindre la sélection à la discussion**. La sélection part avec votre prochain message, avec ses lignes. **Joindre le fichier à la discussion**, dans l'éditeur ou sur un fichier de l'explorateur, envoie un lien vers un fichier ; tenon le lit avec ses outils, sous accord.

**Accords.** Quand tenon veut écrire un fichier, lancer une commande ou appeler un outil MCP, une carte apparaît dans la discussion. Cliquez sur l'une des options de tenon : Autoriser, Toujours pour cette session, Refuser, et Autoriser avec réseau pour une commande isolée. Si le panneau est masqué, une notification pose la question. Fermer la notification vaut refus. Arrêter refuse toutes les demandes en attente. L'extension ne répond jamais d'elle-même.

**Diffs.** Un appel d'outil qui modifie un fichier affiche **Voir le diff**. Le diff s'ouvre dans l'éditeur de diff de VS Code, titré `<fichier> (tenon)`, en lecture seule. Le texte est celui de tenon, secrets masqués.

**Barre d'état.** La barre d'état affiche `tenon · <fournisseur> · <UE|hors UE|local> · <isolé|non isolé>`. Un fournisseur hors UE, ou une juridiction inconnue, s'affiche en couleur d'avertissement. Après un repli du routage, la barre montre le fournisseur qui a répondu. « isolé » signifie que tenon confine les commandes shell ; avec `[sandbox] mode = "off"`, ou sans isolation sur la machine, la barre affiche « non isolé ». Quand tenon s'arrête, la barre affiche `tenon` seul. Un clic ouvre la discussion.

**Mode terminal.** Lancez **tenon : Ouvrir dans le terminal** (Cmd+Échap, Ctrl+Échap sous Linux). L'interface terminal de tenon s'ouvre dans un terminal nommé « tenon », à droite de l'éditeur, dans le dossier de l'espace de travail. La même commande le réaffiche tant qu'il est ouvert. Quand ce terminal est visible, Cmd+Option+K (Ctrl+Alt+K) y tape une référence à la sélection : `@src/a.go#L10-20`, `@src/a.go#L5` pour une ligne, `@src/a.go` sans sélection. La touche n'agit que si le terminal tenon est le terminal actif. L'extension ajoute `tenon.insertReference` à `terminal.integrated.commandsToSkipShell`, pour que le terminal ne capte pas la touche. Si vous définissez `terminal.integrated.commandsToSkipShell` dans vos propres réglages, ajoutez-y `tenon.insertReference`.

**Langue.** L'extension suit la langue de VS Code : anglais ou français.

**Journaux.** **tenon : Voir les journaux** ouvre le canal de sortie « tenon ». **tenon : Relancer** redémarre `tenon acp`.

**Fonctionnement.** Pour lister et reprendre les sessions du dossier ouvert, l'extension utilise `session/list` et les métadonnées `_meta.tenon` (fournisseur, nom du fournisseur, modèle, juridiction, isolation avec son indicateur `confined`, profil, politique) que `tenon acp` renvoie pour `session/new` et `session/load`. Chaque `usage_update` porte le fournisseur, le nom et la juridiction de l'appel, si bien qu'un repli s'affiche aussitôt. L'extension ne fait aucun appel réseau et n'envoie aucune télémétrie. Ses vues web n'envoient à l'extension que des messages fixes (un identifiant de lien, jamais une URL ; l'identifiant d'un appel d'outil pour un diff), et l'extension vérifie chacun avant d'agir.

### Client tiers

VS Code n'a pas de client ACP intégré. L'extension tierce [ACP Client for VS Code](https://github.com/formulahendry/vscode-acp) (`formulahendry/vscode-acp`) en est un. Elle lit les agents dans le réglage `acp.agents` : chaque clé est le nom de l'agent, avec `command`, `args` et `env`. Dans `settings.json` :

```json
{
  "acp.agents": {
    "tenon": {
      "command": "tenon",
      "args": ["acp"],
      "env": {}
    }
  }
}
```

Définir `acp.agents` remplace la liste par défaut de l'extension. Recopiez d'abord les valeurs par défaut si vous voulez les garder.

Source : le réglage `acp.agents` dans le [package.json](https://github.com/formulahendry/vscode-acp/blob/main/package.json) de l'extension. Nous n'avons pas testé d'autres extensions VS Code.

## Ce qui reste appliqué

L'éditeur ne fait qu'afficher. tenon garde toutes ses garanties :

- **Permissions.** Les écritures, commandes et appels MCP soumis à validation sont demandés dans l'éditeur : Autoriser, Toujours pour cette session, Refuser, et Autoriser avec réseau pour une commande isolée. « Toujours pour cette session » n'accorde jamais le réseau. Une demande annulée ou une erreur du client vaut refus.
- **Isolation.** Les commandes s'exécutent dans l'isolation système de tenon, comme dans le terminal.
- **Masquage.** Les secrets sont masqués avant d'atteindre le modèle, le journal d'audit et l'éditeur (arguments, résultats, diffs).
- **Routage.** La classification, le routage local et les replis s'appliquent. Un envoi à un fournisseur hors UE demande un consentement dans l'éditeur ; le refus est la valeur par défaut.
- **Audit.** Chaque session écrit le même journal d'audit chaîné. `SessionStarted` enregistre l'éditeur (`client: {name, version}`) et `transport: "acp"`. L'export de conformité affiche l'éditeur de chaque session.

tenon n'utilise que ses propres outils. Il n'appelle jamais les méthodes `fs/*` ni `terminal/*` de l'éditeur.

## Serveurs MCP de l'éditeur

Certains éditeurs transmettent leurs propres serveurs MCP à l'ouverture d'une session. tenon les ignore par défaut et affiche l'avis `serveurs MCP de l'éditeur ignorés ([acp] accept_editor_mcp)`.

Pour les accepter, réglez dans la configuration **globale** (refusé dans une configuration projet ; une politique d'organisation peut l'imposer à `false`) :

```toml
[acp]
accept_editor_mcp = true
```

Les serveurs acceptés passent par les mêmes filtres que ceux de votre configuration : profil, politique, consentement de juridiction, isolation, audit. Valeurs par défaut :

- serveur stdio : `jurisdiction = "local"`, sans `risk` déclaré (pas de réseau, projet en lecture seule dans l'isolation) ;
- serveur http : `jurisdiction = "non-eu"` (consentement demandé), ou `local` pour `localhost`, `127.0.0.1` et `::1` ;
- les serveurs sse ne sont pas pris en charge ;
- un serveur de même identifiant qu'un serveur de votre configuration est ignoré : votre configuration l'emporte.

Les variables d'environnement et en-têtes HTTP fournis par l'éditeur sont ignorés, avec un avis. tenon n'en conserve ni n'en journalise jamais les valeurs. Un serveur qui a besoin d'un secret doit être déclaré dans la configuration de tenon (`env_from`, `bearer_env`).

## Limites

- Ni image ni audio dans les prompts : l'éditeur reçoit une erreur « invalid params ».
- tenon ne lit pas les tampons de l'éditeur. Il lit les fichiers sur le disque avec ses outils. Enregistrez vos fichiers avant de demander.
- Les fichiers joints (blocs `resource`) sont ajoutés au message : ils sont masqués et classés comme un texte saisi. Les liens de fichiers (`resource_link`) sont cités par leur chemin ; tenon les lit avec ses outils, sous permission.
- Un seul tour à la fois par session. Un second prompt pendant un tour reçoit `turn in progress`.
- tenon envoie ses avis (avis du démarrage, arrêts par hook) comme pensées de l'agent (`agent_thought_chunk`, préfixe `tenon:`), et les jetons en `usage_update` (`used` : jetons d'entrée du dernier appel, `size` : fenêtre de contexte de son fournisseur). Un `usage_update` est envoyé pour chaque appel de l'agent principal ; son `_meta.tenon` porte aussi les totaux de la session sur tous les appels, sous-agents, compaction et hooks compris : `sends` (`provider`, `providerName`, `jurisdiction`, `n` par fournisseur), `sessionIn` et `sessionOut`. Certains clients ignorent `usage_update`.
- `session/load` rejoue les messages, les appels d'outils (sous forme de texte) et le `usage_update` de chaque appel, puis répond. Le compte de jetons d'une session reprise (`sessionTokens`) inclut donc son historique. Les diffs ne sont pas rejoués.
- Pas de modes ACP (`session/set_mode`), pas de transport distant.
