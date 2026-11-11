---
date: 2026-11-11
slug: le-modele-propose-le-harness-dispose
description: Une lecture mathématique de tenon. Le modèle de langage est un oracle non fiable ; chaque garantie réside dans le harness qui filtre ses actions. Systèmes de transitions, ordres de décision, étiquettes de flux d'information, chaînes de hachage, hystérésis et builds reproductibles.
---

# Le modèle propose, le harness dispose

*Une théorie des invariants de tenon.*

## Résumé

On décrit d'ordinaire un agent de code par ce que son modèle sait faire. Cet essai décrit tenon par ce que son modèle **ne peut pas** faire. Le modèle de langage y est traité comme un oracle non fiable : une fonction qui peut renvoyer n'importe quelle suite d'appels d'outils, y compris hostile. Toute garantie offerte par tenon doit alors être une propriété du *harness*, le programme déterministe placé entre l'oracle et le monde. Nous modélisons le harness comme un système de transitions gardées et en énonçons précisément les invariants : un ordre total sur les décisions et un résultat de monotonie pour la politique d'organisation ; une étiquette de flux d'information qui ne peut que monter ; une application de masquage cohérente dont les marqueurs ne peuvent jamais être réécrits ; une chaîne de hachage dont la falsification se ramène à une collision de SHA-256 ; une boucle d'hystérésis qui borne le contexte envoyé au modèle ; une borne de terminaison pour chaque tour ; et une chaîne de livraison où un binaire est une fonction déterministe de ses sources. Chaque énoncé est vérifié contre le code qu'il décrit, avec ses références de fichiers. Nous terminons par ce qui n'est *pas* démontré.

<!-- more -->

## Sommaire

