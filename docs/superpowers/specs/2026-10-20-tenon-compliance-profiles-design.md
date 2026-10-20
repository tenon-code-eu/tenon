# tenon : profils de conformité (v0.7)

Date : 2026-10-20. Statut : conception validée en séance, à relire.
Spec parente : [2026-10-14-tenon-design.md](2026-10-14-tenon-design.md). Spec liée : [export de conformité](2026-10-20-tenon-audit-export-design.md).

## 1. Objectif

Permettre à un utilisateur ou à son organisation de verrouiller où partent les données : un profil empêche tenon d'envoyer la conversation, et les appels d'outils MCP, ailleurs qu'à des destinataires de l'UE ou du poste local, sans qu'une configuration de projet ou une option de ligne de commande puisse l'assouplir. Le profil appliqué est tracé dans le journal d'audit et apparaît dans l'export de conformité.

Critères de succès :

- avec un profil, aucun octet de conversation ne part vers un fournisseur ou un serveur MCP interdit, même avec `--allow-non-eu` ou une configuration de projet hostile ;
- le profil effectif est visible (écran d'accueil, `tenon config check`) et enregistré dans `SessionStarted` ;
- sans profil, le comportement actuel est inchangé.

## 2. Décisions

| Sujet | Décision | Raison |
|---|---|---|
| Autorité | Config globale ; le projet peut seulement durcir | Un dépôt ne doit jamais assouplir une garantie (même principe que les permissions) |
| Nature | Plancher vérifié, pas surcouche de valeurs | Une surcouche peut être écrasée par une valeur posée plus tard |
| Profils v0.7 | `eu-only`, `strict-local` | Vérifiables par tenon lui-même ; pas de qualification déclarative (SecNumCloud) |
| Contrôle | Une fonction pure, appelée au chargement, au démarrage et avant chaque envoi | Défense en profondeur sans logique dupliquée |
| Portée | Données envoyées au modèle et aux serveurs MCP | Les commandes `shell` restent sous les règles de permissions ; dit dans la doc |

## 3. Profils

Ordre de sévérité : aucun profil < `eu-only` < `strict-local`.

### `eu-only`

- Fournisseur : juridiction `eu` ou `local`.
- Serveurs MCP (stdio et http) : juridiction `eu` ou `local`.
- `--allow-non-eu` refusé ; la TUI ne propose jamais le consentement hors UE.

### `strict-local`

- Fournisseur : juridiction `local` **et** hôte de `base_url` local.
- Serveurs MCP : stdio de juridiction `local` ; http de juridiction `local` et d'hôte local.
- `--allow-non-eu` refusé.
- Hôte local : `localhost`, ou une adresse IP littérale de boucle locale (127.0.0.0/8, ::1) ou privée (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, fc00::/7). Tout autre nom d'hôte est refusé, même déclaré `local` (pas de résolution DNS).

### Hors périmètre des profils

Règles de permissions des outils, masquage (toujours actif), niveau d'audit, hooks, points de restauration : inchangés. Une commande `shell` peut toujours accéder au réseau ; le profil porte sur ce que tenon envoie au modèle et aux serveurs MCP.

## 4. Configuration

- Clé `profile = "eu-only" | "strict-local"`, dans la config globale et dans `.tenon/config.toml`. Absente : aucun profil.
- Profil effectif = le plus strict des deux. Un projet qui demande un profil plus souple que le profil global est ignoré, avec l'avis `project profile "<p>" is weaker than global profile "<g>": ignored` (i18n).
- Valeur inconnue : `config: unknown profile "<x>" (eu-only|strict-local)`.
- Seul le fournisseur sélectionné (`--provider`, sinon `default_provider`) est contrôlé au démarrage ; les autres fournisseurs déclarés ou préconfigurés ne sont pas une erreur. Ainsi, avec `strict-local` et `default_provider = "mistral"` (défaut), le démarrage échoue et le message propose `--provider ollama` ou `default_provider`.

## 5. Contrôles et erreurs

| Situation | Comportement |
|---|---|
| Fournisseur sélectionné interdit | Code 1 avant tout envoi : `profile eu-only: provider "<id>" (<juridiction>) not allowed` |
| Hôte non local (`strict-local`) | Code 1 : `profile strict-local: provider "<id>" url host "<hôte>" is not a local address` |
| `--allow-non-eu` avec un profil | Code 1 : `profile <p>: --allow-non-eu not allowed` |
| Serveur MCP interdit | Non lancé ni contacté ; avis `profile <p>: MCP server "<id>" (<raison>) skipped` et `McpServerDown{error}` ; le démarrage continue |
| Violation détectée avant un envoi (défense en profondeur) | Envoi refusé, `Error{code:"profile"}`, tour terminé `blocked` |
| `tenon config check` | Affiche le profil effectif ; code 1 si le fournisseur sélectionné viole le profil |

Les messages sont dans le catalogue i18n (EN/FR).

## 6. Traçabilité

- `SessionStartedPayload.Profile string` (`json:"profile,omitempty"`). À la reprise, le profil est recalculé ; le `SessionStarted{resumed}` porte le profil courant.
- Écran d'accueil : ligne `profil : <p>` / `profile: <p>` quand un profil s'applique.
- Export de conformité : `report.profiles` (profils distincts des sessions du périmètre, triés ; vide si aucun) ; `profile` dans chaque `ProviderUse` n'est pas ajouté (un fournisseur peut servir sous plusieurs profils) : à la place, `report.profiles` et, dans le HTML, une ligne d'en-tête « Profils appliqués ». Au niveau `metadata`, inconnu.

## 7. Architecture

- `internal/profile` (nouveau, pur) :
  - `type Name string` ; `None`, `EUOnly = "eu-only"`, `StrictLocal = "strict-local"` ;
  - `Parse(s string) (Name, error)` ; `Stricter(a, b Name) Name` ;
  - `CheckProvider(n Name, p llm.ProviderInfo) error` ; `CheckMCP(n Name, s MCPTarget) error` avec `MCPTarget{ID, Transport, URL, Jurisdiction string}` (pas d'import de config) ;
  - `IsLocalHost(host string) bool`.
- `internal/config` : champ `Profile` (global et projet), `Config.Profile` effectif et l'avis d'affaiblissement.
- `cmd/tenon` : contrôle du fournisseur sélectionné, refus de `--allow-non-eu`, filtrage MCP avant `mcp.Start`, avis, `config check`, écran d'accueil (option TUI).
- `internal/policy` (`Outbound`) et le consentement MCP : appel de `CheckProvider`/`CheckMCP` avant envoi.
- `internal/event` : champ `profile` ; `internal/compliance` : `Profiles []string`, rendu HTML/JSON, golden mis à jour.
- Docs EN/FR : section « Profils de conformité » (usage, sécurité), exemple de config.

## 8. Tests

- `profile` : `Parse`, `Stricter`, `CheckProvider`/`CheckMCP` en table (eu, non-eu, local avec hôtes `localhost`, `127.0.0.1`, `::1`, `10.1.2.3`, `192.168.0.5`, `fd00::1`, `gpu.lan`, `example.com`, `8.8.8.8`), `IsLocalHost`.
- `config` : profil global, projet plus strict appliqué, projet plus souple ignoré avec avis, valeur inconnue.
- CLI bout en bout : `eu-only` + fournisseur `non-eu` → code 1 sans requête ; `--allow-non-eu` → code 1 ; `strict-local` + `ollama` sur `localhost` → OK ; `strict-local` + défaut `mistral` → code 1 ; MCP non-eu ignoré sous `eu-only` avec avis ; `SessionStarted.profile` dans session et audit ; `config check` affiche le profil.
- Défense en profondeur : `Outbound` avec profil refuse un fournisseur interdit même consenti.
- Export : `profiles` dans JSON et HTML.

## 9. Hors périmètre

Profils personnalisés, profil `secnumcloud` (qualification non vérifiable par tenon), fichier de politique système ou signé, contrôle réseau des commandes `shell`.
