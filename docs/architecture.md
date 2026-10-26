# Architecture

Ce document décrit le code. Les décisions et leurs raisons sont dans [DESIGN.md](../DESIGN.md).

## Vue d'ensemble

```
 sessions Claude ──── MCP HTTP + jeton ────┐
 commande board ───── MCP et /api + jeton ─┼──> démon board (127.0.0.1:7391) ──> ~/.board/board.db
 page (navigateur) ── cookie + CSRF, SSE ──┘           │                          (SQLite, WAL, verrou exclusif)
                                                       └──> notifications macOS (osascript)
```

Un seul process, lancé par launchd (`dev.board.daemon`), sert tout le monde. Le binaire `board` est à la fois ce démon (`board daemon`) et la commande du propriétaire.

## Un appel, de bout en bout

1. **La connexion.** À l'ouverture d'une connexion TCP, `ConnContext` résout une fois l'identité de l'appelant :
   - le port du pair donne le PID, via libproc ;
   - on remonte jusqu'à l'ancêtre dont l'exécutable est sous `claude_prefixes`, par défaut `~/.local/share/claude/versions/` ;
   - l'identité vaut `claude:<pid>:<démarrage>`, et `terminal` faute d'ancêtre Claude.

   Le serveur écrit cette identité dans l'en-tête `X-Board-Identity`, en écrasant toujours ce que le client aurait envoyé.
2. **Le filtre HTTP.** `requireHost` refuse tout autre hôte que l'adresse locale, contre le DNS rebinding. `http.NewCrossOriginProtection` refuse les appels d'un autre site. Puis `authenticate` accepte :
   - le jeton (sessions et commande) ;
   - ou le cookie de la page, jamais sur `/mcp`, avec le jeton CSRF sur toute requête autre que GET.
3. **L'outil.** `tools.go` décode l'entrée et construit le `Caller` : identité, nom (`as`), terminal ou non, `Vouched`. `Vouched` est vrai si l'appel vient du terminal ou passe `owner_asked`, sauf avec `touch_id`. `Bind` lie le nom à l'identité : un nom tenu par une session vivante ne peut pas être pris par une autre.
4. **Le service.** `service.go` ouvre une transaction immédiate et y fait tout :
   - `plan` valide l'opération et en déduit l'action gouvernée, le projet et un résumé lisible ;
   - `decide` croise la règle du mode du projet pour cette action (`free`, `circle`, `approval`, `deny`) et l'appartenance de l'appelant au cercle du ticket. Pour un déplacement entre deux projets, la règle la plus stricte l'emporte ;
   - ce qui est permis passe par `apply`, ce qui est interdit est refusé ;
   - ce qui demande l'accord du propriétaire passe par `needsOwner`. Avec sa parole (`Vouched`), il est appliqué, avec la preuve `owner` ou `owner_asked`. En mode `queue`, il est mis en file. Sinon, il est refusé avec `ErrNeedsOwner`, qui dit à la session de lui poser la question.
5. **L'écriture.** `apply` modifie le ticket et ajoute un événement au journal, qui garde l'auteur et la preuve. Le journal est en ajout seul.
6. **La diffusion.** `changed()` réveille le notifieur et pousse un événement `changed` en SSE aux pages ouvertes, qui rechargent `/api/board`.

Prendre une tâche (`board_next`) choisit puis prend dans la même transaction : deux sessions ne peuvent pas prendre le même ticket.

## Le modèle

| Table | Contenu |
|---|---|
| `meta` | `schema_version` |
| `projects` | slug, nom |
| `tickets` | projet, `kind` (`task` ou `reminder`), titre, détails, priorité, échéance, état, session propriétaire, parent, liens, dernière activité de la session propriétaire |
| `ticket_circle` | les sessions qui ont créé, distribué ou pris le ticket : elles décident seules de son sort |
| `events` | le journal : auteur, type, texte, données, preuve |
| `actors` | le lien entre un nom de session et son identité système |
| `cursors` | le dernier passage de chaque session, pour « quoi de neuf » |
| `requests` | les demandes en file (option `queue`) |
| `notified` | les échéances déjà notifiées, une fois par jour (migration 2) |

Les états d'une tâche sont `inbox`, `todo`, `in_progress`, `blocked`, `to_validate`, `done` et `dropped`. Un rappel ne passe qu'entre `todo`, `done` et `dropped`. `schema.sql` est la version 1, et chaque migration ajoutée à la fin de `store.go` fait monter la version d'un cran. Un binaire refuse une base plus récente que lui.

## Les règles

`policy.go` définit :
- **les actions gouvernées** : `add`, `declare`, `note`, `progress`, `triage`, `take`, `reassign`, `close`, `drop`, `move`, `send_back`, `create_project` et `rebind_name` ;
- **les trois modes** : `demande`, `auto` (par défaut) et `tout_permis`, chacun avec une règle par action.

`transitionAction` traduit un changement d'état demandé par une note en action gouvernée. Les réglages peuvent redéfinir n'importe quelle règle, ou ajouter un mode.

## Le démon

`cmd/board/daemon.go` enchaîne au démarrage :
- le jeton ;
- la base, avec son verrou exclusif ;
- les réglages : un fichier invalide fait démarrer sur les défauts, avec une notification ;
- le résolveur d'identité, le service et le serveur HTTP.

Il lance ensuite à côté :
- le notifieur, qui suit le journal ;
- la relecture de `config.toml` toutes les 3 secondes, appliquée à l'enregistrement avec une notification ;
- la sauvegarde quotidienne (`VACUUM INTO` vers un fichier temporaire, renommé ensuite, 7 copies gardées).

`install.go` écrit le LaunchAgent : session graphique, relance automatique, `umask 077`, journal dans `~/.board/daemon.log`. Il enregistre le MCP avec `claude mcp add --scope user`.

## La page

`index.html`, `app.js`, `app.css` et `favicon.svg` sont embarqués dans le binaire.

La connexion passe par `board open`, qui demande un lien à usage unique, valable une minute. Ce lien pose un cookie de session valable 12 heures, gardé en mémoire. La page reçoit les changements par SSE et se rafraîchit aussi toutes les 5 minutes, pour les silences et les échéances. Elle construit tout son DOM par `textContent` : un ticket écrit par une session n'est jamais interprété comme du HTML.

Les boutons du propriétaire appellent des routes réservées au terminal et à la page :
- Valider et Refuser sur une demande en file ;
- Clore et Renvoyer sur un ticket à valider ;
- Fait et Pas fait sur un rappel.

## Les notifications

`notify.go` suit le journal à partir du dernier événement au démarrage : un redémarrage ne rejoue rien. Une notification part pour :
- une trouvaille ;
- un ticket bloqué, à valider ou fait ;
- une demande restée sans réponse ;
- une échéance, la veille et le jour même, à partir de 9 h.

Chaque type se règle sous `[notifications]`. Au-delà de trois notifications d'un coup, un résumé les remplace. Les gestes du propriétaire lui-même ne le notifient pas.
