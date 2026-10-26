# board - design

> Un Trello local partagé entre Thibault et ses sessions Claude Code.
> Idée née dans un échange avec un collègue le 22/10/2026, arbitrée le même jour.
> Dépôt public depuis le 26/10. Usage dans le [README](README.md), code dans [docs/architecture.md](docs/architecture.md), règle des contributions dans [CONTRIBUTING.md](CONTRIBUTING.md).

## Principes

1. **Agnostique des rôles.** Le board ne connaît que des sessions nommées et des tickets. Il ne sait pas ce qu'est une coordinatrice ou un ouvrier, mais ses briques permettent ce mode sans l'imposer.
2. **Tout est réglable.** Chaque décision de ce document est un réglage dont la valeur par défaut est celle retenue ici. Les modes ne sont que des préréglages d'une matrice.
3. **Aucune faille pour le Mac.** Le démon n'ouvre aucune porte sur la machine : écoute locale, jeton, protections web, aucun texte de ticket interprété, rien vers internet. Le board fait confiance aux sessions du propriétaire, qui ont déjà un terminal complet : il ne leur oppose pas de Touch ID (précisé le 22/10, après une première version où chaque validation passait par Touch ID).
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
| Déclarer un travail déjà en cours (à son nom, que personne d'autre ne peut prendre) | validation | libre | libre |
| Signaler l'avancement (bloqué, à valider, reprise) | cercle | cercle | cercle |
| Prendre un ticket libre | validation | libre | libre |
| Passer ou relâcher un ticket déjà pris | validation | cercle | cercle |
| Clore (→ fait) | validation | cercle | cercle |
| Abandonner | validation | cercle | cercle |
| Déplacer vers un autre projet (avec ses sous-tickets ; le mode le plus strict des deux projets s'applique) | validation | cercle | cercle |
| Valider une trouvaille | Thibault | Thibault | libre |
| Créer un projet | Thibault | Thibault | Thibault |
| Reprendre le nom d'une session encore active | Thibault | Thibault | Thibault |
| Changer un réglage | Thibault | Thibault | Thibault |

« Cercle » : le cercle agit seul, et toute autre session passe par une validation. « Prendre » a été séparé de « passer » au socle, puisque personne n'est encore dans le cercle d'un ticket libre. En Tout permis, les tâches ajoutées vont directement en « à faire ».

La matrice est un réglage. On peut ajouter un mode ou modifier une case.

### Validations

- **La parole du propriétaire suffit.** Une action qui demande son accord s'applique quand il la déclenche lui-même (son terminal, la page) ou quand une session agit sur ce qu'il vient de lui demander dans la conversation (`owner_asked`, `-asked` en commande). Le journal note d'où vient l'accord (`owner` ou `owner_asked`).
- **Sans sa parole, rien ne s'empile** : le board répond à la session qu'il faut son accord, et la session lui pose la question dans la conversation. Pas de clic, pas d'invite.
- **Options** (`config.toml`, désactivées par défaut) : `queue` met les demandes sans parole en file (page, `board ok`, `board wait`) ; `touch_id` fait passer chaque décision par Touch ID (et active la file), avec les invites et le verrou au démarrage du premier essai.
- Une prise validée reste atomique : si quelqu'un d'autre a pris le ticket entre-temps, elle est refusée.

### Notifications (macOS)

Elles partent quand un ticket est bloqué, à valider ou fait, quand une trouvaille est déposée, et la veille et le jour d'une échéance. Chaque déclencheur se règle.

Les rappels apparaissent aussi dans le « quoi de neuf » de toute session qui fait le point sur le projet, avec ou sans coordinatrice. Un rappel sans projet n'arrive que par notification.

## Architecture

- **Un seul démon Go** pour toutes les sessions, lancé par launchd (LaunchAgent, avec les droits de l'utilisateur), et relancé s'il tombe.
  - Mesure du 22/10 : 21 sessions ouvertes. Un serveur par session coûterait environ 700 Mo en Bun ou 170 Mo en Go. Le démon unique mesuré pèse 20 Mo (SQLite en Go pur et SDK MCP compris).
- **Transport** : MCP en HTTP sur `127.0.0.1`, enregistré une fois avec `claude mcp add --scope user` et un en-tête de jeton. Après un redémarrage du démon, Claude Code se ré-initialise tout seul (vérifié).
- **Stockage** : SQLite en mode WAL et `locking_mode=EXCLUSIVE` dans `~/.board/board.db`. Seul le démon peut lire ou écrire tant qu'il tourne, et la prise d'un ticket est une seule écriture conditionnelle dans une transaction.
- **Démarrage** : le board est ouvert dès que le démon démarre (le verrou par Touch ID n'existe plus qu'en option, avec `touch_id`).
- **Mode d'emploi des agents** : il est livré par les instructions du serveur MCP. Rien à ajouter dans les CLAUDE.md.
- **Page** : servie par le démon, mise à jour poussée en direct (SSE), sans rafraîchissement périodique.
- **Échéances et silences** : surveillés par un minuteur interne du démon, sans tâche planifiée à côté.
- **Un seul binaire** : `board` est à la fois le démon et la commande pour l'humain (`add`, `ls`, `ok`, `refuse`, `wait`, `open`, `config`).
- **Réglages** : dans `~/.board/config.toml`. Une modification s'applique dès l'enregistrement, et une notification dit ce qui a changé. Un fichier invalide est ignoré et signalé.

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
| `board_move` | déplacer un ticket rangé dans le mauvais projet, avec ses sous-tickets |
| `board_add` | ajouter un ticket ou une trouvaille, avec un parent facultatif |

Quand l'action d'un agent attend une validation, l'outil répond avec l'identifiant de la demande.

## Sécurité

| Risque | Parade |
|---|---|
| Un site web ou un autre process qui appelle le démon | Écoute sur `127.0.0.1` uniquement, jeton obligatoire, contrôle des en-têtes `Host` et `Origin` (contre le DNS rebinding et le CSRF), aucun CORS. La page passe par un cookie `HttpOnly` / `SameSite=Strict` posé par `board open` avec un lien à usage unique, et chaque bouton porte un jeton CSRF |
| Une session qui se fait passer pour une autre | Le démon identifie l'appelant par le système : à partir de la connexion, il remonte jusqu'au process `claude` ancêtre (`libproc`, avec un cache par connexion). Un agent qui contourne MCP avec `curl` est rattaché à sa propre session, et les en-têtes d'identité fournis par le client sont ignorés. Un nom est lié à ce process : libre si le process est mort, et sa reprise demande une validation s'il est vivant |
| Une validation inventée par une session | Accepté : une session a déjà un terminal complet, le board lui fait confiance. Le journal garde d'où vient chaque accord (`owner_asked` et le nom de la session), et `touch_id` existe pour qui veut le verrou |
| Un contenu piégé dans un ticket | Échappement systématique (`html/template`), markdown assaini, CSP stricte, aucune ressource externe. Les notifications passent leurs textes en arguments, jamais assemblés en script |
| Un accès direct à la base | `~/.board` en `0700` et la base en `0600`. Les règles deny `Read(~/.board/**)` de Claude Code couvrent les outils fichiers et les commandes courantes. Le verrou exclusif bloque `sqlite3` et les scripts tant que le démon tourne. Une sauvegarde quotidienne limite la perte en cas de base abîmée |
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
5. **Réglages et installation** (code fait le 22/10, installation à lancer avec le propriétaire) : `~/.board/config.toml` commenté, écrit au premier démarrage, lu strictement (une clé inconnue est une erreur) ; une modification à chaud est présentée en clair dans une invite Touch ID et n'est appliquée qu'après accord, un contenu refusé n'est pas reproposé tant que le fichier ne change pas ; un fichier invalide au démarrage fait tourner le démon sur les réglages par défaut, avec une notification. `board install` copie le binaire dans `~/.local/bin`, écrit le LaunchAgent (session graphique, relance automatique, journal dans `~/.board/daemon.log`) et enregistre le MCP pour toutes les sessions ; `board uninstall` les retire en gardant les données. Les règles deny sur `~/.board` s'ajoutent à la main dans `~/.claude/settings.json`.

## Tickets de départ

1. Les deux MCP Grafana lancent un `docker run` par session : 18 conteneurs, soit environ 340 Mo dans la VM Docker le 22/10. Les passer en un seul conteneur partagé.

## Arbitrages depuis la mise en service (22/10)

| # | Décision | Qualification |
|---|---|---|
| 1 | Valider depuis le téléphone : non, les demandes attendent le retour au Mac | inchangé |
| 2 | Notifications Slack : pas maintenant, le démon n'appelle toujours pas internet | inchangé |
| 3 | Partage avec un collègue : après quelques jours de rodage | plus tard |
| 4 | Sauvegarde : copie quotidienne cohérente de la base dans `~/.board/backups/`, 7 gardées | fait |
| 5 | Nom : « board » est gardé | inchangé |
| 6 | Silences : visibles dans la page et le « quoi de neuf », sans notification | inchangé |
| 7 | Ticket n°4 (conception) : passé à valider, les suites deviennent des tickets séparés | fait |
| 8 | Page : un onglet par projet plus « Pour toi », l'onglet dans l'adresse ; Clore et Renvoyer (avec commentaire, action `send_back` libre par défaut) sur les tickets à valider | fait |
| 9 | Sécurité = aucune faille pour le Mac, pas de Touch ID opposé aux sessions : la parole du propriétaire (terminal, page, `owner_asked`) suffit | fait |
| 10 | Sans sa parole : pas de file ni de clic, la session lui pose la question (`queue` en option) | fait |
| 11 | Plus de verrou au démarrage ; réglages appliqués à l'enregistrement, avec notification (`touch_id` en option) | fait |
| 12 | Rappels (23/10) : « Fait » ou « Pas fait » depuis la page, avec un commentaire facultatif qui va au journal (bandeau « Pour toi », onglet du projet, fiche) ; « pas fait » abandonne le rappel. Ses propres gestes ne le notifient plus | fait |
| 13 | Dépôt (26/10) : public sur GitHub, historique anonymisé avant le premier push (noms de clients et de collègues remplacés par `atlas`, `orion`, `zephyr`) | fait |
| 14 | Contributions (26/10) : une seule vraie règle, rien de non rétrocompatible ([CONTRIBUTING.md](CONTRIBUTING.md)) ; README, CLAUDE.md, architecture et skill `use-board` dans le dépôt | fait |
