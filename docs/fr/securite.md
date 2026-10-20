# Sécurité, souveraineté et limites

*Traduction française de [la version anglaise](../security.md). En cas de différence, la version anglaise fait foi.*

Ce document dit ce que tenon garantit, et surtout ce qu'il ne garantit pas.
Il décrit le comportement du code actuel.
Le mode d'emploi détaillé est dans [utilisation.md](utilisation.md).

## Souveraineté : critères retenus

La souveraineté de tenon ne repose pas sur la nationalité des fournisseurs.
Elle repose sur quatre critères vérifiables.
Un service utilisé **par défaut** pour faire fonctionner, construire ou distribuer tenon doit les remplir tous :

1. **Ouvert** : logiciel open source, ou protocole ouvert et documenté.
2. **Auto-hébergeable** : vous pouvez le remplacer par votre propre instance.
3. **Droit applicable européen** : l'opérateur n'est soumis à aucune loi extraterritoriale qui permettrait à une autorité non européenne d'accéder aux données (par exemple CLOUD Act, FISA section 702).
4. **Données maîtrisées** : aucun transfert de données hors de l'UE sans votre consentement explicite, tracé dans le journal d'audit.

Un service qui ne remplit pas ces critères reste utilisable.
Il n'est jamais utilisé par défaut, et son usage est toujours explicite et tracé.

Conséquences dans tenon :

- Les fournisseurs préconfigurés sont européens (`eu`) ou locaux (`local`). Le fournisseur par défaut est Mistral AI.
- Un fournisseur ou un serveur MCP `non-eu` exige votre accord : question dans l'interface interactive pour un fournisseur, option `--allow-non-eu` sinon.
- Les bibliothèques sont vendorées : la compilation depuis les sources ne demande aucun proxy de modules (`GOFLAGS=-mod=vendor`).

## Ce que tenon garantit

### Journal d'audit

