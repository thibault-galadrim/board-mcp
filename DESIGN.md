# board - design

> Un Trello local partagé entre Thibault et ses sessions Claude Code.
> Idée née dans un échange avec un collègue le 22/10/2026, arbitrée le même jour.
> Version locale d'abord. Le partage (page, dépôt distant) se décidera plus tard.

## Principes

1. **Agnostique des rôles.** Le board ne connaît que des sessions nommées et des tickets. Il ne sait pas ce qu'est une coordinatrice ou un ouvrier, mais ses briques permettent ce mode sans l'imposer.
2. **Tout est réglable.** Chaque décision de ce document est un réglage dont la valeur par défaut est celle retenue ici. Les modes ne sont que des préréglages d'une matrice.
3. **Sécurité d'abord.** Un agent manipulé (par un mail, une page web ou un message lu) ne doit pas pouvoir retourner le board contre son propriétaire. Une validation humaine ne peut pas être forgée.
4. **Empreinte minimale.** Un seul process pour toutes les sessions, aucun CPU au repos.

## Usage au quotidien

1. Thibault ajoute des tickets en parlant à n'importe quelle session, ou avec `board add` dans un terminal.
2. Une session qui demande la suite reçoit le ticket le plus urgent **de son projet** parmi les tickets « à faire ». Le projet est toujours explicite : la session le nomme d'après le contexte de la conversation, jamais d'après le dossier.
3. Elle écrit ses notes d'avancement dans le **journal** du ticket (un historique daté, jamais un champ écrasé).
4. Ce qu'elle trouve en route va dans la **boîte de réception** (état « à trier »). Elle ne le traite pas : c'est Thibault qui valide la trouvaille.
5. Si elle a besoin de Thibault, elle passe le ticket en **bloqué** avec sa question. Quand elle a fini, elle le passe en **à valider** avec un résumé et sa PR. Thibault le passe en fait après sa recette.
6. Thibault a une page en direct : des colonnes par projet, la boîte de réception, les validations en attente, les tickets silencieux et les rappels.

## Modèle

### Tickets

- **Deux genres de tickets.**
  - **Tâche** : faite par une session.
  - **Rappel** : une tâche de Thibault que les agents lui ressortent mais n'exécutent jamais.
- **États** : à trier → à faire → en cours → bloqué ⇄ en cours → à valider → fait. On peut passer en abandonné depuis n'importe quel état.
- **Champs.**
  - projet (facultatif pour un rappel perso), titre, détails (markdown) ;
  - priorité P0 à P3, échéance facultative ;
  - propriétaire (un nom de session) ;
  - parent facultatif (sous-tickets), liens (PR, branche, Notion) ;
  - créé par, dernière activité.
- **Tri de la suite** : les tickets en retard d'abord, puis par priorité, puis par ancienneté.

### Projets

La liste est fermée. Un agent ne crée un projet que si Thibault dit explicitement « on fait un nouveau projet ». Un appel avec un projet inconnu est refusé et le board renvoie la liste des projets existants.

### Les cinq briques (agnostiques des rôles)

1. **Prendre pour soi ou pour quelqu'un.** `prendre(ticket, pour=<nom>)`. Une coordinatrice inscrit le ticket au nom de l'ouvrier qu'elle lance, une session seule le prend pour elle.
2. **Un ticket appartient à un nom, pas à un process.** On peut le passer à quelqu'un d'autre. Une session relancée sous le même nom retrouve ses tickets. Une compaction ne change rien.
3. **Des sous-tickets facultatifs** via `parent`.
4. **« Quoi de neuf depuis mon dernier passage ».** Le board garde un curseur de lecture par nom. C'est là qu'arrivent les notes, les blocages, les trouvailles, les tickets silencieux et les rappels du projet qui arrivent à échéance.
5. **Le silence est un constat.** Un ticket en cours sans activité depuis 2 h (réglable) est marqué silencieux. Rien n'est relâché automatiquement.

### Le cercle du ticket

