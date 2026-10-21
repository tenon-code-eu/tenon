# tenon : liste blanche de fournisseurs et profil secnumcloud (v0.7)

Date : 2026-10-21. Statut : conception validée en séance, à relire.
Spec parente : [2026-10-14-tenon-design.md](2026-10-14-tenon-design.md). Specs liées : [profils de conformité](2026-10-20-tenon-compliance-profiles-design.md), [politique d'organisation](2026-10-20-tenon-org-policy-design.md), [export de conformité](2026-10-20-tenon-audit-export-design.md).

## 1. Objectif

Deux compléments aux profils :

1. Une **liste blanche d'hôtes de fournisseurs** dans la configuration utilisateur (globale et projet), pour qui n'a pas de politique d'organisation : tenon refuse tout fournisseur dont l'hôte n'y figure pas.
2. Un **profil `secnumcloud`** fondé sur une **qualification déclarée** : un fournisseur (ou serveur MCP) porte `qualifications = ["secnumcloud"]`, et le profil n'accepte que les destinataires locaux ou déclarés qualifiés. tenon ne vérifie pas la qualification (certification ANSSI d'un service, pas une propriété technique) : il trace qui l'a déclarée.

Critères de succès :

- un hôte hors d'une liste blanche présente n'est jamais contacté, quelle que soit l'option de ligne de commande ;
- sous `secnumcloud`, aucun octet de conversation ne part vers un destinataire ni local ni déclaré qualifié ;
- sous une politique d'organisation, l'utilisateur ne peut pas s'auto-qualifier ;
- sans liste blanche ni qualification ni profil `secnumcloud`, le comportement actuel est inchangé.

## 2. Décisions

| Sujet | Décision | Raison |
|---|---|---|
| Qualification | Déclarative, jamais vérifiée ; préréglages sans qualification | tenon ne peut pas prouver une certification ; il ne doit pas l'affirmer |
| Autorité de la déclaration | Sous politique : seules les déclarations de la politique (`providers.define`) comptent | Empêche un contournement d'un profil imposé |
| Liste blanche | Toutes les listes présentes (politique, globale, projet) doivent accepter l'hôte | Le projet ne peut que restreindre |
| Correspondance d'hôte | Celle de la politique, extraite dans un paquet commun | Une seule règle, déjà revue |
| Ordre des profils | aucun < `eu-only` < `secnumcloud` < `strict-local` | Chaque profil accepte un sous-ensemble du précédent |

## 3. Liste blanche

```toml
[provider_allowlist]          # config globale, et .tenon/config.toml
hosts = ["api.mistral.ai", "*.scaleway.ai"]
local = true                  # hôtes locaux (profile.IsLocalHost)
```

- Correspondance : hôte de `base_url` en minuscules, sans port ; `x.eu` exact ; `*.x.eu` = sous-domaines stricts de `x.eu`, jamais `x.eu` ; `local = true` admet `profile.IsLocalHost`.
- Entrées refusées au chargement (mêmes règles que la politique) : vide, avec schéma, chemin, port, ou joker ailleurs qu'en tête (`*.` suivi d'au moins deux labels).
- Section présente mais vide (ni `hosts` non vide ni `local = true`) : erreur de chargement `config: provider_allowlist is empty` (faute de frappe probable ; la politique, elle, refuse tout dans ce cas).
- Application : le fournisseur choisi doit être accepté par chaque liste présente (politique, globale, projet) :
  - au démarrage, après le contrôle de la politique et avant `detectLocal` : code 1, `provider "<id>" (host "<h>") not in provider_allowlist (<global|project>)` ;
  - avant chaque envoi, dans `Outbound.Check` (après la politique) : `Error{code:"allowlist"}`, tour terminé avec la raison `blocked`.
- La liste blanche ne concerne pas les serveurs MCP (le profil et la politique les couvrent).

## 4. Qualification et profil `secnumcloud`

