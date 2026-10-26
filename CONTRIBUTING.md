# Contribuer

## La règle : rien de non rétrocompatible

Une nouvelle version doit marcher telle quelle sur une installation existante, sans geste de migration. Elle doit accepter, sans rien changer :
- la base ;
- le `config.toml` ;
- les sessions Claude déjà lancées, qui ont chargé les anciens outils ;
- les scripts qui appellent `board` ;
- les habitudes de son propriétaire.

Tout le reste se discute. Ceci non.

Concrètement :

| Surface | On peut | On ne peut pas |
|---|---|---|
| Base SQLite | ajouter une migration en fin de liste (`internal/board/store.go`) : table, colonne avec valeur par défaut, index | modifier une migration publiée ou `schema.sql` (la version 1), renommer ou supprimer une table ou une colonne, changer le sens d'une valeur stockée (états, types de tickets, types d'événements) |
| `config.toml` | ajouter une clé facultative dont le défaut reproduit le comportement actuel | renommer ou supprimer une clé, changer son type ou ses valeurs admises, changer la valeur par défaut d'une clé existante. Le parseur est strict : une clé inconnue invalide tout le fichier |
| Outils MCP | ajouter un outil, un paramètre facultatif, un champ dans une réponse | renommer ou supprimer un outil, un paramètre ou un champ, rendre obligatoire un paramètre facultatif, changer le sens d'une valeur (`state`, `kind`, `status`) |
| Commande `board` | ajouter une commande, une option | renommer ou supprimer une commande ou une option, changer les codes de sortie de `board wait` (0 accordé, 2 refusé, 3 inapplicable, 4 toujours en attente) |
| Routes `/api` | ajouter une route, un champ facultatif | changer une route existante : la commande installée peut être plus récente que le démon qui tourne, ou l'inverse |
| Installation | | changer l'adresse par défaut `127.0.0.1:7391`, le dossier `~/.board` et le nom de ses fichiers, le label `dev.board.daemon`, le nom du MCP `board`, les variables `BOARD_HOME`, `BOARD_ADDR` et `BOARD_AS` |
| Comportement par défaut | ajouter un comportement derrière une option désactivée par défaut | ajouter un geste sur le chemin par défaut (clic, Touch ID, file d'attente), durcir une règle par défaut de la matrice des modes |

Si un changement ne peut pas se faire de façon rétrocompatible, on ne le fait pas tel quel. On ajoute à côté (un nouvel outil, une nouvelle clé, une nouvelle migration), et l'ancien continue de marcher. En cas de doute, ouvre une issue avant d'écrire le code.

## Le reste, par bon sens

- **Tests.** `go test -race ./...` passe avant chaque commit. Un bug commence par un test qui le reproduit et échoue, puis vient le correctif. Un changement de schéma vient avec un test qui ouvre une base de la version précédente.
- **Sécurité.** Le board ne doit ouvrir aucune faille sur la machine :
  - il écoute en local seulement, avec un jeton ;
  - il contrôle les en-têtes `Host` et `Origin` ;
  - les boutons de la page portent un jeton CSRF ;
  - la CSP est stricte, et le contenu des tickets n'est jamais interprété comme du HTML ;
  - les requêtes SQL sont paramétrées, et le démon ne fait aucun appel réseau sortant.

  Une nouvelle dépendance demande une vraie raison.
- **Sobriété.** Un seul démon pour toutes les sessions, autour de 20 Mo de mémoire. Rien ne sonde en boucle : la page reçoit les changements par SSE, et les échéances ont leur minuteur.
- **La parole du propriétaire suffit.** Les verrous plus forts restent des options, désactivées par défaut.
- **Tout est réglable.** Une nouvelle règle devient une clé de `config.toml`, dont le défaut reproduit le comportement actuel.
- **Langues.**
  - En anglais : le code, les commentaires et les messages de commit.
  - En français : l'interface, la commande, les notifications et la documentation.
- **Aucun nom réel.** Pas de clients, de collègues ni de projets réels dans le code, les tests ou la doc. Les exemples utilisent `atlas`, `orion`, `zephyr` ou `perso`.
- **Commits.** Atomiques, au format `type(scope): résumé` (`feat(page): …`, `fix(cli): …`), et chacun passe les tests.

## Avant d'ouvrir une PR

- [ ] `go test -race ./...` passe.
- [ ] Une installation existante marche sans geste : base, `config.toml`, sessions déjà lancées, scripts.
- [ ] Une nouvelle option est désactivée par défaut, ou reproduit le comportement actuel.
- [ ] La doc est à jour :
  - le README pour l'usage ;
  - DESIGN.md si une décision change ;
  - `internal/server/instructions.md` et `skills/use-board` si les sessions doivent le savoir.
- [ ] La description dit ce qui change pour l'utilisateur, et comment la compatibilité est tenue.