1. [Le harness comme système de transitions](#1-le-harness-comme-système-de-transitions)
2. [Décisions : un ordre, pas un vote](#2-décisions--un-ordre-pas-un-vote)
3. [La configuration comme demi-treillis](#3-la-configuration-comme-demi-treillis)
4. [La souveraineté comme flux d'information](#4-la-souveraineté-comme-flux-dinformation)
5. [Le masquage : une application cohérente et à sens unique](#5-le-masquage--une-application-cohérente-et-à-sens-unique)
6. [L'isolation : réduire l'ensemble atteignable](#6-lisolation--réduire-lensemble-atteignable)
7. [La chaîne d'audit](#7-la-chaîne-daudit)
8. [Le contexte comme ressource : hystérésis et compaction](#8-le-contexte-comme-ressource--hystérésis-et-compaction)
9. [Terminaison et retour arrière](#9-terminaison-et-retour-arrière)
10. [La chaîne de livraison comme fonction](#10-la-chaîne-de-livraison-comme-fonction)
11. [Ce qui n'est pas démontré](#11-ce-qui-nest-pas-démontré)

Références · Notes

## Prélude

« L'homme propose, Dieu dispose » est une phrase sur celui qui a le dernier mot. Dans un agent, c'est le modèle qui rédige la proposition : *lire ce fichier*, *lancer cette commande*, *envoyer ce contexte*. Autre chose décide si la proposition devient un effet. Dans tenon, cette autre chose est une quarantaine de paquets Go sous `internal/`, et la question intéressante n'est pas l'intelligence du modèle mais ce qui reste vrai **quoi que** dise le modèle.

La question est ancienne. Les principes de conception de Saltzer et Schroeder[^ss] (valeurs par défaut sûres, médiation complète, moindre privilège) sont exactement ceux d'un harness : chaque action passe par un point de contrôle unique, la réponse par défaut est non, et chaque outil ne reçoit que les droits dont il a besoin. Ce qui suit tente d'écrire ces principes pour un programme concret, sous forme de définitions et de propositions plutôt que de slogans.

## 1. Le harness comme système de transitions

!!! definition "Définition 1 (État)"
    Un état de session est un n-uplet

    $$
    \sigma = (H,\; \Phi,\; A,\; \ell,\; u)
    $$

    où $H$ est l'historique de la conversation (une liste de messages, avec le prompt système $H_0$), $\Phi$ le système de fichiers vu depuis la racine du projet, $A = (a_1,\dots,a_n)$ la chaîne d'audit, $\ell \in \{0,1\}$ l'étiquette de sensibilité de la session, et $u \in \mathbb{N}$ le nombre de tokens consommés.

Le modèle est une fonction $\mathcal{M}$ qui, à une *vue* de l'historique, associe un message pouvant contenir des appels d'outils $c_1,\dots,c_k$. Nous ne faisons aucune hypothèse sur $\mathcal{M}$ : il peut se tromper, être manipulé par une injection de prompt cachée dans un fichier qu'il a lu, ou simplement mal tomber. Le harness est la fonction de transition

$$
\delta(\sigma, c) =
\begin{cases}
\sigma' & \text{si } \mathrm{garde}(\sigma, c) \text{ est vraie et l'outil s'exécute,}\\
\sigma \oplus \mathrm{refus}(c) & \text{sinon,}
\end{cases}
$$

où $\oplus$ ajoute un message d'outil expliquant le refus, et où *chaque* branche ajoute un enregistrement d'audit.

La garde n'est pas un test unique mais une chaîne de traitements, exécutée pour chaque appel dans l'ordre où le modèle les a émis, séquentiellement, jamais en parallèle (`internal/engine/loop.go`) :

1. **contrôle des arguments** : les outils d'écriture et les outils MCP refusent les arguments qui contiennent un marqueur de masquage (§5) ;
2. **hooks avant outil**, qui peuvent bloquer, demander ou réécrire les arguments (les arguments réécrits sont revalidés) ;
3. **décision de permission** $d(c) \in \mathcal{D}$ (§2) ;
4. **validation humaine**, si $d(c)$ l'exige ;
5. **contrôle de classification** sur les arguments finaux (§4) ;
6. **exécution**, confinée par l'isolation système (§6).

L'intérêt de cette écriture est la forme de l'argument de sûreté. Une *propriété de sûreté* au sens de Lamport[^lamport] est un ensemble d'exécutions clos par préfixe : « rien de mauvais n'arrive jamais ». Pour une telle propriété, il suffit de montrer que $\delta$ préserve un invariant $I$ :

$$
I(\sigma) \;\wedge\; \sigma \xrightarrow{c} \sigma' \;\Longrightarrow\; I(\sigma') \qquad \text{pour tout } c.
$$

Le quantificateur porte sur **tout** appel, et c'est précisément ce qui permet d'ignorer $\mathcal{M}$. Chaque section ci-dessous nomme un tel $I$.

## 2. Décisions : un ordre, pas un vote

Une règle est un n-uplet $r = (\text{outil}, \text{motif}, \text{décision}, \text{projet}, \text{réseau})$, où le motif est un joker dans lequel `*` reconnaît n'importe quelle sous-chaîne. Les règles viennent de trois endroits : la configuration globale de l'utilisateur, le fichier `.tenon/config.toml` du projet, et la politique d'organisation signée.

!!! definition "Définition 2 (Ordre des décisions)"
    Soit

    $$
    \mathcal{D} = \{\,\mathsf{allow} \prec \mathsf{ask}^{\uparrow} \prec \mathsf{ask} \prec \mathsf{deny}\,\}
    $$

    où $\mathsf{ask}^{\uparrow}$ est une question *levable* (un hook peut y répondre) et $\mathsf{ask}$ une question à laquelle seul un humain peut répondre.

La fonction de décision (`DecideNet`, `internal/policy/permissions.go`) n'est pas un vote à la majorité. C'est une cascade de gardes, dont la première qui se déclenche l'emporte :

$$
d(c) =
\begin{cases}
\mathsf{deny} & \exists\, r \in R_{\mathsf{deny}} : r \models c \\
\mathsf{ask} & c \text{ est imposé (serveur MCP hors UE)} \\
\mathsf{ask} & \exists\, p \in \mathrm{chemins}(c) : p \notin \mathrm{racine} \;\vee\; \mathrm{sensible}(p) \\
\mathsf{ask} & \exists\, r \in R_{\mathsf{ask}}^{\mathrm{proj}} : r \models c \\
\mathsf{allow} & \exists\, r \in R_{\mathsf{allow}} : r \models_{\forall} c \\
\mathsf{ask}^{\uparrow} & \exists\, r \in R_{\mathsf{ask}} : r \models c \\
\mathsf{allow} & \mathrm{risque}(c) = \mathrm{Read} \\
\mathsf{ask}^{\uparrow} & \text{sinon.}
\end{cases}
$$

Trois asymétries y sont codées, et méritent d'être énoncées.

*La correspondance est asymétrique.* Une règle deny ou ask correspond si **un** chemin de l'appel correspond ($\models$) ; une règle allow doit correspondre à **tous** ($\models_\forall$). Deny est en outre comparée sans tenir compte de la casse et contre toutes les formes du chemin (lexicale, résolue à travers les liens symboliques, brute), pour qu'un lien ne puisse pas masquer une cible interdite. Allow n'est comparée qu'à la forme résolue.

*Allow ne se compose pas avec le shell.* Un motif allow sur une commande ne couvre jamais une commande contenant un métacaractère du shell (`;`, `&`, `|`, un accent grave, `$(`, `>`, `<` ou un saut de ligne) absent du motif lui-même (`chainsBeyond`). Sans cela, `allow "git status*"` autoriserait `git status; curl …`. Le joker est un langage ; ce contrôle garantit que le langage d'une règle allow n'est pas silencieusement clos par composition séquentielle.

*Le défaut dépend du risque.* Chaque outil a une classe de risque dans $\{\mathrm{Read}, \mathrm{Write}, \mathrm{Exec}\}$. Lire dans le projet est autorisé par défaut ; écrire et exécuter sont demandés par défaut. C'est la *valeur par défaut sûre* de Saltzer et Schroeder, prise au pied de la lettre.

La validation est elle aussi une fonction. Un *allow* venu d'un hook ne lève que $\mathsf{ask}^{\uparrow}$, jamais $\mathsf{ask}$ ni $\mathsf{deny}$ ; un *ask* venu d'un hook force une question, même par-dessus $\mathsf{allow}$. Le « oui pour la session » d'un humain est mémorisé sous la clé $(\text{outil}, \text{commande}, \text{chemins})$, mais **jamais** pour l'accès réseau, qui doit être accordé à nouveau à chaque fois.

## 3. La configuration comme demi-treillis

La politique d'organisation est conçue comme un *plancher* : quelque chose qu'un utilisateur ne peut pas assouplir. « Ne peut pas assouplir » est un énoncé de monotonie, et il se démontre.

Le fichier de politique est vérifié avant tout le reste : il doit être signé avec minisign (Ed25519[^ed25519]) et, dans un répertoire système, chaque fichier doit appartenir à root et n'être inscriptible ni par le groupe ni par les autres ; sinon tenon refuse de démarrer. Ses règles ne peuvent être que `deny` ou `ask` (`internal/orgpolicy/policy.go` rejette les règles `allow`), et elles sont insérées **avant** toutes les autres, avec le drapeau projet.

!!! proposition "Proposition 1 (La politique est un plancher)"
    Soit $P$ un ensemble de règles de politique, toutes dans $\{\mathsf{deny}, \mathsf{ask}\}$ et marquées *projet*, et soit $U$ un ensemble quelconque de règles utilisateur, allow comprises. Pour tout appel $c$, si une règle de $P$ correspond à $c$, alors

    $$
    d(P \cup U,\; c) \;\succeq\; \mathsf{ask}.
    $$

!!! proof "Démonstration"
    Si une règle deny de $P$ correspond, le premier cas de la cascade se déclenche et le résultat est $\mathsf{deny}$, quel que soit $U$. Sinon, une règle ask de $P$ correspond. Tous les cas précédant le quatrième renvoient $\mathsf{deny}$ ou $\mathsf{ask}$. Le quatrième, les ask du projet, se déclenche sur cette règle de $P$ et renvoie $\mathsf{ask}$. Le cas allow n'arrive qu'en cinquième position : aucune règle de $U$ n'est donc jamais consultée pour un allow. Dans toutes les branches, le résultat est au moins $\mathsf{ask}$.

La même idée s'étend à toute la configuration. Plaçons chaque réglage dans une chaîne finie ordonnée par sévérité : les profils de conformité

$$
\texttt{none} < \texttt{eu-only} < \texttt{secnumcloud} < \texttt{strict-local},
$$

les modes d'isolation $\texttt{off} < \texttt{auto} < \texttt{required}$, un budget de tokens où *plus petit est plus strict*, la mémoire activée ou non, etc. Une configuration est un point du produit $\mathcal{C} = \prod_i C_i$ de ces chaînes, ordonné composante par composante. Un produit de chaînes est un treillis, et tenon fusionne le fichier global $g$, le fichier du projet $p$ et la politique $\pi$ par la borne supérieure :

$$
\mathrm{cfg} = g \sqcup p \sqcup \pi.
$$

Dans le code, c'est `Stricter` pour les profils, « le plus petit non nul » pour les budgets, « le plus strict de tous » pour l'isolation, et une *concaténation* pour les chemins interdits en lecture, les règles de masquage et les marqueurs de classification (des ensembles ordonnés par inclusion). Le fichier du projet est en outre restreint syntaxiquement : il ne peut contenir ni règle allow, ni autorisation réseau, ni isolation `off`, ni chemin inscriptible supplémentaire.

!!! proposition "Proposition 2 (La fusion n'assouplit jamais)"
    $\mathrm{cfg} \succeq g$, $\mathrm{cfg} \succeq p$ et $\mathrm{cfg} \succeq \pi$. En particulier, un dépôt cloné peut rendre tenon plus strict sur votre machine, jamais plus permissif ; et un utilisateur peut être plus strict que son organisation, jamais moins.

Cela découle de la définition d'une borne supérieure. Son intérêt pratique est qu'il n'est pas nécessaire de raisonner sur l'*ordre* de chargement des fichiers : $\sqcup$ est commutative et associative.

## 4. La souveraineté comme flux d'information

La résidence des données est un énoncé sur l'endroit où l'information a le droit d'aller, et le flux d'information a une théorie mathématique depuis le modèle en treillis de Denning[^denning] : donner à chaque objet une classe de sécurité, à chaque canal une habilitation, et n'autoriser un flux que vers le haut.

tenon utilise la plus petite instance non triviale de ce modèle : un treillis à deux points $\{0 \sqsubset 1\}$ pour l'étiquette de session $\ell$, et une juridiction pour chaque fournisseur :

$$
j(p) \in \{\texttt{local},\; \texttt{eu},\; \texttt{non-eu}\}.
$$

!!! definition "Définition 3 (Classification)"
    Un chemin de fichier correspondant à un motif classifié configuré, ou un texte contenant un marqueur tel que `DIFFUSION RESTREINTE`, `SPÉCIAL FRANCE`, `RESTREINT UE` ou `EU RESTRICTED` (comparé après normalisation de la casse, des accents et des espaces, sur des frontières de mots), est *classifié*. Toucher un objet classifié fait passer $\ell \leftarrow 1$.

Deux invariants découlent de `internal/engine/classify.go` et `internal/routing`.

!!! proposition "Proposition 3 (L'étiquette ne fait que monter)"
    Pour toute transition $\sigma \to \sigma'$, $\ell' \ge \ell$.

Aucun chemin de code ne remet $\ell$ à zéro au cours d'une session ; l'événement `SessionClassified` n'est émis qu'une fois. C'est la *marque de crue* (high-water mark) de Denning, et sa monotonie fait sa sûreté : une session ne peut pas « oublier » qu'elle a vu un document classifié pour ensuite parler à un modèle distant.

!!! proposition "Proposition 4 (Aucune fuite vers le haut)"
    Toute requête envoyée à un fournisseur $p$ vérifie

    $$
    \ell = 1 \;\Longrightarrow\; j(p) = \texttt{local}.
    $$

Le routeur calcule sa liste de candidats à partir de $\ell$ : une session sensible ne reçoit **que** le fournisseur local configuré, et un candidat déclaré local dont la juridiction n'est pas `local` est écarté. S'il n'existe aucun fournisseur local, le harness refuse au lieu de se dégrader : les appels sur des chemins classifiés sont refusés, les résultats d'outils marqués sont remplacés par `[classified content withheld: no local provider]`, et un prompt marqué est bloqué. En seconde ligne, avant chaque envoi vers un fournisseur non local, la requête brute et la requête masquée sont toutes deux analysées à la recherche de marqueurs.

Le reste de la résidence est une conjonction de prédicats évaluée avant **chaque** envoi (`Outbound.Check`) :

$$
\mathrm{envoi}(p) \iff \mathrm{profil}(p) \wedge \mathrm{hôtesPolitique}(p) \wedge \bigwedge_{k} \mathrm{listeBlanche}_k(p) \wedge \big(j(p) \in \{\texttt{eu},\texttt{local}\} \vee \mathrm{accord}(p)\big).
$$

L'ordre compte : l'accord n'apparaît que dans le dernier terme, si bien que le « oui, envoyez hors UE » d'un utilisateur ne peut jamais l'emporter sur un profil, une politique ou une liste blanche. Et une juridiction non déclarée vaut `non-eu` par défaut : l'inconnu est traité comme le pire cas.

Le routage a une dernière propriété, moins visible. Quand le premier fournisseur échoue, tenon peut basculer sur le candidat suivant, mais seulement **avant le premier fragment utile** de la réponse, et seulement pour des erreurs classées comme réessayables (aucun premier fragment en 60 s, connexion refusée, service indisponible). D'où :

!!! proposition "Proposition 5 (Aucune réponse recousue)"
    Chaque message de l'assistant dans $H$ a été produit par un seul fournisseur.

Une réponse n'est jamais la concaténation d'un préfixe venu d'un modèle et d'un suffixe venu d'un autre, ce qui rendrait vide de sens l'enregistrement d'audit « quel modèle a dit cela ».

## 5. Le masquage : une application cohérente et à sens unique

Avant que quoi que ce soit ne quitte la machine (requêtes au modèle, arguments d'outils, lignes d'audit, fichiers de session, sortie sans interface), il passe par un masqueur $\mu$ (`internal/policy/redact.go`). Ses règles reconnaissent les clés privées, les jetons de cloud et de forge, les JWT, les mots de passe dans les URL, les affectations de la forme `api_key = …` dont la valeur fait au moins 12 caractères, et certaines données personnelles : adresses e-mail, numéros de téléphone français, IBAN et numéros de sécurité sociale (NIR).

Pour les deux derniers, la reconnaissance n'est pas une simple expression régulière mais une clé de contrôle, ce qui rend les faux positifs rares. Un IBAN est accepté quand ses chiffres réarrangés vérifient l'ISO 7064 :

$$
\mathrm{IBAN} \equiv 1 \pmod{97},
$$

et un NIR $n$ (13 chiffres, avec `2A` et `2B` pour la Corse ramenés à 19 et 18) porte la clé

$$
k = 97 - (n \bmod 97).
$$

!!! definition "Définition 4 (Application des marqueurs)"
    Le masqueur a un état propre à la session. Pour une valeur reconnue $v$ de type $T$, il renvoie un marqueur $\mu(v) = \texttt{[}T\texttt{\_}i\texttt{]}$, où $i$ est un compteur par type, et il mémorise la paire.

L'application est **cohérente** : $v = v' \Rightarrow \mu(v) = \mu(v')$, si bien que le modèle peut encore raisonner sur « la même clé utilisée deux fois ». Elle est **injective** sur les valeurs reconnues : deux secrets différents reçoivent deux marqueurs différents. Et elle est **à sens unique** : il n'existe d'inverse nulle part dans le code. Les marqueurs ne sont jamais restaurés, pas même dans les fichiers de session de l'utilisateur.

Le sens unique crée un risque : le modèle, voyant `[EMAIL_1]`, pourrait l'écrire dans un fichier et corrompre le projet. Le harness ferme cette boucle.

!!! proposition "Proposition 6 (Les marqueurs ne sont jamais réécrits)"
    Aucun outil d'écriture ni aucun outil MCP ne s'exécute avec un argument qui contient une occurrence du langage des marqueurs $L_\mu$, l'expression régulière `\[[A-Z_]+_\d+\]`, y compris dans les chaînes JSON imbriquées dans les arguments.

Ce contrôle passe en premier dans la chaîne de garde, avant les hooks et les permissions : il tient donc même pour des appels qu'un humain aurait validés.

## 6. L'isolation : réduire l'ensemble atteignable

Les permissions décident *si* une commande s'exécute. L'isolation décide de ce qu'elle peut toucher *une fois lancée*, et elle ne se fie pas à ce que la commande dit d'elle-même. Sur macOS, tenon génère un profil Seatbelt pour `sandbox-exec` ; sur Linux, il se relance lui-même sous Landlock, avec un espace de noms utilisateur et réseau quand le noyau le permet.

Soit $W$ l'ensemble inscriptible : la racine du projet, les répertoires temporaires, quelques périphériques comme `/dev/null`, et les caches de l'utilisateur. Soit $R^{-}$ l'ensemble interdit en lecture : `~/.ssh`, `~/.gnupg`, `~/.aws`, `~/.kube`, `~/.docker`, `~/.netrc`, les trousseaux macOS, et les répertoires de configuration, de données et de politique de tenon lui-même. Pour un processus confiné $q$ et tout fichier $f$ :

$$
\mathrm{écrit}_q(f) \Rightarrow f \in W, \qquad \mathrm{lit}_q(f) \Rightarrow f \notin R^{-}, \qquad \mathrm{réseau}_q = \varnothing \text{ sauf autorisation.}
$$

La troisième clause demande de la précision, car elle dépend du noyau. Sur macOS, c'est `(deny network*)`. Sur Linux, c'est un espace de noms réseau vide (boucle locale seulement) quand les espaces de noms utilisateur sont disponibles ; sinon, avec un ABI Landlock $\ge 4$, `bind` et `connect` TCP sont refusés mais **l'UDP reste ouvert**. tenon indique le niveau obtenu, et `[sandbox] mode = "required"` transforme « impossible d'isoler » en « refus d'exécuter ». Le profil `strict-local` impose ce mode.

Deux autres détails sont des invariants à part entière. L'environnement de la commande est celui de tenon **moins** toutes les variables qui portent une clé de fournisseur : une commande confinée ne peut pas lire la clé qui paie le modèle. Et l'accès réseau est accordé commande par commande, par une règle globale sur l'outil `shell` ou par une réponse explicite « autoriser avec réseau », jamais mémorisé pour la session.

## 7. La chaîne d'audit

Le journal d'audit est une suite de lignes JSON dans `<data dir>/tenon/audit/YYYY-MM-DD.jsonl`, une par événement, chacune avec les champs `seq, ts, session, type, payload, prev, hash` dans cet ordre fixe. Le payload est masqué avant écriture.

!!! definition "Définition 5 (Chaîne)"
    Soit $J(a)$ le JSON canonique d'une ligne privée de son champ `hash`, et $\Vert$ la concaténation. Avec $h_0 = \texttt{"sha256:0"}$,

    $$
    h_i = \mathrm{SHA256}\big(h_{i-1} \,\Vert\, J(a_i)\big), \qquad \mathtt{prev}(a_i) = h_{i-1}.
    $$

(Le hash précédent apparaît deux fois : en préfixe, et dans $J(a_i)$ via `prev`. C'est redondant, et sans danger.) La chaîne traverse les sessions et les jours, dans l'ordre des noms de fichiers.

La vérification (`tenon audit verify`) recalcule la chaîne et contrôle, pour chaque ligne, qu'elle se lit, que `prev` est égal au hash précédent, que le hash recalculé est égal à `hash`, et que la resérialisation de la ligne redonne **exactement les mêmes octets**. Ce dernier contrôle compte : sans forme canonique, deux suites d'octets différentes pourraient porter le même contenu une fois lues, et le hash protégerait la lecture plutôt que le fichier.

!!! proposition "Proposition 7 (Falsification détectable)"
    Soit $a_1..a_n$ une chaîne vérifiée de tête $h_n$. Produire une suite différente $a'_1..a'_m$, de même longueur et différant sur au moins une ligne, qui se vérifie avec la même tête $h'_m = h_n$, exige une collision de SHA-256.

!!! proof "Esquisse de démonstration"
    Soit $i$ le dernier indice où les deux suites diffèrent, de sorte que $a_k = a'_k$ pour $k > i$. En remontant depuis la tête, l'égalité des têtes et des lignes après $i$ impose $h_i = h'_i$. Alors $\mathrm{SHA256}(h_{i-1} \Vert J(a_i)) = \mathrm{SHA256}(h'_{i-1} \Vert J(a'_i))$ avec $a_i \ne a'_i$. Par canonicité $J(a_i) \ne J(a'_i)$ : les deux entrées diffèrent, c'est une collision.

À quel point est-ce improbable ? Pour un hash idéal de 256 bits, la borne des anniversaires majore la probabilité d'une collision *quelconque* parmi $q$ évaluations par

$$
p \;\le\; \frac{q(q-1)}{2^{257}} \;\approx\; \frac{q^2}{2^{257}}.
$$

Même avec $q = 2^{64}$ hashs, bien plus qu'aucun journal n'en contiendra jamais, $p \approx 2^{-129}$.

La proposition 7 ne dit rien de la **suppression de la fin** : retirer $a_{k+1}..a_n$ laisse une chaîne parfaitement valide de tête $h_k$. Une chaîne de hachage prouve l'intégrité *relativement à une tête connue*. C'est la limite classique des journaux sécurisés[^sk], et tenon y répond de la manière classique, par l'horodatage à la Haber et Stornetta[^hs] : les 32 octets du hash de tête sont envoyés à une autorité d'horodatage RFC 3161[^rfc3161], avec un nonce aléatoire de 64 bits, et le jeton signé est conservé. `tenon audit verify` vérifie ensuite chaque jeton contre ses racines et exige que le condensat ancré **soit un hash de la chaîne**. Un jeton ancre le préfixe jusqu'à son condensat à un instant $t$ ; toute suppression avant ce point devient détectable, et le vérificateur indique combien de lignes suivent la dernière ancre.

Le journal est aussi **fermé par défaut** (fail-closed). Une écriture prend un verrou exclusif, relit la tête sur le disque, ajoute la ligne et appelle `fsync` ; en cas d'échec, elle tronque à la taille précédente et l'écrivain refuse toute écriture ultérieure. Le moteur écrit dans le journal d'audit *avant* le fichier de session, et après un échec il refuse d'émettre quoi que ce soit.

!!! proposition "Proposition 8 (Aucun effet sans trace)"
    Toute exécution d'outil est précédée d'un enregistrement `ToolCallRequested` écrit durablement. Si le journal d'audit ne peut plus être écrit, le moteur refuse tout événement ultérieur et tout nouveau tour : aucune action supplémentaire n'est exécutée.

Un outil ne s'exécute qu'après que `runTool` a émis sa demande, et une erreur d'émission termine le tour avant d'atteindre `decideAndRun`. Avec l'écrivain fermé par défaut, le journal peut *s'arrêter tôt*, mais il n'*omet* jamais une action exécutée.

Notons ce qui n'est **pas** affirmé : les lignes d'audit ne sont pas signées, si bien que quiconque peut écrire dans le répertoire de données peut reconstruire de zéro une chaîne cohérente. Ce sont les jetons d'horodatage, portés par la signature d'un tiers, qui transforment « cohérent » en « cohérent avec ce qui existait à l'instant $t$ ».

## 8. Le contexte comme ressource : hystérésis et compaction

Le modèle voit une *vue* $V(H)$ de l'historique, reconstruite à chaque envoi. tenon estime la taille d'une vue en tokens par

$$
\tau(V) = \left\lfloor \frac{\#\mathrm{runes}(V)}{4} \right\rfloor,
$$

en comptant les messages, les arguments des appels d'outils et les schémas d'outils. Soit $w$ la fenêtre de contexte du modèle. Deux mécanismes tiennent $\tau$ en respect.

**Masquage avec hystérésis.** D'abord, les résultats d'outils identiques (même outil, mêmes arguments, même SHA-256 de sortie) sont dédupliqués : seul le plus récent est conservé. Ensuite, si

$$
\tau(V) > 0{,}5\, w,
$$

les plus anciens résultats d'outils sont remplacés par un court résumé (outil, arguments, nombre de lignes, un hash de 12 chiffres hexadécimaux, et une invitation à relancer l'outil) **jusqu'à ce que**

$$
\tau(V) < 0{,}4\, w .
$$

Les résultats des trois dernières itérations utilisant des outils, et ceux des sous-agents, ne sont jamais masqués, et un résumé n'est appliqué que s'il est plus court que ce qu'il remplace. Un résultat masqué le reste dans les vues suivantes.

Les deux seuils forment une boucle d'hystérésis, le même dispositif qu'un thermostat. Avec un seuil unique $\theta$, une vue oscillant autour de $\theta$ masquerait et démasquerait à chaque pas, et chaque changement invaliderait le cache de prompt du fournisseur. Avec l'écart $[0{,}4w,\, 0{,}5w]$, un épisode de masquage achète au moins $0{,}1\,w$ tokens de croissance avant le suivant.

**Compaction.** Si la vue dépasse encore

$$
\tau(V) > \theta\, w, \qquad \theta = 0{,}8 \text{ par défaut, } \theta \in \,]0,1[,
$$

tenon résume. Il conserve le prompt système $H_0$, conserve la plus longue queue commençant à un message utilisateur dont la taille est au plus $0{,}3\,w$, et remplace tout ce qui se trouve entre les deux par un résumé rédigé par le modèle. Si le fournisseur répond malgré tout « contexte trop long », tenon force une compaction et réessaie **exactement une fois**. Si la queue seule est trop grande, le tour se termine par `context_too_large` plutôt que de laisser tomber en silence le dernier message de l'utilisateur.

Pourquoi cela compte pour le coût tient en un calcul court. Un agent renvoie toute la vue à chacune de ses $n$ itérations. Si chaque itération ajoute environ $s$ tokens, le coût en entrée sans aucune borne vaut

$$
\sum_{k=1}^{n} k\,s \;=\; \frac{n(n+1)}{2}\, s \;=\; \Theta(n^2),
$$

alors qu'avec une vue bornée par $\theta w$ il vaut au plus

$$
\sum_{k=1}^{n} \min(k\,s,\; \theta w) \;\le\; n\,\theta w \;=\; O(n).
$$

Masquage et compaction transforment une facture quadratique en facture linéaire. Par-dessus, les définitions d'outils sont triées par nom, pour que le préfixe de chaque requête soit stable à l'octet près, ce qui permet aux fournisseurs de le mettre en cache.

## 9. Terminaison et retour arrière

Une propriété de sûreté dit que rien de mauvais n'arrive ; une propriété de vivacité dit que quelque chose de bon finit par arriver[^as]. Pour un agent, ce quelque chose de bon est qu'un tour **se termine**.

!!! proposition "Proposition 9 (Tout tour se termine)"
    Un tour effectue au plus $M$ itérations de la boucle, avec $M = 50$ par défaut. Chaque itération effectue un nombre borné d'appels au fournisseur et au plus $k$ appels d'outils, chacun borné dans le temps. Tout tour se termine donc, avec l'une des raisons `done`, `max_iterations`, `incomplete`, `cancelled`, `blocked`, `blocked_by_hook`, `budget_exceeded`, `context_too_large` ou `error`.

Les bornes sont concrètes. Un appel au fournisseur est réessayé au plus $3$ fois, avec une attente $1\,\mathrm{s} \cdot 2^{a}$ plafonnée à $60\,\mathrm{s}$ ; une erreur de longueur de contexte déclenche au plus une compaction forcée ; une commande shell a un délai par défaut de 2 minutes et est tuée avec tout son groupe de processus. Un hook `Stop` qui relance l'agent consomme une itération comme une autre : les hooks ne peuvent pas repousser la borne. Un sous-agent exécute sa propre boucle avec le même $M$ et ne peut pas lancer de sous-agents à son tour : l'outil `agent` est retiré de son registre, la profondeur de récursion vaut donc $1$.

Le budget de tokens est une borne plus faible, *souple* : il est vérifié au début d'un tour ou d'un sous-agent, si bien qu'un tour peut finir légèrement au-dessus du budget, mais aucun nouveau tour ne commence une fois $u \ge B$. Un unique avertissement est émis quand $u \ge 0{,}8\,B$.

**Retour arrière.** Avant chaque écriture, tenon copie le contenu précédent du fichier dans un stockage adressé par le contenu : un fichier de condensat $x = \mathrm{SHA256}(\text{contenu})$ est rangé sous `x[0:2]/x`. Chaque prompt est un point de reprise $\rho_t$. La restauration est une fonction

$$
\mathrm{restaurer}(\rho_t)\colon\; \Phi \mapsto \Phi_t,
$$

et avant qu'un octet soit réécrit, son condensat est comparé à son nom. L'adressage par le contenu rend le stockage dédupliqué gratuitement (deux versions identiques ne font qu'un objet) et rend la corruption détectable plutôt que silencieuse. Le retour arrière est une commande de l'utilisateur uniquement ; le modèle ne peut pas l'invoquer.

## 10. La chaîne de livraison comme fonction

Tout ce qui précède est une propriété d'un programme. Reste à savoir si le binaire que vous exécutez **est** ce programme.

Une version de tenon est compilée avec ses dépendances embarquées (aucun proxy de modules), `CGO_ENABLED=0`, `-trimpath`, un identifiant de build vide, et `SOURCE_DATE_EPOCH` fixé à la date du dernier commit. Les archives sont écrites avec des noms triés, des propriétaires et des modes fixes, des dates de modification fixes et `gzip -n`. L'intention est que le build soit une **fonction** :

$$
\mathrm{build}\colon (\text{sources}, \text{chaîne d'outils}, \text{cible}) \longmapsto \text{octets},
$$

sans argument caché comme l'horloge, le chemin du dépôt ou l'ordre d'un listage de répertoire. `scripts/repro-check.sh` le teste : il compile chaque cible deux fois, dans des répertoires distincts avec des caches Go distincts, et échoue à la moindre différence de SHA-256.

La distribution est alors une chaîne de deux contrôles. `SHA256SUMS` liste le condensat de chaque artefact et est signé avec minisign, une signature Ed25519 dont la clé publique est `tenon.pub` à la racine du dépôt. Le script d'installation vérifie toujours l'archive contre `SHA256SUMS`, et vérifie la signature de `SHA256SUMS` quand `minisign` est disponible (toujours, avec `TENON_REQUIRE_SIGNATURE=1`, ce que fixent les modèles de CI). Un SBOM CycloneDX accompagne chaque version.

En CI, tenon tourne sans interface et la fonction de validation est la constante $\mathrm{valider} \equiv \mathsf{false}$ : chaque question reçoit non, et l'exécution sort avec le code $3$. Avec `--strict`, un tour qui ne s'est pas terminé par `done` sort avec $4$. Le code de sortie est l'échec de plus haute priorité, dans l'ordre $1 > 3 > 4 > 0$, si bien qu'un pipeline distingue « cassé », « refusé » et « inachevé » sans lire les journaux.

## 11. Ce qui n'est pas démontré

Une liste honnête compte davantage que toutes les propositions ci-dessus.

- **Le noyau.** L'isolation vaut ce que valent Landlock et Seatbelt. Sur Linux sans espaces de noms utilisateur et avec un ABI Landlock ancien, l'UDP n'est pas confiné.
- **La juridiction est déclarée, pas mesurée.** $j(p)$ vient de la configuration. tenon applique les conséquences d'une déclaration ; il ne géolocalise pas un serveur.
- **Le masquage est de la reconnaissance de motifs.** Un secret de moins de 8 caractères, ou dans un format qu'aucune règle ne connaît, n'est pas masqué. Les fragments en flux sont masqués un par un : un secret coupé entre deux fragments peut passer.
- **L'humain.** La proposition 1 garantit qu'une question est posée. Elle ne garantit pas que la réponse a été lue.
- **La fin du journal.** Entre le dernier horodatage et maintenant, supprimer des lignes est indétectable sans copie externe de la tête.
- **Ce sont des démonstrations sur papier.** Les propositions décrivent un modèle du code, vérifié par lecture, pas par un assistant de preuve. Un bogue dans le code est un contre-exemple au modèle, et doit être signalé comme tel.

## Conclusion

La thèse de cet essai tient en une ligne : **le modèle propose, le harness dispose**. Rien dans les §§2 à 10 ne dépend de la qualité du modèle. L'ordre des décisions, le plancher de la politique, l'étiquette qui monte, les marqueurs à sens unique, le processus confiné, la chaîne de hachage, la bande d'hystérésis, la boucle bornée et le build reproductible sont tous des propriétés d'un programme déterministe qui se lit, se teste et s'audite. C'est la seule sorte de garantie que l'on puisse offrir honnêtement sur un système dont le composant le plus puissant est, par construction, une boîte noire.

## Références

1. J. H. Saltzer et M. D. Schroeder. *The Protection of Information in Computer Systems.* Proceedings of the IEEE, 63(9), 1975.
2. L. Lamport. *Proving the Correctness of Multiprocess Programs.* IEEE Transactions on Software Engineering, SE-3(2), 1977.
3. B. Alpern et F. B. Schneider. *Defining Liveness.* Information Processing Letters, 21(4), 1985.
4. D. E. Denning. *A Lattice Model of Secure Information Flow.* Communications of the ACM, 19(5), 1976.
5. S. Haber et W. S. Stornetta. *How to Time-Stamp a Digital Document.* Journal of Cryptology, 3(2), 1991.
6. B. Schneier et J. Kelsey. *Secure Audit Logs to Support Computer Forensics.* ACM Transactions on Information and System Security, 2(2), 1999.
7. C. Adams, P. Cain, D. Pinkas et R. Zuccherato. *Internet X.509 Public Key Infrastructure Time-Stamp Protocol (TSP).* RFC 3161, 2001.
8. D. J. Bernstein, N. Duif, T. Lange, P. Schwabe et B.-Y. Yang. *High-Speed High-Security Signatures.* Journal of Cryptographic Engineering, 2(2), 2012.
9. ISO 7064:2003. *Information technology — Security techniques — Check character systems.*
10. Projet Reproducible Builds. *Définitions et spécification de SOURCE_DATE_EPOCH.* reproducible-builds.org.

Le code cité tout au long de l'essai se trouve dans le [dépôt tenon](https://github.com/tenon-code-eu/tenon), principalement `internal/engine`, `internal/policy`, `internal/audit`, `internal/routing`, `internal/sandbox` et `scripts/`. Les limites du §11 figurent aussi dans [Sécurité et limites](../../securite.md).

## Notes

[^ss]: Saltzer et Schroeder énoncent huit principes ; un harness s'appuie surtout sur les *valeurs par défaut sûres*, la *médiation complète* et le *moindre privilège*.
[^lamport]: Lamport a introduit le vocabulaire sûreté/vivacité pour les programmes concurrents ; Alpern et Schneider ont ensuite montré que toute propriété est l'intersection d'une propriété de sûreté et d'une propriété de vivacité.
[^ed25519]: minisign signe soit le message (ancien format `Ed`), soit son condensat BLAKE2b-512 (`ED`) ; tenon accepte les deux et vérifie aussi la signature globale portant sur le commentaire de confiance.
[^denning]: Le modèle de Denning est un treillis de classes muni d'une relation de flux ; l'étiquette à deux points de tenon, avec marque de crue, en est l'instance non triviale la plus simple.
[^sk]: La construction de Schneier et Kelsey ajoute des clés évolutives, pour qu'un attaquant qui compromet la machine plus tard ne puisse pas réécrire les entrées antérieures ; tenon préfère des horodatages externes à des clés conservées sur la machine.
[^hs]: L'idée de Haber et Stornetta : lier les documents par des hashs et publier un condensat de la chaîne rend l'antidatage infaisable.
[^rfc3161]: La requête porte l'OID de SHA-256 et un nonce que la réponse doit renvoyer, et demande le certificat de l'autorité. La révocation (CRL, OCSP) n'est pas vérifiée.
[^as]: Définition d'Alpern et Schneider : une propriété de vivacité est une propriété dont tout préfixe fini peut encore être prolongé en une exécution qui la satisfait.