- Chaque événement (message, appel d'outil, permission, résultat, erreur) est écrit dans `~/.local/share/tenon/audit/AAAA-MM-JJ.jsonl`, avec `fsync`.
- Chaque ligne contient l'empreinte SHA-256 de la ligne précédente. La première ligne a `prev = "sha256:0"`.
- `tenon audit verify` détecte une ligne modifiée, insérée ou supprimée au milieu du journal.
- Le contenu est masqué avant écriture. Avec `audit.level = "metadata"`, seules l'empreinte et la taille de chaque charge utile sont gardées.
- Si l'écriture échoue, tenon n'exécute plus aucune action : la conformité passe avant la continuité.

### Permissions

- Chaque appel d'outil passe par le moteur de permissions avant exécution.
- Par défaut, seule la lecture dans le projet est autorisée sans demande. Lire `.env*`, `*.pem` ou `id_rsa*`, ou accéder hors du projet, demande toujours une validation.
- Une règle `deny` l'emporte toujours. Sur un chemin, elle s'applique aussi à la forme avant résolution des liens symboliques, sans tenir compte de la casse.
- Un dépôt ne peut pas assouplir les règles : sa configuration ne peut contenir ni règle `allow`, ni fournisseur, ni serveur MCP.
- En mode non interactif, toute demande de validation devient un refus.
- Dans l'interface interactive, la validation exige un délai de calme de 500 ms, une commande entière visible et au moins 40 colonnes (voir [utilisation.md](utilisation.md#valider-une-action)).

### Masquage

- Les secrets (clés d'API, jetons, clés privées, valeurs de type `.env`) et des données personnelles (IBAN, NIR, e-mail, téléphone français) sont remplacés par des marqueurs : `[SECRET_1]`, `[EMAIL_1]`…
- Le masquage s'applique à tout ce qui part vers le fournisseur, au journal d'audit, aux sessions et aux sorties affichées.
- Les clés d'API des fournisseurs configurés et les valeurs `env_from` / `bearer_env` des serveurs MCP sont masquées à l'identique, même sans forme reconnaissable.
- Les marqueurs ne sont jamais restaurés. Un appel MCP dont les arguments contiennent un marqueur est refusé.

### Transparence

- Le modèle, le fournisseur, le pays et la juridiction sont affichés au démarrage, dans la barre d'état et dans l'événement `SessionStarted` de la sortie JSON.
- Les commits qui contiennent des fichiers écrits par l'agent reçoivent les trailers `AI-Assisted-By` et `AI-Session` (voir [utilisation.md](utilisation.md#marquage-git-du-code-produit)).

### Export de conformité

- `tenon audit export` ne fait que lire le journal : il ne le modifie ni ne le verrouille.
- Le contenu est de nouveau masqué à la construction du rapport, et les caractères de contrôle sont remplacés par des espaces.
- Le rapport HTML ne contient aucun script et ne charge aucune ressource externe ; sa Content-Security-Policy est `default-src 'none'; style-src 'unsafe-inline'`.
- Les deux fichiers sont créés en mode `0600` et ne remplacent jamais un fichier existant.
- Sans `--details`, les fichiers gardent des textes masqués tirés du journal : chemins des fichiers modifiés, identifiants de session, messages d'erreur, textes des pannes MCP et raisons des hooks. Manipulez aussi le rapport par défaut avec soin.
- Avec `--details`, les fichiers contiennent des données de conversation (prompts et commandes en entier ; réponses, arguments et résultats d'outils coupés à 500 caractères). Conservez-les et partagez-les comme le journal lui-même. Le HTML affiche un bandeau quand elles sont incluses.
- `report_id` et `html_sha256` détectent une modification accidentelle ; ils n'arrêtent pas qui réécrit les deux fichiers. Voir [utilisation.md](utilisation.md#vérifier-lintégrité-dun-rapport).

### Profils de conformité

- Avec `profile = "eu-only"` ou `"strict-local"` (configuration globale ; un projet ne peut que le durcir), le fournisseur sélectionné et les serveurs MCP doivent respecter le profil, et `--allow-non-eu` est refusé.
- Le profil est contrôlé au démarrage puis avant chaque envoi au modèle : en cas de violation, rien n'est envoyé et le tour est bloqué (événement `Error`, `code: "profile"`).
- Le profil est enregistré dans `SessionStarted` et listé dans l'export de conformité.
- Un profil borne les données envoyées au modèle et aux serveurs MCP. Il ne restreint **pas** les commandes shell ni les programmes MCP stdio, qui peuvent toujours accéder au réseau, et il ne change ni les permissions, ni le niveau d'audit, ni les hooks. Voir [utilisation.md](utilisation.md#profils-de-conformité).

## Limites connues

### Les permissions ne sont pas un bac à sable

Une commande shell autorisée s'exécute **avec vos droits**, dans le dossier du projet.
Elle peut lire, modifier ou envoyer tout ce que vous pouvez lire, modifier ou envoyer.
Une règle `allow` large (`tool = "shell"`, `match = "*"`) donne ce pouvoir au modèle sans validation.
Il en va de même pour un serveur MCP stdio : c'est un programme lancé avec vos droits.
L'isolation système n'existe pas dans cette version.

### Les skills de projet ne sont pas dignes de confiance

Un skill placé dans `.tenon/skills`, `.claude/skills` ou `.agents/skills` d'un dépôt est un contenu écrit par les auteurs du dépôt, comme `AGENTS.md`.
Son texte peut contenir des instructions destinées à tromper le modèle (injection d'instructions), et le modèle peut le charger de lui-même quand la description correspond.
tenon ne filtre pas ce texte.

Ce qu'un skill ne peut pas faire :

- Il ne peut pas élargir les permissions. `allowed-tools` est lu, affiché et journalisé, mais ne change jamais une décision.
- Ses scripts ne sont pas exécutés par le système de skills : ils passent par l'outil `shell`, avec la validation habituelle, et toute commande qu'aucune règle `allow` ne couvre demande votre validation.
- Il ne peut pas masquer un skill utilisateur : à nom égal, le skill utilisateur l'emporte et un avis signale le skill de projet comme ignoré.
- Ses fichiers sont lus avec les mêmes protections que les autres fichiers : pas de lien symbolique, pas de chemin hors du dossier du skill, pas de fichier sensible.

Ce que vous devez faire : lire les skills d'un dépôt inconnu avant d'utiliser le projet, comme vous liriez `AGENTS.md`.
`tenon skills` liste les skills trouvés par tenon, avec leur source. Une règle `deny` sur `tool = "skill"` en interdit un.
Chaque chargement figure dans le journal d'audit avec le SHA-256 du fichier lu (voir [utilisation.md](utilisation.md#skills)).

### Les sous-agents agissent avec les droits de la session

Un sous-agent s'exécute avec les mêmes permissions, le même masquage, le même accord de juridiction et le même audit que l'agent principal.
Chacune de ses actions passe par les mêmes règles et les mêmes validations. Ses actions figurent dans le même journal d'audit, dans la même chaîne.
Il n'obtient jamais plus de droits que la session, et il ne peut pas lancer de sous-agent.
Le champ `model` d'un fichier d'agent est ignoré : un sous-agent utilise le fournisseur et le modèle de la session, donc il n'ajoute aucune nouvelle juridiction.

Un fichier placé dans `.tenon/agents`, `.claude/agents` ou `.agents/agents` d'un dépôt est un contenu non fiable, comme `AGENTS.md` et les skills de projet.
Son texte devient le prompt système d'un sous-agent, et peut contenir des instructions destinées à tromper le modèle. tenon ne filtre pas ce texte.
Un agent de projet ne peut pas masquer un agent utilisateur, et `tools` ne peut que restreindre les outils de la session.
Ce que vous devez faire : lire les agents d'un dépôt inconnu avant d'utiliser le projet.
Un fichier de projet nommé `general-purpose` remplace l'agent intégré, qui est la cible par défaut de l'outil `agent` : un dépôt peut donc changer les instructions et les outils de toute délégation qui ne nomme pas d'agent.
Les approbations « oui pour la session » sont partagées entre l'agent principal et ses sous-agents : une approbation donnée pour une action d'un sous-agent vaut aussi pour l'agent principal, et inversement.
`tenon agents` liste les agents trouvés par tenon, avec leur source. Une règle `deny` sur `tool = "agent"` en interdit un (voir [utilisation.md](utilisation.md#sous-agents)).

### Les hooks exécutent votre code, avec vos droits

Un hook est un programme que vous avez écrit ou installé. Il s'exécute sous votre identité, hors des règles de permission.
Seuls vos propres fichiers sont lus (`~/.config/tenon/hooks.json`, et `~/.claude/settings.json` si vous mettez `hooks.claude_settings = true`). **Les hooks d'un projet ne s'exécutent jamais** : tenon affiche seulement un avis. Un dépôt ne peut pas vous faire exécuter de code par un hook qui lui appartient. Mais un de vos hooks qui exécute un fichier du projet (`$CLAUDE_PROJECT_DIR/.claude/hooks/x.sh`, `./scripts/...`, `npm test`) exécute du code du dépôt, avec vos droits : n'écrivez de tels hooks que pour des dépôts de confiance.

Un hook ne peut pas élargir les permissions de la session. `allow` lève seulement un `ask` issu des règles, comme le ferait une règle `allow` globale. Il ne lève jamais une règle `deny`, une action protégée, un chemin hors du projet, un fichier sensible ni une règle `ask` du projet. Un hook peut toujours restreindre : `deny`, `ask`, ou une modification de l'entrée de l'outil, qui est revalidée.

Les hooks `command` reçoivent l'entrée de l'outil **non masquée** : ils s'exécutent sur votre machine, et tenon ne contrôle pas ce qu'ils en font. Un hook qui envoie cette entrée à un service (par exemple un hook de notification qui poste vers un service hors UE) relève de votre responsabilité, comme un serveur MCP stdio. Le contrôle de juridiction ne le couvre pas.
Les hooks `prompt` et `agent`, et tout ce qu'un hook renvoie au modèle (`additionalContext`, raisons, stderr), passent par le même masquage et le même contrôle de juridiction que toute requête. Un hook `prompt` ou `agent` utilise le fournisseur et le modèle de la session (`model` est ignoré), donc il n'ajoute aucune nouvelle juridiction.

Un hook en échec ne bloque pas. Un plantage, un délai, une sortie illisible ou une erreur du fournisseur est une erreur non bloquante : l'action continue, avec un avertissement dans l'interface et dans le journal d'audit. Un hook de sécurité cassé ne vous protège donc pas. Testez vos hooks et surveillez les avertissements. Seul un fichier de hooks invalide empêche tenon de démarrer.
Chaque exécution de hook est dans le journal d'audit (voir [utilisation.md](utilisation.md#hooks)). `tenon hooks` liste les hooks chargés par tenon.

### Les points de restauration gardent le contenu brut de vos fichiers

Avant que `write_file` et `edit_file` écrasent un fichier, tenon en garde une copie pour que `/rewind` puisse le restaurer. La copie est le **contenu brut**, secrets compris (`.env`, clés, jetons) : la restauration exige le fichier exact.
Les copies sont dans `~/.local/share/tenon/checkpoints/`, hors du projet, donc elles ne peuvent pas être commitées par erreur. Les dossiers sont en `0700` et les fichiers en `0600`. Elles ne sont jamais envoyées au fournisseur et jamais écrites dans la session ni dans le journal d'audit, qui ne contiennent que les empreintes. Le masquage ne s'y applique pas.
Quiconque peut lire votre dossier de données peut lire ces copies. Elles sont gardées `retention_days` jours (30 par défaut). Pour toutes les supprimer maintenant : `rm -rf ~/.local/share/tenon/checkpoints`. `retention_days = 0` arrête leur enregistrement.
Un rewind n'écrit que des chemins que les outils ont écrits dans la session. Il ne suit jamais un lien symbolique dans le dernier composant d'un chemin : un chemin devenu lien est un conflit, et n'est pas écrit. Un fichier que vous avez modifié après l'agent n'est pas écrasé sans votre réponse explicite. Chaque copie est vérifiée avec son sha256 avant d'être restaurée. `write_file` et `edit_file` n'écrasent jamais un fichier existant qu'ils ne peuvent pas lire.
Un rewind est une action de l'utilisateur : aucun outil et aucun hook ne peut le déclencher. Il n'annule pas les commandes `shell` ni les outils MCP (voir [utilisation.md](utilisation.md#points-de-restauration)).

### La juridiction est déclarative

tenon ne vérifie pas la juridiction d'un fournisseur ou d'un serveur.
Il applique la valeur écrite dans la configuration.
Une erreur de déclaration (`eu` pour un opérateur soumis au CLOUD Act) supprime la protection.
Par prudence, un fournisseur ajouté sans `jurisdiction` est traité comme `non-eu`.

### Serveur stdio `local`

Un serveur MCP stdio vaut `local` par défaut.
Cela décrit l'endroit où tourne le programme, pas l'endroit où il envoie ensuite vos données.
Si le programme relaie un service hors de l'UE, déclarez-le `jurisdiction = "non-eu"`.

### Le masquage est approximatif

- La détection repose sur des expressions régulières. Elle oublie des secrets au format inconnu et masque parfois du texte ordinaire.
- La détection des données personnelles est partielle. tenon ne garantit pas la conformité au RGPD.
- La réponse du modèle arrive par fragments (`AssistantDelta`). Chaque fragment est masqué séparément : un secret coupé entre deux fragments peut apparaître en clair dans les fragments du journal d'audit et de la sortie `--output json`. Le message final complet (`AssistantMessage`) est masqué d'un seul tenant.
- Une clé d'API ou une valeur `env_from` / `bearer_env` de moins de 8 caractères n'est pas masquée telle quelle, pour ne pas masquer du texte ordinaire.

Ajoutez vos propres règles avec `[[redaction.rules]]`.

### Troncature de fin du journal

Supprimer les **dernières** lignes du journal ne casse pas la chaîne : `tenon audit verify` ne peut pas le détecter.
Parade : notez régulièrement le dernier hash affiché par `tenon audit verify` hors de la machine (ticket, dépôt séparé, message signé).
Plus tard, vérifiez que ce hash figure toujours dans le journal, par exemple avec `grep`.
S'il a disparu, des lignes ont été retirées.

### Sosies visuels et terminal

Le panneau de validation rend visibles les caractères invisibles, de largeur nulle, bidirectionnels et les espaces inhabituelles (`[U+XXXX]`).
Il ne peut pas tout montrer :

- les lettres sosies (cyrillique `а` au lieu de latin `a`, faux guillemets, faux tirets) restent affichées telles quelles. Un avertissement signale seulement qu'une commande shell contient un caractère non ASCII ;
- un domaine sosie dans une URL n'est pas détecté ;
- certains terminaux réordonnent le texte bidirectionnel ou dessinent mal les marques combinantes : le rendu dépend aussi de votre terminal et de sa police.

Lisez la commande. En cas de doute, refusez.

### Marquage git

- Le marquage repose sur les fichiers de session. Une session supprimée n'est plus prise en compte.
- La correspondance se fait par chemin : un fichier écrit une fois par l'agent est marqué à chaque commit où il est indexé, même si un humain l'a entièrement réécrit depuis.
- `git commit --no-verify`, un hook absent ou `tenon` absent du `PATH` donnent un commit non marqué. Les commits de fusion ne sont pas marqués.

Le marquage est une aide à la traçabilité, pas une preuve.

### Outils de build et réseau

Compiler tenon depuis les sources ne contacte aucun service : les dépendances sont vendorées.
La chaîne de développement (`make lint`, `make vuln`, `make release`, CI) fait exception :

- les outils de build épinglés (staticcheck et govulncheck, installés dans `.tools/` par `scripts/go-tool.sh` ; cyclonedx-gomod et nfpm, lancés par `go run <module>@<version>`) sont téléchargés depuis leurs hôtes d'origine (`GOPROXY=direct`) : `honnef.co` (qui renvoie vers `github.com`), `golang.org` et `go.googlesource.com` (opéré par Google), `github.com` (opéré par Microsoft) ;
- leurs empreintes sont vérifiées auprès de `sum.golang.org`, opéré par Google ;
- `govulncheck` interroge la base de vulnérabilités `vuln.go.dev`, opérée par Google ;
- le dépôt et la CI GitHub Actions sont hébergés par GitHub (Microsoft, États-Unis) ; les fichiers `.forgejo/` et `.woodpecker.yml` permettent une CI sur une forge européenne ou auto-hébergée ;
- la CI (`.github/workflows/ci.yml`, `.forgejo/workflows/ci.yml`, `.woodpecker.yml`) télécharge ses images depuis Docker Hub (Docker Inc., États-Unis) : `golang` et, pour Woodpecker, `woodpeckerci/plugin-git`. Ces images sont épinglées à une version ; un miroir de registre européen peut les remplacer.

Ces services ne reçoivent aucune donnée de vos sessions.
Avec la configuration par défaut de Go (sans `GOPROXY=direct`), les modules passent aussi par `proxy.golang.org`, opéré par Google.

## Récupération

### Journal d'audit corrompu

Symptômes : `tenon audit verify` signale `fichier:ligne`, ou tenon refuse de démarrer avec « last log line unreadable, chain not resumed » (dernière ligne du journal illisible, chaîne non reprise).

Ne modifiez pas le journal en place : il sert de preuve.

1. Quittez toutes les instances de tenon.
2. Mettez le journal de côté, sans le modifier :
   ```
   mkdir -p ~/tenon-audit-2026-10-15/tenon
   mv ~/.local/share/tenon/audit ~/tenon-audit-2026-10-15/tenon/audit
   ```
3. Vérifiez la copie pour noter l'endroit exact de la faute :
   ```
   XDG_DATA_HOME=~/tenon-audit-2026-10-15 tenon audit verify
   ```
4. Relancez tenon : un nouveau journal commence avec `prev = "sha256:0"`.
5. Consignez l'incident (date, faute signalée, dernier hash connu) à côté de la copie.

Une dernière ligne coupée (arrêt brutal pendant une écriture) produit le même symptôme.
La procédure reste la même.

### Session corrompue

À la reprise (`tenon --resume`), tenon lit la session jusqu'à la dernière ligne lisible et affiche un avertissement.
Le fichier est coupé à la première ligne illisible.
L'original complet est gardé dans `~/.local/share/tenon/sessions/<id>.jsonl.corrupt`.

Si la reprise échoue malgré tout, démarrez une nouvelle session.
Le contenu de l'ancienne reste lisible dans son fichier JSONL et dans le journal d'audit.

Une session « déjà ouverte dans un autre terminal » est verrouillée par un autre processus tenon.
Fermez-le avant de reprendre la session.
