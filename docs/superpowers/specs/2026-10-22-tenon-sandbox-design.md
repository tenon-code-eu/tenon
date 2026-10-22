# tenon : isolation système des commandes (v0.8)

Date : 2026-10-22. Statut : conception validée en séance, à relire.
Spec parente : [2026-10-14-tenon-design.md](2026-10-14-tenon-design.md) (feuille de route §2 : « Isolation au niveau système »). Specs liées : [profils de conformité](2026-10-20-tenon-compliance-profiles-design.md), [politique d'organisation](2026-10-20-tenon-org-policy-design.md), [export de conformité](2026-10-20-tenon-audit-export-design.md).

## 1. Objectif

Les permissions décident si une commande s'exécute, pas ce qu'elle peut faire une fois lancée. Une commande approuvée (ou couverte par une règle `allow`) peut aujourd'hui écrire partout, lire les secrets de l'utilisateur et exfiltrer par le réseau. Confiner les commandes shell et les serveurs MCP stdio avec les mécanismes du système : Landlock et espaces de noms sous Linux, `sandbox-exec` sous macOS.

Critères de succès :

- une commande shell isolée ne peut écrire que dans le projet, les temporaires et les caches ;
- elle ne peut lire ni `~/.ssh` ni les autres secrets connus, ni la configuration et les données de tenon (journal d'audit) ;
- elle n'a pas de réseau, sauf ouverture explicite (règle ou réponse à la demande) ;
- les variables d'environnement qui portent des secrets connus de tenon ne lui sont jamais transmises ;
- isolation active par défaut ; indisponible : notice et exécution non confinée, ou refus avec `required` ;
- aucune dépendance externe : stdlib et `golang.org/x/sys` (déjà vendorisé).

## 2. Décisions

| Sujet | Décision | Raison |
|---|---|---|
| Menace | Écriture + réseau ; lecture libre sauf secrets connus | Couvre destruction et exfiltration sans casser les chaînes d'outils |
| Activation | `auto` par défaut ; `required` refuse sans isolation ; `off` | Protège tout le monde sans bloquer les noyaux anciens |
| Réseau | Coupé ; ouvert par règle (`network = true`) ou par la réponse « avec réseau » | Granularité par commande, sans proxy à maintenir |
| Linux | Relance `tenon __sandbox` : Landlock (fichiers) + `CLONE_NEWUSER\|CLONE_NEWNET` (réseau) ; repli Landlock TCP | Go ne peut pas exécuter de code entre fork et exec ; le namespace coupe aussi UDP/DNS |
| macOS | `sandbox-exec -p <profil SBPL généré>` | Seul mécanisme disponible sans privilège ; déprécié mais fonctionnel |
| Environnement | Variables de secrets retirées toujours, même avec `off` | Fuite actuelle de la clé d'API vers chaque commande |
| Profils | `strict-local` implique `required` | Le profil promet que rien ne quitte la machine |
| Hors isolation | Hooks, gitmark | Commandes de l'utilisateur et de tenon lui-même |

## 3. Configuration

Config globale :

```toml
[sandbox]
mode = "auto"      # "auto" (défaut) | "required" | "off"
write = []         # chemins inscriptibles en plus des défauts
deny_read = []     # chemins interdits en lecture et écriture en plus de la liste intégrée
```

- `mode` inconnu : `config: sandbox.mode must be auto, required or off`.
- `write`, `deny_read` : chemins absolus ou commençant par `~/` ; sinon `config: sandbox.<clé>: path must be absolute or start with ~/`.
- Config projet (`.tenon/config.toml`) : peut seulement durcir. `mode = "required"` accepté ; `mode = "off"` et `write` refusés (`config: sandbox.<clé> not allowed in project configuration`) ; `deny_read` accepté (ajouté).
- Ordre de durcissement : `off` < `auto` < `required`. Mode effectif = le plus strict de global, projet, politique, profil.

Politique d'organisation :

```toml
[sandbox]
mode = "required"  # "auto" | "required" ; "off" refusé à la lecture de la politique
deny_read = ["/srv/secrets"]
```

- Plancher : la politique ne fait que durcir ; `deny_read` ajouté. `write` absent de la politique.

Profil : `strict-local` impose `required`. Les autres profils ne changent pas le mode.

Règles de permission (shell seulement) :

```toml
[[permissions.rules]]
tool = "shell"
match = "go mod download *"
decision = "allow"
network = true
```

- `network = true` : la commande correspondante s'exécute avec réseau. Accepté avec `allow` et `ask` ; avec `deny` : `config: network only with allow or ask`. Sur un autre outil que `shell` : `config: network only for tool shell`.
- Refusé dans la config projet (`config: network not allowed in project configuration`) et dans la politique (une politique ne peut pas ouvrir).
- Pour `ask`, la réponse « autoriser » donne le réseau de la règle.

## 4. Ce que voit une commande isolée

`Spec` construite par tenon pour chaque lancement :

- **Dossier** : la racine du projet (shell) ou le dossier propre du serveur (MCP stdio).
- **Écriture** autorisée : projet ; `$TMPDIR` et `/tmp` (macOS : aussi `/private/tmp`, `/private/var/folders`) ; `/dev/null`, `/dev/tty`, `/dev/zero`, `/dev/random`, `/dev/urandom` ; cache utilisateur (`$XDG_CACHE_HOME` ou `~/.cache` ; macOS : `~/Library/Caches`) ; `~/.npm` (R20) ; `write`.
- **Interdit en écriture seule** (`DenyWrite`, macOS seulement, R19) : `<projet>/.git/hooks`, `config`, `commondir` et `config.worktree`, présents ou non (dossier `.git` ordinaire ; fichier existant en `literal`, sinon `subpath`), et chaque dossier parent en `(deny file-write-unlink (literal …))` (ni renommage ni suppression ; touch, tar, rsync possibles). Au mieux : `.git/modules/*` et dépôts imbriqués non couverts. Ignoré par l'assistant Linux (R9).
- **Lecture** : partout.
- **Interdit en lecture et écriture** (prime sur ce qui précède, même dans le projet) : `~/.ssh`, `~/.gnupg`, `~/.aws`, `~/.azure`, `~/.config/gcloud`, `~/.kube`, `~/.docker`, `~/.netrc`, `~/.password-store`, `~/Library/Keychains` (trousseau macOS, R16), le dossier de configuration de tenon, le dossier de données de tenon (journal d'audit, sessions), le fichier de politique, `deny_read` (global, projet, politique). Chemins absents : ignorés.
- **Réseau** : coupé, sauf `Network`.
- **Environnement** : celui de tenon moins les variables nommées par `api_key_env` (tous les fournisseurs, et toujours tous les préréglages, même redéfinis : R17), `bearer_env` et `env_from` (tous les serveurs MCP). Le retrait s'applique aussi en mode `off` et quand l'isolation est indisponible. La variable interne de la spec n'atteint jamais la commande.

Serveurs MCP stdio : isolés selon `risk` déclaré. `network` : réseau ouvert. `write` ou `exec` : projet inscriptible. `read` ou vide : projet en lecture seule. Leur dossier de travail (`$HOME`) n'est pas rendu inscriptible : seuls les défauts (temporaires, caches) et `write` le sont.

## 5. Mécanismes

### Linux

- `Wrap` remplace la commande par `<os.Executable()> __sandbox -- <argv d'origine>`, avec la spec en JSON dans `TENON_SANDBOX_SPEC` ; `Dir`, `Setpgid`, `Cancel` et `WaitDelay` sont conservés.
- Réseau coupé : `SysProcAttr.Cloneflags = CLONE_NEWUSER|CLONE_NEWNET`, `UidMappings`/`GidMappings` de l'utilisateur sur lui-même (taille 1), `GidMappingsEnableSetgroups = false`. Le namespace n'a qu'un loopback, sans route.
- `tenon __sandbox` : traité en tout début de `main`, avant tout chargement de configuration ou d'i18n. Lit et retire `TENON_SANDBOX_SPEC`, lit l'ABI Landlock (`landlock_create_ruleset(NULL, 0, LANDLOCK_CREATE_RULESET_VERSION)`), pose `PR_SET_NO_NEW_PRIVS`, crée le ruleset (droits fichiers de l'ABI, plus `LANDLOCK_ACCESS_NET_CONNECT_TCP`/`BIND_TCP` en repli TCP), ajoute les règles, `landlock_restrict_self`, puis `syscall.Exec` (recherche dans `PATH`). Échec : message sur stderr `sandbox: <raison>`, code 126, la commande n'est pas lancée.
- Interdits sous Landlock (listes d'autorisation seulement) : pour chaque chemin interdit, remonter depuis `/` (lecture) ou depuis la zone inscriptible qui le contient (écriture) et autoriser chaque voisin à chaque niveau, sans le composant interdit. La liste est figée au lancement : une entrée créée ensuite dans un dossier non inscriptible n'est pas lisible par la commande (documenté).
- Repli réseau : si `CLONE_NEWUSER` échoue (userns désactivés, AppArmor, seccomp de conteneur), Landlock TCP (ABI ≥ 4, noyau ≥ 6.7) refuse `connect` et `bind` TCP ; UDP reste ouvert. Notice au démarrage : `sandbox: network isolation TCP only (user namespaces unavailable)`.
- Ni namespace ni Landlock TCP : réseau non isolé. `auto` : notice `sandbox: network not isolated (<raison>)` ; `required` : refus au démarrage.
- Landlock absent (noyau < 5.13, LSM non chargé) : isolation indisponible.

### macOS

- `Wrap` remplace la commande par `/usr/bin/sandbox-exec -p <profil> -- <argv d'origine>`.
- Profil SBPL généré, dans cet ordre (la dernière règle qui correspond l'emporte) : `(version 1)`, `(allow default)`, `(deny file-write*)`, `(allow file-write* (subpath …) (literal …))` pour les chemins inscriptibles, `(deny file-read* file-write* (subpath …))` pour les interdits, `(deny network*)` si le réseau est fermé. Chemins canoniques (`EvalSymlinks`) et échappés.
- `sandbox-exec` est déprécié par Apple mais fonctionnel ; documenté.

### Autres systèmes

Isolation indisponible (`unsupported OS`).

### Sonde

`Probe() Status` au démarrage, une fois par processus : Linux : ABI Landlock, puis lancement de `tenon __sandbox --probe` avec `CLONE_NEWUSER|CLONE_NEWNET` (succès = réseau complet) ; macOS : `sandbox-exec -p '(version 1)(allow default)' /usr/bin/true`. Délai 2 s.

## 6. Demande d'approbation

- Avec isolation active, la demande pour une commande shell propose « Autoriser », « Autoriser avec réseau », « Toujours autoriser » (sans réseau), « Refuser ».
- `PermissionResolved` gagne `network bool`. « Toujours autoriser » ne mémorise jamais le réseau.
- Headless : pas de demande ; le réseau ne vient que d'une règle.
- Isolation inactive : options inchangées.

## 7. Journal, export, `config check`

- `SessionStarted.sandbox` : `{mode, mechanism ("landlock"|"sandbox-exec"|"none"), network ("full"|"tcp"|"none"), reason}`.
- `ToolResult` des appels `shell` et le démarrage des serveurs MCP stdio : `sandbox: {confined bool, network bool}`.
- Export de conformité : ligne « Isolation » / « Sandbox » (mode, mécanisme, isolation réseau), et nombre de commandes lancées non confinées ou avec réseau.
- `tenon config check` : `sandbox: <mode> (<mechanism>, network <full|tcp|none>)`, ou `sandbox: <mode> (unavailable: <raison>)`.

## 8. Architecture

- `internal/sandbox` (nouveau) : `type Spec struct { Dir string; Write, DenyRead []string; Network bool }`, `type Status struct { Available bool; Mechanism, Network, Reason string }`, `Probe() Status`, `Wrap(cmd *exec.Cmd, s Spec) error`, `Main(args []string) int` (helper `__sandbox`), `DefaultDenyRead(home, configDir, dataDir, policyPath string) []string`, `DefaultWrite(project, home string, getenv func(string) string) []string` ; fichiers `landlock_linux.go`, `seatbelt_darwin.go`, `sandbox_other.go`.
- `cmd/tenon` : `__sandbox` en tête de `main` ; mode effectif, sonde, notices, refus `required` ; `config check`.
- `internal/config` : `Sandbox{Mode, Write, DenyRead}`, `Rule.Network`, durcissement projet et politique.
- `internal/orgpolicy` : `Policy.Sandbox{Mode, DenyRead}`.
- `internal/profile` : `strict-local` → `required`.
- `internal/tools` : `Shell` reçoit la spec de base et l'environnement filtré ; `Run` applique `Network` de la décision.
- `internal/policy`, `internal/engine` : la décision porte `Network` (règle ou réponse) jusqu'à l'outil.
- `internal/mcp` : `Wrap` sur les serveurs stdio selon `risk`.
- `internal/event`, `internal/compliance`, `internal/ui/tui` (option « avec réseau »), `internal/i18n`, docs EN/FR (sécurité, usage, politique, `docs/config.example.toml`).

## 9. Tests

- `sandbox` (unitaires) : profil SBPL (ordre, échappement, réseau), découpage des interdits sur une arborescence temporaire (voisins autorisés, composant interdit absent, interdit dans une zone inscriptible), chemins absents ignorés, `DefaultWrite`/`DefaultDenyRead`.
- `sandbox` (intégration, ignorés si `Probe` indisponible) : écriture hors projet refusée ; écriture dans le projet acceptée ; lecture d'un `~/.ssh` factice (HOME temporaire) refusée ; interdit dans le projet refusé ; `connect` vers un `httptest` hors du namespace refusé ; accepté avec `Network` ; code 126 sur spec invalide. Repli TCP testé quand les userns sont indisponibles (CI en conteneur).
- `config` / `orgpolicy` / `profile` : mode invalide, `off` et `write` refusés en projet, `network` sur `deny` ou autre outil refusé, `network` refusé en projet et en politique, politique `off` refusée, durcissement, `strict-local` → `required`.
- `tools` : variables de secrets absentes de l'environnement de la commande, avec et sans isolation.
- Bout en bout : `required` + isolation indisponible (sonde simulée) → code 1 ; `auto` indisponible → notice, commande exécutée ; règle `network = true` ; réponse « avec réseau » ; `SessionStarted.sandbox` et `ToolResult.sandbox` dans le journal ; ligne d'export.

## 10. Hors périmètre

Isolation des hooks, liste d'hôtes réseau (proxy filtrant), espace de noms PID ou montage, seccomp, limites de ressources (cgroups), Windows, isolation de tenon lui-même.