Thibault, plus toute session qui a créé, distribué ou pris en charge le ticket (reconstitué à partir du journal). Seul le cercle peut décider du sort du ticket : le prendre, le passer, le relâcher, le clore ou l'abandonner. Les autres sessions peuvent le voir et le commenter.

### Modes (inspirés des modes de permission de Claude Code)

Un mode par défaut pour tout le board, ajustable projet par projet. **Le défaut est Auto** depuis le premier essai réel du 22/10 : en Demande partout, le flot d'invites faisait valider sans lire, ce qui est exactement la faille qu'un agent manipulé exploiterait. Un projet sensible peut rester en Demande.

| Action | Demande | Auto (défaut) | Tout permis |
|---|---|---|---|
| Ajouter, écrire une note, déposer une trouvaille | libre | libre | libre |
| Signaler l'avancement (bloqué, à valider, reprise) | cercle | cercle | cercle |
| Prendre un ticket libre | validation | libre | libre |
| Passer ou relâcher un ticket déjà pris | validation | cercle | cercle |
| Clore (→ fait) | validation | cercle | cercle |
| Abandonner | validation | cercle | cercle |
| Valider une trouvaille | Thibault | Thibault | libre |
| Créer un projet | Thibault | Thibault | Thibault |
| Reprendre le nom d'une session encore active | Thibault | Thibault | Thibault |
| Changer un réglage | Thibault | Thibault | Thibault |

« Cercle » : le cercle agit seul, et toute autre session passe par une validation. « Prendre » a été séparé de « passer » au socle, puisque personne n'est encore dans le cercle d'un ticket libre. En Tout permis, les tâches ajoutées vont directement en « à faire ».

La matrice est un réglage. On peut ajouter un mode ou modifier une case.

### Validations

- **Dans la conversation** : la session pose la question et Thibault répond.
- **En différé** : la demande attend dans la page (boutons Valider et Refuser), ou dans le terminal avec `board ok <id>` / `board refuse <id>`.
- **Preuve** : chaque validation déclenche un **Touch ID** affiché par le démon, d'où qu'elle parte. C'est réglable : par exemple, Touch ID seulement pour le sensible.
- **Seule autorité : le Touch ID posé en direct.** La base ne contient aucun droit. Une validation n'existe qu'au moment où le démon reçoit le Touch ID et exécute l'action dans la foulée. Il n'y a donc rien à falsifier dans la base.
- **Quelles demandes déclenchent l'invite tout de suite** (réglable : `owner`, `always`, `never`) : par défaut, seulement ce que le propriétaire a déclenché lui-même, c'est-à-dire sa commande `board`, un bouton de la page, ou une action qu'une session marque `owner_asked` parce qu'il vient de la lui dicter. Une session manipulée qui mettrait `owner_asked` à tort n'obtient qu'une invite, rare donc lue. Tout le reste attend dans la file, avec une notification.
- **Attente sans boucle** : la session lance `board wait <id>` en tâche de fond. Ça ne coûte rien, et Claude Code la réveille quand la commande se termine.
- Une prise validée reste atomique : si quelqu'un d'autre a pris le ticket entre-temps, elle est refusée.

### Notifications (macOS)

Elles partent quand un ticket est bloqué, à valider ou fait, quand une trouvaille est déposée, et la veille et le jour d'une échéance. Chaque déclencheur se règle.

Les rappels apparaissent aussi dans le « quoi de neuf » de toute session qui fait le point sur le projet, avec ou sans coordinatrice. Un rappel sans projet n'arrive que par notification.

## Architecture

