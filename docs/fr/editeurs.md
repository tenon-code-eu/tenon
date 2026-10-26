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
- tenon envoie ses avis (avis du démarrage, arrêts par hook) comme pensées de l'agent (`agent_thought_chunk`, préfixe `tenon:`), et les jetons en `usage_update`. Certains clients ignorent `usage_update`.
- `session/load` rejoue les messages et les appels d'outils sous forme de texte. Les diffs ne sont pas rejoués.
- Pas de modes ACP (`session/set_mode`), pas de transport distant.
