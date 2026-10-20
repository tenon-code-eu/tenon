# tenon : politique d'organisation signée (v0.7)

Date : 2026-10-20. Statut : conception validée en séance, à relire.
Spec parente : [2026-10-14-tenon-design.md](2026-10-14-tenon-design.md). Specs liées : [profils de conformité](2026-10-20-tenon-compliance-profiles-design.md), [export de conformité](2026-10-20-tenon-audit-export-design.md).

## 1. Objectif

Permettre à une organisation d'imposer à tous ses postes un socle que l'utilisateur ne peut pas assouplir : profil plancher, fournisseurs autorisés, règles de permissions et de masquage, niveau d'audit, serveurs MCP autorisés. La politique est un fichier déployé par l'administrateur (MDM, Ansible), signé avec minisign, vérifié à chaque démarrage. Elle est tracée dans le journal d'audit et dans l'export de conformité.

Critères de succès :

- une politique présente et valide s'applique, quelles que soient la config globale, la config projet et les options de ligne de commande ;
- une politique présente mais invalide, non signée, mal signée ou modifiable par l'utilisateur empêche tenon de démarrer (échec fermé) ;
- sans politique, le comportement actuel est inchangé ;
- la politique appliquée (id, version, clé) est visible au démarrage, dans `tenon config check`, dans `SessionStarted` et dans l'export.

## 2. Décisions

| Sujet | Décision | Raison |
|---|---|---|
| Ancrage | Chemin système possédé par root, clé publique à côté | L'utilisateur ne peut ni modifier ni retirer la politique ; aucun réseau |
| Signature | minisign, formats `ED` (préhaché BLAKE2b-512) et `Ed` (historique) | Outil déjà utilisé pour les releases ; l'admin signe avec `minisign -Sm policy.toml` |
| Dépendance | `golang.org/x/crypto/blake2b`, vendoré | Module officiel Go ; pas de crypto maison |
| Nature | Plancher : la politique ne fait que durcir | Même principe que le projet et les profils |
| Échec | Fermé : toute anomalie empêche le démarrage | Une politique ignorée en silence ne protège rien |
| Point d'application | 3ᵉ couche de `config.Load` + contrôle d'hôte dans `Outbound.Check` | Un seul point de fusion, défense en profondeur avant chaque envoi |

## 3. Emplacement et chargement

- Dossier système : `/etc/tenon/` (Linux), `/Library/Application Support/tenon/` (macOS). Fichiers : `policy.toml`, `policy.toml.minisig`, `policy.pub` (clé publique minisign, format du fichier `.pub` de minisign).
- `policy.toml` absent du dossier système : tenon regarde `TENON_POLICY_DIR` s'il est défini (politique personnelle ou de test). La variable n'est jamais consultée quand une politique système existe : elle peut ajouter une politique, jamais en retirer ou en remplacer une.
- `policy.toml` absent partout : aucune politique.
- `policy.toml` présent, tenon refuse de démarrer (code 1, `policy: <fichier>: <raison>`) si :
  - `policy.toml.minisig` ou `policy.pub` manque ;
  - l'identifiant de clé de la signature diffère de celui de `policy.pub` ;
  - la signature du fichier ou la signature globale (commentaire de confiance) est invalide ;
  - dans le dossier système seulement : un des trois fichiers, ou le dossier, est inscriptible par le groupe ou les autres (`mode & 0o022 != 0`), ou appartient à l'utilisateur courant alors que celui-ci n'est pas root ;
  - le TOML est invalide ou contient une clé inconnue, ou une valeur est refusée (§4).
- La politique est vérifiée une fois, au démarrage, avant le chargement de la configuration.

## 4. Contenu

```toml
id = "acme-dev"                          # obligatoire, [a-z0-9_-]{1,64}
version = 3                              # obligatoire, entier ≥ 1
profile = "eu-only"                      # optionnel : plancher

[providers]                              # optionnel : liste blanche
allow_hosts = ["api.mistral.ai", "*.scaleway.ai"]
allow_local = true                       # hôtes locaux (profile.IsLocalHost) autorisés

[[providers.define]]                     # optionnel : fournisseurs fournis par l'org
id = "interne"
name = "vLLM interne"
base_url = "https://llm.acme.eu/v1"
api_key_env = "ACME_LLM_KEY"
model = "qwen3"
country = "FR"
jurisdiction = "eu"
context_window = 128000

[[permissions.rules]]                    # deny / ask seulement
tool = "shell"
match = "curl *"
decision = "deny"

[[redaction.rules]]
name = "acme-token"
pattern = "ACME-[A-Z0-9]{32}"

[audit]
level = "full"                           # optionnel : full | metadata, imposé

[mcp]
mode = "allowlist"                       # any (défaut) | allowlist | off
allow_hosts = ["mcp.acme.eu"]
allow_commands = ["/usr/local/bin/mcp-git"]
```

Valeurs refusées au chargement : `id` ou `version` absents ou invalides ; profil inconnu ; règle `allow` ; décision ou outil invalides (mêmes règles que la config) ; motif de masquage qui ne compile pas ; entrée de `allow_hosts` vide, avec schéma, chemin, port ou joker ailleurs qu'en tête (`*.` suivi d'au moins deux labels) ; `allow_commands` non absolu ; `mcp.mode` inconnu ; `audit.level` inconnu ; `providers.define` invalide (mêmes règles que `[[providers]]`, `jurisdiction` obligatoire).

## 5. Application

Ordre de fusion : défauts → global → projet → politique.