- Champ `qualifications` (liste de chaînes) sur `[[providers]]`, `[[mcp.servers]]` et `[[providers.define]]` de la politique. Seule valeur acceptée en v0.7 : `secnumcloud` ; toute autre valeur est une erreur de chargement `config: unknown qualification "<x>" (secnumcloud)` (ou l'équivalent préfixé par le fichier de politique).
- Sous une politique d'organisation, les qualifications de la config utilisateur (fournisseurs et serveurs MCP) sont retirées ; notice `policy <id>: qualifications declared in config ignored (<ids>)`. Les fournisseurs `define` gardent les leurs.
- Profil `secnumcloud` :
  - fournisseur accepté : `jurisdiction = "local"` avec hôte local, ou `jurisdiction = "eu"` avec `secnumcloud` dans `qualifications` ;
  - serveur MCP accepté : `stdio` de juridiction `local` ; `http` local (juridiction `local` et hôte local) ou `eu` avec `secnumcloud` dans `qualifications` ;
  - `--allow-non-eu` refusé (règle existante des profils) ;
  - messages : `profile secnumcloud: provider "<id>" is not declared secnumcloud-qualified` ; MCP : raison `not declared secnumcloud-qualified` dans le message existant `profile <p>: MCP server "<id>" (<reason>) skipped`.
- `profile.Parse` : message `config: unknown profile "<x>" (eu-only|secnumcloud|strict-local)`.

## 5. Traçabilité

- `llm.ProviderInfo.Qualifications []string` (`json:"qualifications,omitempty"`) : présent dans `SessionStarted.provider` ; anciens événements inchangés.
- `tenon config check` : ligne `provider_allowlist: global <n> hosts, project <m> hosts` si une liste est présente (local signalé : `+ local`).
- Export : `ProviderUse.Qualifications []string` (`json:"qualifications"`, jamais nil) ; colonne HTML « Qualification déclarée » / « Declared qualification », avec la note « déclarée, non vérifiée par tenon » / « declared, not verified by tenon » ; au niveau `metadata`, indisponible comme le reste des fournisseurs.

## 6. Architecture

- `internal/hostmatch` (nouveau, stdlib seulement) : `HostOf(rawURL string) string`, `CheckEntry(e string) error`, `Match(entries []string, host string) bool`, `type List struct{ Name string; Hosts []string; Local bool }`, `(List) Allows(host string) bool` (`Local` via `profile.IsLocalHost` : `hostmatch` importe `internal/profile`, qui n'importe pas `hostmatch`).
- `internal/orgpolicy` : `checkHost`, `matchHost`, `HostOf` délèguent à `hostmatch` (`orgpolicy.HostOf` reste, alias) ; `ProviderDef.Qualifications`.
- `internal/llm` : `ProviderInfo.Qualifications`.
- `internal/profile` : `SecNumCloud`, `Parse`, `Stricter`, `CheckProvider`, `MCPTarget.Qualifications`, `CheckMCP`.
- `internal/config` : `Provider.Qualifications`, `MCPServer.Qualifications`, `ProviderAllowlist{Hosts []string; Local bool}`, `Config.Allowlist *ProviderAllowlist`, `Config.ProjectAllowlist *ProviderAllowlist` (clé `provider_allowlist` autorisée dans `projectFile`), validation ; `ApplyPolicy` retire les qualifications hors `define` et remplit `PolicyStrippedQualifications []string`.
- `internal/policy` : `Outbound.SetAllowlists(lists []hostmatch.List)`, `ErrAllowlist`.
- `internal/engine` : `Deps.Allowlists`, branche `ErrAllowlist` → `code:"allowlist"`.
- `cmd/tenon` : `providerInfo` recopie les qualifications ; refus au démarrage ; notices ; `config check` ; `Deps.Allowlists`.
- `internal/compliance` : `ProviderUse.Qualifications`, gabarit HTML.
- `internal/i18n`, docs EN/FR (profils dans la sécurité, configuration dans l'usage, `docs/config.example.toml`).

## 7. Tests

- `hostmatch` : tests d'hôtes repris de `orgpolicy` (exact, joker, apex refusé, suffixe piège, casse, port, local), `CheckEntry`.
- `profile` : `Parse`, `Stricter` (16 paires), `CheckProvider` sous `secnumcloud` (local+hôte local ✓, local+hôte distant ✗, eu qualifié ✓, eu non qualifié ✗, non-eu qualifié ✗), `CheckMCP` sous `secnumcloud`.
- `config` : qualification inconnue refusée, entrée de liste invalide, liste vide refusée, liste projet chargée à part, `ApplyPolicy` retire les qualifications utilisateur et garde celles de `define`.
- `policy.Outbound` : hôte hors liste → `ErrAllowlist` ; listes vides → aucun effet.
- Bout en bout : hôte hors liste globale (code 1, aucune requête) ; hôte accepté globalement mais hors liste projet (code 1) ; `secnumcloud` + fournisseur eu non qualifié (code 1) ; `secnumcloud` + fournisseur qualifié (code 0, `SessionStarted.provider.qualifications == ["secnumcloud"]`) ; politique imposant `secnumcloud` + fournisseur utilisateur auto-qualifié (code 1, notice) ; export avec la colonne.

## 8. Hors périmètre

Liste intégrée de fournisseurs qualifiés, vérification d'une qualification, autres qualifications (HDS, C5…), liste blanche des serveurs MCP dans la config utilisateur.
