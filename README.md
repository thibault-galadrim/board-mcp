# board

Un tableau de tâches local, partagé entre toi et toutes tes sessions Claude Code sur ton Mac.

Les sessions y inscrivent ce qu'elles font, prennent la suite d'un projet, signalent un blocage ou déposent une trouvaille. Toi, tu suis tout sur une page en direct, tu reçois une notification quand on a besoin de toi, et tu tranches d'un mot. Le board est né pour garder la tête froide avec une vingtaine de sessions en parallèle.

Un seul démon Go pour toutes les sessions, environ 20 Mo de mémoire, rien qui sonde en boucle, aucun appel à internet.

## Ce que ça fait

**Pour les sessions**, dix outils MCP :

| Outil | Rôle |
|---|---|
| `board_projects` | les projets, leur mode et leurs compteurs |
| `board_whatsnew` | ce que les autres ont fait depuis mon dernier passage, mes tickets, les rappels qui arrivent |
| `board_list`, `board_show` | lister, filtrer, lire une fiche et son journal |
| `board_add` | une trouvaille (part dans ta boîte de réception), un travail déjà en cours, un rappel pour toi, un sous-ticket |
| `board_next`, `board_claim` | prendre la tâche libre la plus urgente d'un projet, ou un ticket précis |
| `board_note` | tenir le journal, passer en bloqué (avec la question) ou à valider (avec le résumé et la PR) |
| `board_assign`, `board_move` | passer ou relâcher un ticket, le ranger dans le bon projet |

Chaque session s'annonce par un nom (`as`), et le démon vérifie par le système quel process Claude l'appelle : une session ne peut pas agir sous le nom d'une autre.

**Pour toi** :

- **La page** (`board open`) a un onglet « Pour toi », qui regroupe :
  - les bloqués ;
  - les tickets à valider, avec Clore et Renvoyer ;
  - les rappels, avec Fait et Pas fait ;
  - les tickets silencieux.

  Elle a aussi un onglet par projet, en colonnes, et la fiche de chaque ticket avec son journal. Elle se met à jour toute seule.
- **Les notifications macOS** : bloqué, à valider, fait, trouvaille, échéance la veille et le jour même.
- **La commande `board`** pour tout faire depuis le terminal.
- **Les rappels** : tes tâches à toi. Les sessions te les remontent et ne les exécutent jamais.

**Ta parole suffit.** Selon le mode du projet (`demande`, `auto` par défaut, `tout_permis`), certaines décisions te reviennent. Elles s'appliquent quand tu les prends toi-même, depuis ton terminal ou la page. Elles s'appliquent aussi quand une session agit sur ce que tu viens de lui demander dans la conversation : elle passe `owner_asked`, et le journal note d'où vient l'accord. Sans ta parole, la session te pose la question, sans clic ni Touch ID. Les deux restent disponibles en option.

## Prérequis

- macOS : le démon s'appuie sur launchd, osascript, et via cgo sur libproc et LocalAuthentication.
- Go 1.26 et les Command Line Tools de Xcode, pour cgo.
- Claude Code, avec `claude` dans le `PATH`.

## Installation

```sh
git clone git@github.com:thibault-galadrim/board-mcp.git
cd board-mcp
go build -o bin/board ./cmd/board
./bin/board install
```

`board install` :
- copie le binaire dans `~/.local/bin/board` ;
- crée `~/.board` (droits `0700`) avec un jeton ;
- installe le démon comme LaunchAgent `dev.board.daemon`, relancé à chaque ouverture de session ;
- enregistre le MCP `board` pour toutes tes sessions Claude.

Relance-le après chaque mise à jour : il remplace le démon en place.

Ensuite :

1. **Interdis aux sessions de lire le dossier du board**, dans `~/.claude/settings.json` :

   ```json
   { "permissions": { "deny": ["Read(~/.board/**)", "Edit(~/.board/**)"] } }
   ```

2. **Donne ton prénom** : clé `owner_name` dans `~/.board/config.toml`. Le défaut est « Thibault ».
3. **Crée tes projets** depuis ton terminal : `board project-add atlas "Atlas"`. La liste est fermée : une session n'en crée un que si tu le lui dis.
4. **Installe le skill d'onboarding** : `cp -r skills/use-board ~/.claude/skills/`. Dans une session, `/use-board` lui fait inscrire son travail en cours, puis tenir le board à jour.
5. **Ouvre la page** : `board open`.

