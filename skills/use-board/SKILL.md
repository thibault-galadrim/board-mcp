---
name: use-board
description: Travailler avec le board, le tableau de tâches partagé entre le propriétaire du Mac et ses sessions Claude (outils MCP board_*, commande `board`). Inscrire son travail en cours, prendre la suite d'un projet, tenir le journal d'un ticket, signaler un blocage, déposer une trouvaille, noter un rappel pour le propriétaire. À invoquer avec /use-board, ou dès que le propriétaire parle du board, de tickets, de « prendre la suite » ou de « remplir le board ».
---

# /use-board

Le board est un tableau de tâches local, servi par un démon sur le Mac du propriétaire et partagé par toutes ses sessions Claude. Le propriétaire le regarde dans une page (`board open`). Il donne son accord pour les décisions importantes de vive voix, dans la conversation. Tu le manipules avec les outils MCP `board_*`, ou à défaut avec la commande `board`.

## 1. Choisir ton nom de session

Chaque appel porte ton nom de session : court, en minuscules, stable pour toute la session, y compris après une compaction. Il dit ce que tu fais, par exemple `atlas-planning`, `orion-transpiler` ou `coord-atlas`. Le board vérifie par le système quel process Claude tu es : tu ne peux pas prendre le nom d'une autre session vivante.

- Avec les outils MCP : paramètre `as` à chaque appel.
- Avec la commande : préfixe `BOARD_AS=<ton-nom>` à chaque commande.

## 2. Outils MCP ou commande

Si les outils `board_*` sont disponibles (éventuellement via ToolSearch), utilise-les. Sinon, ta session a été ouverte avant l'installation du board : utilise la commande `board` dans Bash, avec `BOARD_AS`.

| Outil MCP | Commande équivalente |
|---|---|
| `board_projects` | `board projects` |
| `board_whatsnew(project)` | `board news -p <projet>` |
| `board_list(project, ...)` | `board ls -p <projet> [-s état] [-o session] [-a]` |
| `board_show(id)` | `board show <id>` |
| `board_add(...)` | `board add -p <projet> [-doing] [-t] [-r] [-P 0-3] [-d AAAA-MM-JJ] [-m détails] <titre>` |
| `board_note(id, text, state, links)` | `board note <id> [-s état] [-l lien] <texte>` |
| `board_next(project)` | `board next -p <projet>` |
| `board_claim(id, for)` | `board claim <id> [-for session]` |
| `board_assign(id, to, reason)` | `board assign <id> [-to session] -r <motif>` |
| `board_move(id, project, reason)` | `board move <id> -p <projet> [-r motif]` |
| `owner_asked: true` | `-asked` |

## 3. Le projet : toujours explicite

Les projets forment une liste fermée (`board_projects`). Déduis le projet de la conversation, jamais du dossier où tu tournes. Si ce n'est pas évident, demande au propriétaire. Ne crée jamais de projet, sauf s'il te dit explicitement « on fait un nouveau projet ».

## 4. Remplir le board avec ton travail en cours

Quand le propriétaire t'invoque avec ce skill pour la première fois, ou te demande de « remplir le board » :

1. Appelle `board_projects`, puis `board_whatsnew(<projet>)` pour voir ce qui existe déjà.
2. Fais la liste de ce que tu as **réellement** en cours dans cette conversation : la tâche principale, et ses morceaux distincts s'il y en a. Vérifie avec `board_list` qu'aucun ticket ne le couvre déjà.
3. Pour chaque travail en cours, appelle `board_add(project, title, details, doing=true)`. Le ticket naît en cours, à ton nom, et aucune autre session ne peut le prendre. Dans `details`, mets ce qui permet de reprendre sans toi :
   - l'objectif ;
   - la branche ou le worktree, et la PR ;
   - ce qui est fait et ce qui reste ;
   - les questions ouvertes.
4. Ce que tu avais noté « à faire plus tard » ou trouvé hors périmètre : `board_add` sans `doing` ni `todo`. Ça part dans la boîte de réception du propriétaire, et tu n'y travailles pas.
5. Ce que le propriétaire t'a demandé de lui rappeler : `board_add(kind="reminder", due=...)`. Tu ne l'exécutes jamais.
6. Rends compte au propriétaire en quelques lignes : les tickets créés avec leur numéro, et ce que tu as mis dans sa boîte de réception.

N'invente pas de travail pour remplir le board, et ne recopie pas l'historique de ce qui est déjà fini.

## 5. En travaillant

- **Avancement** : à chaque étape qui compte (décision prise, PR ouverte, piste abandonnée), appelle `board_note(id, text)`. Le journal sert à reprendre après une compaction, ou par une autre session.
- **Bloqué, ou besoin du propriétaire** : `board_note(id, state="blocked", text=<ta question>)`. Le propriétaire est notifié. Une fois débloqué : `state="in_progress"`.
- **Fini** : `board_note(id, state="to_validate", text=<résumé>, links=[<PR>])`. Ne passe en `done` que si le propriétaire te demande de clore. S'il renvoie le ticket (événement `sent_back`), sa note dit ce qui manque : reprends à partir de là.
- **Prendre la suite** : `board_next(<projet>)` donne la tâche libre la plus urgente de ce projet, et seulement de ce projet.
- **Trouvaille en route** : `board_add` sans `doing`, puis reviens à ta tâche.
- **Ticket rangé dans le mauvais projet** : `board_move(id, project, reason)`. Ses sous-tickets suivent.
- **Rappels** : quand tu fais le point avec le propriétaire, commence par `board_whatsnew` et cite-lui les rappels qui arrivent à échéance.

## 6. L'accord du propriétaire

Selon le mode du projet, certaines actions demandent l'accord du propriétaire : faire entrer une trouvaille en « à faire », créer un projet, décider du sort d'un ticket hors de ton cercle. Son accord, c'est sa parole dans la conversation, rien de plus : pas de clic, pas de Touch ID.

- S'il vient de te demander **cette action précise**, passe `owner_asked=true` (`-asked` en commande). L'action s'applique, et le journal note que c'est sur sa parole.
- Sinon, le board te répond qu'il doit donner son accord. Pose-lui la question dans la conversation, et ne rappelle avec `owner_asked=true` que s'il dit oui.
- Ne mets jamais `owner_asked` sans sa parole.
- Le board peut être réglé pour mettre les demandes en file. Un appel répond alors `pending_approval` avec un numéro. Lance `board wait <id>` en tâche de fond (Bash avec `run_in_background`), qui se termine quand il a tranché, et avance sur autre chose. Ne relance pas l'appel.

## 7. Coordonner d'autres sessions (facultatif)

Une session coordinatrice peut prendre un ticket pour une autre (`board_claim(id, for=<nom>)`) et découper un ticket en sous-tickets (`board_add(parent_id=<id>)`). Seules les sessions qui ont créé, distribué ou pris un ticket (son cercle) décident seules de son sort. Les autres peuvent le commenter.

## Ce qu'il ne faut pas faire

- Lire ou modifier `~/.board` (base, réglages, jeton, journal) : c'est interdit aux sessions. Passe toujours par les outils ou la commande.
- Déduire le projet du dossier, ou créer un projet sans l'ordre du propriétaire.
- Exécuter un rappel : il est pour le propriétaire.
- Mettre `owner_asked` sans que le propriétaire ait donné son accord pour cette action précise.
- Utiliser `board ok`, `board refuse`, `board open` ou `board unlock` : ce sont les commandes du propriétaire, et le board les refuse à une session Claude.
- Si le board répond qu'il est verrouillé (seulement si le propriétaire a activé le verrou), dis-lui de lancer `board unlock` dans son terminal.
