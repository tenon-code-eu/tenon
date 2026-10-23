# tenon : attestation de provenance du code généré (v0.8)

Date : 2026-10-24. Statut : conception validée en séance, à relire.
Spec parente : [2026-10-14-tenon-design.md](2026-10-14-tenon-design.md) (marquage git du code généré). Specs liées : [export de conformité](2026-10-20-tenon-audit-export-design.md), [politique d'organisation](2026-10-20-tenon-org-policy-design.md), [horodatage RFC 3161](2026-10-21-tenon-audit-timestamp-design.md).

## 1. Objectif

Pour chaque commit qui contient du code écrit par l'agent, produire une attestation in-toto signée qui dit quels fichiers en viennent, de quel modèle et de quel fournisseur (pays compris), dans quelles sessions, et à quelle tête du journal d'audit elle se rattache. N'importe qui la vérifie hors ligne avec la clé publique. Usages : transparence (AI Act), preuve dans les marchés publics, traçabilité de la propriété intellectuelle.

Critères de succès :

- un commit fait après une session de l'agent porte une note `refs/notes/tenon-provenance` signée, sans modifier le commit ;
- `tenon audit provenance <rev>` distingue `verified`, `untrusted`, `invalid`, `stale`, `missing`, `none` ;
- un fichier retouché à la main après l'agent est marqué `agent-modified`, jamais `agent` ;
- le hook ne fait jamais échouer un commit ;
- l'attestation ne contient ni prompt ni code ;
- aucune dépendance nouvelle (ed25519 et sha256 de la bibliothèque standard).

## 2. Décisions

| Sujet | Décision | Raison |
|---|---|---|
| Format | Statement in-toto v1 dans une enveloppe DSSE | Standards ouverts de la chaîne d'approvisionnement |
| Signature | Clé ed25519 locale ; clés de confiance dans la politique | Hors ligne, sans service tiers ni identité OIDC |
| Stockage | Notes git `refs/notes/tenon-provenance` + copie dans le dossier de données | Voyage avec le code sans réécrire l'historique |
| Déclenchement | Hook `post-commit` (+ `post-rewrite`) ; commande de rattrapage | Le poste qui a les sessions est le seul à pouvoir attester |
| Attribution | Hash du contenu commité comparé aux écritures de l'agent | Même base que le marquage `gitmark` |

## 3. Attestation

Statement in-toto v1 :

```json
{
  "_type": "https://in-toto.io/Statement/v1",
  "subject": [
    {"name": "git+commit", "digest": {"gitCommit": "<sha>"}},
    {"name": "internal/foo.go", "digest": {"sha256": "<contenu>", "gitBlob": "<oid>"}}
  ],
  "predicateType": "https://tenon-code.eu/provenance/ai-assisted/v1",
  "predicate": {
    "commit": "<sha>",
    "tree": "<oid>",
    "generator": {"name": "tenon", "version": "<version>"},
    "files": [{
      "path": "internal/foo.go",
      "origin": "agent",
      "sessions": ["<id>"],
      "model": "<modèle>",
      "provider": {"id": "<id>", "jurisdiction": "eu", "country": "FR"},
      "written_at": "<RFC 3339>"
    }],
    "audit": {"head": "<hash de tête du journal>", "timestamped": true},
    "profile": "<profil ou vide>",
    "policy": "<id de politique ou vide>"
  }
}
```

- Sujets : le commit, puis chaque fichier attribué (sha256 du contenu au commit, oid git du blob), triés par chemin.
- `origin` : `agent` quand le contenu commité est exactement le dernier contenu écrit par l'agent pour ce chemin (hash égal à un `HashAfter` d'une écriture réussie) ; `agent-modified` quand l'agent a écrit ce chemin dans une session du projet mais que le contenu commité ne correspond à aucune de ses écritures. Un fichier jamais écrit par l'agent n'apparaît pas.
- `sessions`, `model`, `provider`, `written_at` : ceux de l'écriture retenue (la plus récente qui correspond, ou la plus récente pour `agent-modified`) ; plusieurs sessions possibles si plusieurs écritures ont le même hash.
- `audit.head` : hash de tête du journal au moment de l'attestation ; `timestamped` : vrai si un jeton RFC 3161 couvre cette tête ou une tête postérieure.
- Ni prompt, ni contenu de fichier, ni chemin hors du dépôt.

Enveloppe DSSE (`application/vnd.in-toto+json`) : `{"payloadType", "payload" (base64), "signatures": [{"keyid", "sig"}]}` ; signature ed25519 sur `PAE(payloadType, payload)` (`"DSSEv1" SP len(type) SP type SP len(body) SP body`).

## 4. Clés, configuration, politique