Une session ouverte avant l'installation ne voit pas les outils MCP. Elle utilise la commande à la place, avec son nom : `BOARD_AS=fix-login board news -p atlas`.

## Au quotidien

| Commande | Effet |
|---|---|
| `board open` | ouvre la page (lien à usage unique, depuis ton terminal seulement) |
| `board news [-p projet]` | quoi de neuf depuis ton dernier passage |
| `board ls [-p projet] [-s état]` | les tickets ouverts |
| `board add -p atlas -t "titre"` | une tâche directement à faire pour les sessions |
| `board add -r -d 2026-11-03 "titre"` | un rappel pour toi |
| `board show <id>` | la fiche et le journal |
| `board config` | affiche tes réglages |
| `board help` | toutes les commandes |

La page se déconnecte à chaque redémarrage du démon (après une mise à jour par exemple) : relance `board open`.

## Réglages

`~/.board/config.toml` est créé au premier démarrage, avec toutes les clés en commentaire et leur valeur par défaut. Une modification s'applique dès l'enregistrement, et une notification te dit ce qui a changé. Un fichier invalide est ignoré et signalé, et une clé inconnue compte comme une erreur.

Les réglages principaux :
- **Modes :** `default_mode`, le mode de chaque projet sous `[projects]`, et la matrice des modes sous `[modes.<mode>]`. Elle donne une règle par action : `free`, `circle`, `approval` ou `deny`.
- **Accords :** `[approvals]`, avec `queue` pour une file de demandes et `touch_id` pour faire passer chaque décision par Touch ID.
- **Notifications :** `[notifications]`, une clé par type de notification.
- **Délais :** `[timing]`, avec le délai avant qu'un ticket devienne silencieux et le plafond d'écritures par session.

Le détail des modes et des règles est dans [DESIGN.md](DESIGN.md#modes-inspirés-des-modes-de-permission-de-claude-code).

## Sécurité

Le board ne doit ouvrir aucune faille sur le Mac :

- **Accès au démon :** il écoute sur `127.0.0.1` seulement, exige un jeton, et vérifie les en-têtes `Host` et `Origin`, ce qui protège du DNS rebinding et des appels depuis un site web.
- **La page :** un cookie `HttpOnly` / `SameSite=Strict` posé par un lien à usage unique, qui n'ouvre jamais le MCP. Chaque bouton porte un jeton CSRF. La CSP est stricte : aucun script en ligne, aucune ressource externe, et le contenu des tickets n'est jamais interprété comme du HTML.
- **Identité des sessions :** elle est établie par le système. Les en-têtes fournis par un client sont ignorés.
- **La base :** verrouillée en exclusif tant que le démon tourne. Le journal est en ajout seul, et chaque accord garde sa preuve.
- **Les notifications :** leurs textes passent en arguments à `osascript`, jamais assemblés en script.

Le board fait confiance à tes sessions. Ce n'est pas un bac à sable : une session a déjà un terminal complet. Le modèle de menace complet est dans [DESIGN.md](DESIGN.md#sécurité).

## Données

`~/.board` contient :
- `board.db`, la base ;
- `token`, le jeton ;
- `config.toml`, les réglages ;
- `daemon.log`, le journal du démon ;
- `backups/`, une copie cohérente de la base par jour, les 7 dernières gardées.

Pour restaurer une copie :

```sh
board uninstall
cp ~/.board/backups/board-AAAA-MM-JJ.db ~/.board/board.db
board install
```

`board uninstall` arrête le démon et retire le MCP. Il laisse tes données dans `~/.board` et le binaire dans `~/.local/bin/board`.

## Limites

- macOS seulement : une machine, un propriétaire.
- Pas d'accès depuis le téléphone, par choix : le démon n'écoute qu'en local.
- La page se déconnecte à chaque redémarrage du démon.

## Documentation

- [DESIGN.md](DESIGN.md) : la conception et les arbitrages, datés.
- [docs/architecture.md](docs/architecture.md) : le code, et le trajet d'un appel.
- [CONTRIBUTING.md](CONTRIBUTING.md) : la règle des contributions.
- [CLAUDE.md](CLAUDE.md) : le manuel de Claude Code dans ce dépôt.
- [skills/use-board](skills/use-board/SKILL.md) : le skill pour les sessions.
- [spike/README.md](spike/README.md) : l'essai technique du départ.