- Profil : profil effectif = `Stricter(global, projet, politique)`. Si la config globale ou projet demande un profil plus faible que celui de la politique, la notice existante le signale avec la mention « imposé par la politique ».
- Fournisseurs définis : fusionnés après la config utilisateur. Une entrée utilisateur de même `id` est ignorée, avec un avertissement au démarrage.
- Liste blanche (seulement si `[providers]` est présent) : l'hôte de `base_url` (minuscules, sans port) doit correspondre à une entrée de `allow_hosts` (égalité exacte, ou `*.x.eu` qui couvre les sous-domaines de `x.eu` mais pas `x.eu`), ou être local avec `allow_local = true`. Les fournisseurs `define` y sont soumis comme les autres.
  - Fournisseur choisi non autorisé : refus au démarrage, avant `detectLocal`, code 1 : `fournisseur <id> (hôte <h>) non autorisé par la politique <id>`. `--allow-non-eu` ne lève pas ce refus.
  - Même contrôle dans `Outbound.Check` avant chaque envoi (après le profil) : `Error{code:"policy"}`, tour terminé avec la raison `blocked`.
- Règles de permissions : placées en tête, marquées `Policy`. `deny` est absolue ; `ask` prime les `allow` globales (comme les règles projet).
- Masquage : règles ajoutées aux autres.
- Audit : `audit.level` de la politique remplace celui de la config ; s'ils diffèrent, notice au démarrage.
- MCP : `off` retire tous les serveurs ; `allowlist` garde un serveur seulement si son hôte (`http`) figure dans `allow_hosts` (même correspondance que les fournisseurs, sans `allow_local`) ou si `command[0]` (`stdio`) figure tel quel dans `allow_commands`. Un serveur retiré suit le chemin du refus par profil : notice, événement `McpServerDown`, retrait avant `mcp.Start`.
- Le contrôle MCP du profil s'applique aussi aux serveurs gardés.

## 6. Traçabilité et commandes

- `event.SessionStarted.Policy *PolicyRef{ID string; Version int; KeyID string}` (`json:"policy,omitempty"`), `KeyID` en hexadécimal majuscule (16 caractères, comme minisign).
- Écran d'accueil et `tenon config check` : `politique : acme-dev v3 (clé <KeyID>)`, seulement si une politique est active.
- Export de conformité : section « Politique d'organisation » (id, version, clé, nombre de sessions par politique) ; au niveau `metadata`, indisponible ; sessions sans politique non listées.
- `tenon policy check [dossier]` (défaut : le dossier système) : vérifie signature et contenu sans contrôler les permissions, affiche un résumé (id, version, clé, profil, nombre d'hôtes, de règles, mode MCP, niveau d'audit). Codes : 0 valide, 1 invalide ou absente.

## 7. Architecture

- `internal/orgpolicy` (nouveau, n'importe ni `config` ni `policy`) :
  - `minisign.go` : `type PublicKey struct{ KeyID [8]byte; Key ed25519.PublicKey }`, `ParsePublicKey(b []byte) (PublicKey, error)`, `Verify(pk PublicKey, msg, sig []byte) (trustedComment string, err error)`.
  - `policy.go` : `type Policy struct` (champs du §4, `KeyID string` ajouté au chargement), `Parse(b []byte) (Policy, error)`, `(*Policy) AllowsHost(host string) bool`, `(*Policy) AllowsMCP(transport, url, command0 string) bool`, `HostOf(baseURL string) string`.
  - `load.go` : `SystemDir() string`, `Resolve(getenv func(string) string) (dir string, system bool)`, `Load(dir string, checkPerms bool) (*Policy, error)` (`nil, nil` si `policy.toml` absent).
- `internal/config` : `Load(globalPath, projectPath string, getenv func(string) string, pol *orgpolicy.Policy)` ; `Config.Policy *orgpolicy.Policy` ; `Rule.Policy bool` ; champs de notice (`OverriddenProviders []string`, `PolicyAuditOverride bool`).
- `internal/policy` : `Outbound.SetHostPolicy(p *orgpolicy.Policy)` ; `Check` appelle `p.AllowsHost` ; erreur enveloppant `orgpolicy.ErrPolicy`.
- `internal/engine` : `Deps.Policy`, transmis à `Outbound.SetHostPolicy` dans `engine.New` ; `SessionStarted.Policy`.
- `cmd/tenon` : chargement de la politique avant `config.Load` ; refus fournisseur ; filtre MCP ; notices ; `tenon policy check` ; `config check`.
- `internal/compliance` : `Report.Policies`.
- `internal/i18n`, docs EN/FR : `docs/policy.md`, `docs/politique.md`, sécurité, schéma de l'export.

## 8. Tests

- minisign : vecteurs générés une fois avec `minisign` et commités dans `testdata` (clé publique, signature `ED`, signature `Ed`) ; signature altérée, commentaire de confiance altéré, autre clé, fichiers mal formés.
- `Parse` : chaque refus du §4 ; politique minimale (`id`, `version`).
- `AllowsHost` : exact, joker, apex refusé, casse, port ignoré, local avec et sans `allow_local`.
- `Load` : absente → `nil` ; permissions 0666 refusées ; propriétaire = utilisateur courant refusé avec `checkPerms` ; `TENON_POLICY_DIR` ignoré quand une politique système existe (dossier système injecté).
- `config.Load` : plancher de profil, `define` qui gagne, règles en tête marquées, `allow` refusé, niveau d'audit imposé.
- `Outbound.Check` : hôte refusé → `ErrPolicy`.
- Bout en bout : hôte refusé (code 1, message) ; session valide avec `SessionStarted.policy` ; serveur MCP retiré ; export avec la section ; `tenon policy check` valide et invalide.

## 9. Hors périmètre

Hooks imposés par l'organisation, rafraîchissement par URL, expiration ou révocation de politique, plusieurs clés de confiance, Windows.