- `tenon provenance keygen [--force]` : écrit `<config>/provenance/ed25519.key` (0600, PKCS#8 PEM) et `ed25519.pub` (`ed25519:<base64 de la clé publique brute>`) ; refuse d'écraser sans `--force`. `keyid` = 16 premiers caractères hex du sha256 de la clé publique brute.
- Config globale :

```toml
[provenance]
enabled = false          # true : le hook atteste
key_file = ""            # défaut : <config>/provenance/ed25519.key
```

  `[provenance]` refusé dans la config projet (`config: provenance not allowed in project configuration`).
- Politique : `[provenance] required = true` (le hook écrit une erreur au journal si l'attestation est impossible, sans bloquer le commit ; `enabled` forcé à vrai) et `trusted_keys = ["ed25519:<base64>", …]` (clés acceptées à la vérification ; une clé mal formée refuse la politique).

## 5. Création

- L'installation des hooks de marquage ajoute `post-commit` et `post-rewrite` (mêmes règles : ne remplace pas un hook tiers, script court qui appelle tenon).
- `post-commit` lance `tenon provenance attest HEAD --hook` avec un délai de 5 s. En mode hook : jamais de code de sortie non nul ; une erreur donne une notice sur stderr `provenance: <raison>` ; rien n'est fait si la provenance est désactivée ou si aucun fichier n'est attribué.
- `post-rewrite` (amend, rebase) : atteste chaque nouveau commit listé sur l'entrée standard (`<ancien> <nouveau>`), en mode hook.
- `tenon provenance attest [<rev>|<A..B>] [--force]` : fichiers modifiés par rapport au premier parent (`git diff-tree --no-commit-id -r --name-status`) ; ajouts et modifications seulement (les suppressions ne sont pas attestées) ; une note existante n'est remplacée qu'avec `--force` (`provenance: <sha>: note exists (use --force)`).
- Note : l'enveloppe DSSE en JSON compact, écrite par `git notes --ref tenon-provenance add -f -F -` (copie `<data>/provenance/<sha>.dsse.json`, 0600).

## 6. Vérification

`tenon audit provenance <rev>|<A..B> [--key <pub>] [--json]` :

- clés de confiance : `--key` (fichier ou valeur `ed25519:…`), sinon les `trusted_keys` de la politique, sinon la clé publique locale ;
- par commit :
  - `verified` : signature valide par une clé de confiance, sujet `git+commit` égal au commit, chaque sujet fichier égal au blob de ce chemin dans le commit (oid et sha256) ;
  - `untrusted` : signature valide, clé inconnue ;
  - `invalid` : enveloppe mal formée, signature fausse ou sujet fichier différent du blob ;
  - `stale` : sujet `git+commit` différent du commit vérifié (note copiée) ;
  - `missing` : pas de note alors que le message du commit porte un trailer `AI-Assisted-By` ;
  - `none` : pas de note, pas de trailer ;
- sortie texte : une ligne par commit (`<sha court> <statut> <n> files (<a> agent, <m> agent-modified) key <keyid>`), puis les fichiers en `--verbose` ; `--json` : liste de résultats.
- Codes : 0 si tous les commits sont `verified` ou `none` ; 1 sinon.

## 7. Journal, export, `config check`

- Événement `ProvenanceAttested {commit, files, agent, agent_modified, keyid}`, dans une courte session `SessionStarted{kind: "provenance"}`.
- Export de conformité : section « Provenance » (commits attestés, fichiers `agent` et `agent-modified`, identifiants de clé) ; les sessions `provenance` sont exclues des statistiques de conversation.
- `tenon config check` : `provenance: <enabled|disabled> (key <keyid|absent>)`.

## 8. Architecture

- `internal/provenance` (nouveau) : `Statement`, `Envelope`, `PAE(payloadType string, payload []byte) []byte`, `Sign(stmt Statement, key ed25519.PrivateKey) (Envelope, error)`, `Verify(env Envelope, keys []ed25519.PublicKey) (Statement, string, error)` (keyid), `Build(commit CommitInfo, writes []gitmark.Write) (Statement, bool)`, `LoadKey`, `ParsePublicKey`, `KeyID`.
- `internal/gitmark` : `AgentWrites` réutilisé ; installation des hooks `post-commit` et `post-rewrite`.
- `cmd/tenon/provenance.go` : `provenance keygen|attest`, `audit provenance`, mode hook, journal.
- `internal/config`, `internal/orgpolicy` : `[provenance]`.
- `internal/event`, `internal/compliance`, `internal/i18n`, docs EN/FR (`docs/provenance.md`, `docs/fr/provenance.md`, usage, politique).

## 9. Tests

- `provenance` : vecteur PAE de la spécification DSSE ; signature puis vérification ; payload altéré, signature altérée, keyid inconnu ; `Build` : `agent`, `agent-modified`, fichier non écrit absent, plusieurs sessions pour un même hash, tri des sujets.
- Bout en bout (dépôt git temporaire, faux fournisseur) : session qui écrit deux fichiers, retouche manuelle de l'un, commit → note présente, `audit provenance HEAD` → `verified` avec un `agent` et un `agent-modified` ; `--key` d'une autre clé → `untrusted`, code 1 ; note copiée sur un autre commit → `stale` ; trailer sans note → `missing` ; amend → nouveau commit attesté par `post-rewrite` ; hook sans clé → commit réussi, notice ; provenance désactivée → aucune note ; section Provenance de l'export.

## 10. Hors périmètre

Signature sans clé (Sigstore, Rekor), SLSA Build provenance des binaires, attestation des suppressions de fichiers, attribution ligne par ligne, publication automatique des notes, révocation de clés.
