# tenon : horodatage RFC 3161 du journal d'audit (v0.7)

Date : 2026-10-21. Statut : conception validée en séance, à relire.
Spec parente : [2026-10-14-tenon-design.md](2026-10-14-tenon-design.md). Specs liées : [export de conformité](2026-10-20-tenon-audit-export-design.md), [politique d'organisation](2026-10-20-tenon-org-policy-design.md), [profils de conformité](2026-10-20-tenon-compliance-profiles-design.md).

## 1. Objectif

Le journal d'audit est une chaîne de hachage : elle détecte une modification isolée, mais pas une réécriture complète par quelqu'un qui contrôle le poste. Faire horodater le hash de tête par une autorité d'horodatage (TSA, RFC 3161) prouve que tout le préfixe de la chaîne existait à l'instant signé par la TSA. Un auditeur vérifie hors ligne.

Critères de succès :

- une chaîne réécrite après un horodatage est détectée par `tenon audit verify` et par l'export ;
- la TSA ne reçoit qu'un hash SHA-256, jamais de contenu ;
- un échec de l'horodatage ne bloque jamais une session ;
- sans `[audit.timestamp]`, le comportement actuel est inchangé (aucun appel réseau) ;
- aucune nouvelle dépendance : stdlib seulement.

## 2. Décisions

| Sujet | Décision | Raison |
|---|---|---|
| Déclencheur | Fin de session (si configuré) + `tenon audit timestamp` | Couvre chaque session sans appel réseau en cours de travail |
| Vérification | Parseur CMS/RFC 3161 minimal sur `encoding/asn1` et `crypto/x509` | Aucun module tiers ; périmètre restreint à ce que RFC 3161 exige |
| Stockage | Jetons hors chaîne, un fichier DER par jeton | Au niveau `metadata` le payload d'un événement est remplacé par son hash : un jeton dans la chaîne serait perdu |
| Configuration | Config globale ; la politique peut l'imposer ; aucune TSA par défaut | tenon ne recommande aucun prestataire ; l'org peut exiger l'horodatage |
| Profils | `strict-local` : TSA locale seulement ; autres profils : pas de restriction | Seul un hash part ; `strict-local` promet que rien ne quitte la machine |
| Révocation | Non contrôlée | Hors ligne par construction ; documenté |

## 3. Demande et stockage

- Ce qui est horodaté : le hash de tête (`Line.Hash` de la dernière ligne de la chaîne). `messageImprint` = `{sha256, 32 octets bruts du hash}` (le hash est déjà un SHA-256). Vérifiable sans tenon : `openssl ts -verify -digest <hex> -in <jeton>.tsr -token_in -CAfile <ca>`.
- Requête : `TimeStampReq{version 1, messageImprint, nonce 64 bits aléatoire, certReq true}` (le jeton embarque le certificat de la TSA, vérifiable hors ligne). POST `Content-Type: application/timestamp-query`, délai 10 s, réponse lue jusqu'à 1 Mio.
- Avant stockage : `status` granted (0) ou grantedWithMods (1), nonce identique, empreinte identique, jeton valide (§4).
- Fichier : `<datadir>/audit/timestamps/<genTime UTC AAAAMMJJTHHMMSSZ>-<12 premiers hex du hash>.tsr`, contenu = le jeton seul (ContentInfo DER), mode 0600, dossier 0700. Aucun index : tout se relit depuis les jetons. Le sous-dossier n'est pas vu par la lecture de la chaîne (fichiers `*.jsonl` à la racine seulement).
- Rien n'est écrit dans la chaîne pendant un horodatage : la tête reste celle qui est horodatée.
- Tête déjà horodatée (un jeton existant porte la même empreinte) : rien n'est demandé.
- Fin de session : si `[audit.timestamp]` est configuré, après la fermeture du writer d'audit, tenon horodate la tête. Échec : notice sur stderr `audit timestamp: <raison>`, code de sortie inchangé.
- `tenon audit timestamp` : horodate la tête courante. Codes : 0 fait ou déjà fait (`audit timestamp: already anchored`) ; 1 non configuré, journal vide, erreur réseau ou réponse invalide.

## 4. Vérification d'un jeton (`internal/tsp`)

Décodage : `TimeStampResp` → `ContentInfo` (`id-signedData`) → `SignedData` → `encapContentInfo` (`id-ct-TSTInfo`) → `TSTInfo` ; `certificates` ; `signerInfos`. Les CRL éventuelles sont ignorées.

Contrôles, tous bloquants :

1. Exactement un `SignerInfo` ; signataire désigné par issuer+serial ou par SKI, son certificat présent dans `certificates`.
2. Attributs signés présents, avec `contentType` = `id-ct-TSTInfo`, `messageDigest` = hash de l'eContent (algorithme du `SignerInfo`), et `signingCertificate` (ESSCertID, SHA-1) ou `signingCertificateV2` (ESSCertIDv2, SHA-256 par défaut) qui correspond au certificat du signataire (RFC 5816).
3. Signature sur le DER des attributs signés retagués en SET (`0x31`). Acceptés : RSA PKCS#1 v1.5 et ECDSA, avec SHA-256, SHA-384 ou SHA-512. Tout autre algorithme (SHA-1, RSA-PSS…) : `unsupported algorithm <oid>`.
4. Certificat du signataire : EKU `timeStamping` présente et seule, extension EKU critique ; chaîne vers les racines avec `x509.VerifyOptions{Roots, Intermediates: autres certificats du jeton, KeyUsages: [timeStamping], CurrentTime: genTime}` (un certificat expiré depuis reste valide).
5. `TSTInfo` : version 1, `messageImprint` en SHA-256 (32 octets). Le nonce n'est comparé qu'à la demande.

Racines : PEM de la politique si elle en fournit, sinon `ca_file`, sinon racines système ; `--tsa-ca` de `audit verify` remplace le tout.

## 5. Vérification du journal

`tenon audit verify` (étendu) :

- parcourt la chaîne comme aujourd'hui en retenant pour chaque hash sa position (fichier:ligne) et son `ts` ;
- vérifie chaque jeton de `audit/timestamps/` (§4), puis que son empreinte est un hash de la chaîne ; sinon `timestamp <fichier>: anchored hash not in chain` (chaîne réécrite) ;
- avertissement non bloquant si une ligne couverte par un jeton a un `ts` postérieur de plus de 5 min au `genTime` de ce jeton : `timestamp <fichier>: <fichier>:<ligne> ts after genTime (local clock?)` ;
- sortie : ligne actuelle, puis `timestamps: <n> valid, last <fichier>:<ligne> at <genTime RFC 3339> (<CN de la TSA>), <m> lines after` ; sans jeton : `timestamps: none` ;
- codes : 0 tout valide ; 1 chaîne cassée, jeton invalide ou orphelin ;
- option `--tsa-ca <pem>` : racines de l'auditeur.

Export de conformité :

- `Integrity.Timestamps` : `{count, valid, last_file, last_line, last_time, tsa, lines_after, errors []string}` (`errors` jamais nil) ; ligne HTML « Horodatage » / « Timestamps » ;
- jeton invalide ou orphelin : rapport écrit, code 2 (comme une chaîne cassée) ;
- l'export lit les racines comme `audit verify` (politique, `ca_file`, système).

## 6. Configuration, politique, profils

Config globale (clé refusée dans `.tenon/config.toml`, comme `mcp`) :

```toml
[audit.timestamp]
url = "https://tsa.example.eu/tsr"
ca_file = "/etc/ssl/tsa-ca.pem"   # optionnel ; défaut : racines système
```

- `url` : `https`, ou `http` pour un hôte local seulement ; sinon `config: audit.timestamp.url must be https (http only for local hosts)`.
- `ca_file` illisible ou sans certificat : `config: audit.timestamp.ca_file: <raison>`.
- `ca_file` sans `url` : erreur `config: audit.timestamp.ca_file without url`.

Politique :

```toml
[audit.timestamp]
url = "https://tsa.acme.eu/tsr"
ca = """-----BEGIN CERTIFICATE-----
...
-----END CERTIFICATE-----"""      # optionnel ; couvert par la signature
```

- Mêmes règles pour `url` ; `ca` sans certificat PEM valide : refus de la politique.
- Remplace `url` et `ca_file` utilisateur ; s'ils diffèrent : notice `policy <id>: audit timestamp imposed (<url>)`.

Profils : sous `strict-local`, hôte de TSA non local → refus au démarrage, code 1 : `profile strict-local: audit timestamp host "<h>" is not local`. Même refus pour `tenon audit timestamp`. Les autres profils et la liste blanche de fournisseurs ne concernent pas la TSA. Proxys d'environnement respectés, sauf sous `strict-local` (contournés).

`tenon config check` : `audit.timestamp: <url> (roots: <ca_file|system|policy>)` si configuré.

## 7. Architecture

- `internal/tsp` (nouveau, stdlib seulement) : `NewRequest(digest []byte) (der []byte, nonce *big.Int, err error)`, `Request(ctx context.Context, c *http.Client, url string, digest []byte) (raw []byte, info Info, err error)`, `Verify(raw []byte, roots *x509.CertPool) (Info, error)`, `type Info struct{ GenTime time.Time; Digest []byte; Serial *big.Int; TSA string }` (`TSA` = CN du signataire).
- `internal/tsp/tsptest` : fausse TSA (`httptest`, CA et signataire ECDSA générés au test) qui produit de vrais jetons, avec options d'altération (statut, nonce, empreinte, signature, EKU, ESSCertID, algorithme).
- `internal/audit/timestamp.go` : `Head(dir string) (hash string, err error)`, `Anchored(dir, hash string) (bool, error)`, `SaveToken(dir string, info tsp.Info, raw []byte) (path string, err error)`, `VerifyTimestamps(dir string, roots *x509.CertPool) (TimestampSummary, error)` (parcourt la chaîne elle-même) ; `Verify` inchangé.
- `internal/config` : `Audit.Timestamp{URL, CAFile}`, `(Config) TimestampRoots() (*x509.CertPool, string, error)` (pool, source) ; `ApplyPolicy` recopie la TSA de la politique.
- `internal/orgpolicy` : `Policy.AuditTimestamp{URL, CA}` et validation.
- `cmd/tenon` : horodatage en fin de session, `audit timestamp`, `audit verify --tsa-ca`, refus `strict-local`, notices, `config check`.
- `internal/compliance` : `Integrity.Timestamps`, gabarit HTML.
- `internal/i18n`, docs EN/FR : sécurité (modèle de menace, révocation non contrôlée, vérification openssl), usage, politique, `docs/config.example.toml`.

## 8. Tests

- `tsp` : vecteurs réels générés une fois avec `openssl ts -reply` (signataire RSA et signataire ECDSA) et leur script de génération, commités dans `testdata` ; refus : signature altérée, empreinte altérée, `messageDigest` faux, ESSCertID absent ou faux, EKU absente ou non critique, autre racine, SHA-1, RSA-PSS, statut rejected, nonce différent ; certificat expiré après `genTime` accepté.
- `audit` : `Head`, `Anchored`, jeton orphelin après réécriture complète de la chaîne (hashes recalculés), lignes après le dernier jeton, avertissement d'horloge.
- `config` / `orgpolicy` : url http distante refusée, http local accepté, `ca_file` invalide, `ca_file` sans url, clé dans la config projet refusée, PEM de politique invalide, politique qui remplace la TSA utilisateur.
- Bout en bout : session + fausse TSA → un `.tsr` créé et `audit verify` code 0 ; chaîne réécrite → code 1 ; TSA injoignable → notice, code 0 ; `strict-local` + TSA distante → code 1 ; politique qui impose la TSA ; `audit timestamp` deux fois → « already anchored » ; export avec la section et code 2 sur jeton orphelin.

## 9. Hors périmètre

Révocation (CRL, OCSP), jetons d'archivage longue durée (LTV, ré-horodatage), plusieurs TSA, préréglages de TSA, horodatage périodique en cours de session, Windows.