- **Un seul démon Go** pour toutes les sessions, lancé par launchd (LaunchAgent, avec les droits de l'utilisateur), et relancé s'il tombe.
  - Mesure du 22/10 : 21 sessions ouvertes. Un serveur par session coûterait environ 700 Mo en Bun ou 170 Mo en Go. Le démon unique mesuré pèse 20 Mo (SQLite en Go pur et SDK MCP compris).
- **Transport** : MCP en HTTP sur `127.0.0.1`, enregistré une fois avec `claude mcp add --scope user` et un en-tête de jeton. Après un redémarrage du démon, Claude Code se ré-initialise tout seul (vérifié).
- **Stockage** : SQLite en mode WAL et `locking_mode=EXCLUSIVE` dans `~/.board/board.db`. Seul le démon peut lire ou écrire tant qu'il tourne, et la prise d'un ticket est une seule écriture conditionnelle dans une transaction.
- **Déverrouillage** : chaque démarrage du démon demande un Touch ID en affichant les réglages actifs. Sans ce Touch ID, le board reste fermé.
- **Mode d'emploi des agents** : il est livré par les instructions du serveur MCP. Rien à ajouter dans les CLAUDE.md.
- **Page** : servie par le démon, mise à jour poussée en direct (SSE), sans rafraîchissement périodique.
- **Échéances et silences** : surveillés par un minuteur interne du démon, sans tâche planifiée à côté.
- **Un seul binaire** : `board` est à la fois le démon et la commande pour l'humain (`add`, `ls`, `ok`, `refuse`, `wait`, `open`, `config`).
- **Réglages** : dans `~/.board/config.toml`. Le démon détecte une modification, affiche l'écart et attend le Touch ID avant de l'appliquer. Sans validation, la modification est ignorée et signalée.

### Outils MCP (première version)

Chaque appel porte le nom de la session (`as`).

| Outil | Rôle |
|---|---|
| `board_projects` | lister les projets, leur mode et leurs compteurs (création seulement sur demande du propriétaire) |
| `board_list` | lister, filtrer (projet, état, propriétaire, les miens) |
| `board_show` | fiche complète : détails, journal, cercle, sous-tickets, demandes en attente |
| `board_whatsnew` | quoi de neuf depuis mon dernier passage |
| `board_next` | prendre la suite dans un projet |
| `board_claim` | prendre un ticket précis, pour soi ou `pour` quelqu'un |
| `board_assign` | passer à quelqu'un ou relâcher, avec un motif |
| `board_note` | écrire une note et/ou changer l'état (bloqué + question, à valider + résumé + PR) |
| `board_add` | ajouter un ticket ou une trouvaille, avec un parent facultatif |

Quand l'action d'un agent attend une validation, l'outil répond avec l'identifiant de la demande.

## Sécurité

| Risque | Parade |
|---|---|
| Un site web ou un autre process qui appelle le démon | Écoute sur `127.0.0.1` uniquement, jeton obligatoire, contrôle des en-têtes `Host` et `Origin` (contre le DNS rebinding et le CSRF), aucun CORS. La page passe par un cookie `HttpOnly` / `SameSite=Strict` posé par `board open` avec un lien à usage unique, et chaque bouton porte un jeton CSRF |
| Une session qui se fait passer pour une autre | Le démon identifie l'appelant par le système : à partir de la connexion, il remonte jusqu'au process `claude` ancêtre (`libproc`, avec un cache par connexion). Un agent qui contourne MCP avec `curl` est rattaché à sa propre session, et les en-têtes d'identité fournis par le client sont ignorés. Un nom est lié à ce process : libre si le process est mort, et sa reprise demande une validation s'il est vivant |
| Une validation forgée | Le Touch ID en direct est la seule autorité, et la base ne contient aucun droit (voir Validations) |
| Un contenu piégé dans un ticket | Échappement systématique (`html/template`), markdown assaini, CSP stricte, aucune ressource externe. Les notifications passent leurs textes en arguments, jamais assemblés en script |
| Un accès direct à la base | `~/.board` en `0700` et la base en `0600`. Les règles deny `Read(~/.board/**)` de Claude Code couvrent les outils fichiers et les commandes courantes. Le verrou exclusif bloque `sqlite3` et les scripts tant que le démon tourne. Si un agent tue le démon pour y toucher, le redémarrage demande un Touch ID que tu n'attendais pas : tu refuses et le board reste fermé. Risque restant, accepté : l'état des tickets peut être modifié pendant ce court arrêt, jamais une validation ni un réglage sans que tu le voies |
| Un agent qui s'emballe | Plafonds de taille par champ et de débit par session |
| Les dépendances | SDK MCP Go officiel, SQLite, versions figées (`go.sum`), `govulncheck` |
| Une fuite | Le démon n'appelle jamais internet. Les requêtes SQL sont toujours paramétrées |

Le journal est en ajout seul : chaque action garde son auteur, et chaque validation garde sa méthode de preuve.

Niveaux écartés le 22/10, réutilisables plus tard :
- le coffre-fort, avec une base appartenant à un utilisateur macOS dédié et un assistant signé pour Touch ID ;
- le sandbox de Claude Code activé partout ;
- la clé de scellement dans le Trousseau (un agent pourrait signer son propre programme).

## Essai technique (fait le 22/10, détails dans `spike/README.md`)

- [x] Touch ID depuis un LaunchAgent, sans application ni signature : validation et refus fonctionnent.
- [x] MCP HTTP avec jeton, protections `Host` et `Origin`, instructions du serveur reçues, reconnexion automatique après redémarrage.
- [x] Identification de la session appelante par le système, y compris pour un `curl` lancé par un agent.
- [x] `~/.board` : règles deny partielles, verrou exclusif efficace tant que le démon tourne.
- [x] Trousseau : abandonné.

## Découpage (en PR)

1. ~~Essai technique~~ (fait).
2. ~~Socle~~ (fait le 22/10) : le démon, SQLite avec verrou exclusif, neuf outils MCP, la prise atomique, le journal, la commande `board`, la matrice des modes, le jeton et les contrôles d'en-têtes, l'identification des sessions par `libproc`. Les demandes de validation sont créées et rejouables (`Approve`/`Refuse`), mais rien ne les déclenche encore : c'est le lot 3.
3. ~~Validations~~ (fait le 22/10, essai réel avec le doigt à refaire au retour devant le Mac) : chaque demande déclenche une invite Touch ID (repli sur le mot de passe macOS, réglable), une à la fois, et l'appel de la session attend 45 s. « Annuler » sur une invite déclenchée par une session vaut refus ; sur un `board ok`, la demande reste en attente. `board ok`, `board refuse` et `board unlock` ne sont acceptés que depuis un terminal hors session Claude. `board wait` survit aux redémarrages du démon (codes de sortie : 0 approuvée, 2 refusée, 3 inapplicable, 4 en attente). Le démon démarre verrouillé.
4. ~~Page et notifications~~ (fait le 22/10) : `board open` crée un lien à usage unique (depuis le terminal seulement) qui pose un cookie `HttpOnly` / `SameSite=Strict`, sans accès à `/mcp`, avec jeton CSRF sur chaque bouton. La page montre un bandeau « Pour toi » (demandes avec Valider / Refuser, bloqués, à valider, rappels proches, silencieux), les colonnes de chaque projet et la fiche d'un ticket avec son journal ; elle se met à jour par flux poussé (SSE), plus un rafraîchissement toutes les 5 minutes pour les silences et les échéances. Les notifications macOS (`osascript`, texte passé en arguments) suivent le journal : trouvaille, bloqué, à valider, fait, demande restée sans réponse, et échéance la veille et le jour même à partir de 9 h, une seule fois même après un redémarrage (table `notified`, schéma 2). Au-delà de 3 notifications d'un coup, un résumé. Les silences ne notifient pas (réglable plus tard).
5. **Réglages et installation** : `config.toml` avec Touch ID, les règles deny sur `~/.board`, le LaunchAgent et l'enregistrement MCP.

## Tickets de départ

1. Les deux MCP Grafana lancent un `docker run` par session : 18 conteneurs, soit environ 340 Mo dans la VM Docker le 22/10. Les passer en un seul conteneur partagé.

## Ouvert (plus tard)

- Valider quand le propriétaire n'est pas devant le Mac (il suit souvent ses sessions depuis son téléphone) : aujourd'hui, les demandes attendent son retour et le board reste verrouillé.

- Partage avec un collègue (page, dépôt distant).
- Notification Slack en plus de macOS.
- Nom définitif (« board » est provisoire), port d'écoute.
