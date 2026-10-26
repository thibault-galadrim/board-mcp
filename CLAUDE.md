# CLAUDE.md

Le manuel de Claude Code dans ce dépôt. L'usage est dans le [README](README.md), les décisions dans [DESIGN.md](DESIGN.md), le code dans [docs/architecture.md](docs/architecture.md).

## Le projet

`board` est un tableau de tâches local, partagé entre son propriétaire et toutes ses sessions Claude Code sur un Mac. C'est un binaire Go unique, à la fois le démon et la commande. Le démon sert sur `127.0.0.1:7391` :
- le MCP en HTTP, pour les sessions ;
- une API, pour la commande ;
- une page en direct, pour le propriétaire.

Le stockage est en SQLite. Le projet est macOS seulement : launchd, osascript, et via cgo libproc et LocalAuthentication.

## Commandes

```sh
go vet ./...
go test -race ./...                       # toute la suite, avant chaque commit
go build -o bin/board ./cmd/board         # le binaire (bin/ est ignoré par git)
BOARD_PREVIEW=1 go test -v ./internal/server -run TestPreview
                                          # page de démonstration sur 127.0.0.1:7393, imprime un lien de connexion
```

Pour regarder la page, ouvre le lien de prévisualisation dans Chrome headless et fais une capture. Vérifie le thème clair, le thème sombre et une largeur de téléphone.

## Règles dures

1. **Rien de non rétrocompatible.** C'est la seule vraie règle du projet, détaillée surface par surface dans [CONTRIBUTING.md](CONTRIBUTING.md) : base, `config.toml`, outils MCP, commande, routes `/api`, installation, comportement par défaut. Ajouter, oui. Renommer, retirer ou changer un sens ou un défaut, jamais.
2. **Aucune faille sur la machine.** Ne jamais affaiblir :
   - l'écoute sur loopback seulement ;
   - le jeton ;
   - `requireHost` et `http.NewCrossOriginProtection` ;
   - le cookie de la page, qui n'atteint jamais `/mcp` ;
   - le jeton CSRF sur toute requête de la page autre que GET ;
   - la CSP stricte : aucun script ni style en ligne, aucune ressource externe ;
   - le DOM construit par `textContent` dans `app.js` ;
   - les arguments d'`osascript` passés après `--` ;
   - le SQL paramétré ;
   - l'absence de tout appel réseau sortant.
3. **La parole du propriétaire suffit.** Aucun geste sur le chemin par défaut : ni clic de confirmation, ni Touch ID, ni file d'attente. Les verrous plus forts (`queue`, `touch_id`) restent des options désactivées.
4. **Tout est réglable.** Une nouvelle règle devient une clé de `config.toml`, dont le défaut reproduit le comportement actuel. Il faut alors toucher :
   - `Template`, `Parse`, `Validate` et `Diff` dans `internal/config/config.go` ;
   - les tests. `TestTemplateParsesToTheDefaults` impose que le modèle décrive exactement les défauts.
5. **Empreinte minimale.** Un seul démon, environ 20 Mo. Rien ne sonde en boucle : la page est poussée par SSE, et les échéances, les sauvegardes et la relecture des réglages ont leur minuteur.
6. **Bug, puis test reproducteur, puis correctif**, toujours dans cet ordre.
7. **Langues.** Le code, les commentaires et les commits sont en anglais. L'interface, la commande, les notifications et la documentation sont en français, sans tiret cadratin.
8. **Dépôt public : aucun nom réel.** Pas de client, de collègue ni de projet réel dans le code, les tests, la doc ou les commits. Les exemples utilisent `atlas`, `orion`, `zephyr` ou `perso`.
9. **La machine du propriétaire.** Plusieurs gestes demandent son accord :
   - lire ou modifier `~/.board` : ses règles deny l'interdisent aux sessions ;
   - lancer `board install` ou `board uninstall` : cela redémarre son démon et déconnecte sa page.

   `board ok`, `board refuse`, `board open` et `board unlock` sont ses commandes à lui, et le démon les refuse à une session.

## Carte du code

| Chemin | Rôle |
|---|---|
| `cmd/board/main.go`, `cli.go`, `approvals.go` | la commande : client MCP et `/api`, `BOARD_AS`, `board wait` et ses codes de sortie |
| `cmd/board/daemon.go` | le démarrage du démon : base, réglages, serveur, notifieur, relecture des réglages, sauvegardes |
| `cmd/board/install.go` | le LaunchAgent, l'enregistrement du MCP, `restartAgent` |
| `cmd/board/settings.go`, `backup.go` | la relecture de `config.toml` toutes les 3 s, la sauvegarde quotidienne |
| `internal/board/` | le domaine : le service (`plan`, `decide`, `apply` dans une transaction), la politique des modes, les requêtes, le schéma et les migrations |
| `internal/server/` | le HTTP : outils MCP (`tools.go`), identification, page et routes (`web.go`, `api.go`), notifications (`notify.go`), instructions des sessions (`instructions.md`) |
| `internal/server/web/` | la page : HTML, JS sans build, CSS, icône, embarqués dans le binaire |
| `internal/identity/` | l'appelant identifié par le système : port TCP, PID, ancêtre `claude` |
| `internal/config/` | `config.toml` : modèle, lecture stricte, validation, différences en français |
| `internal/touchid/`, `internal/notify/` | Touch ID (option), notifications macOS |
| `skills/use-board/` | le skill d'onboarding des sessions |
| `spike/` | l'essai technique du départ, pas compilé dans le binaire |

## Recettes

- **Ajouter un outil MCP.** Il y a six endroits à toucher :
  - **le serveur :** dans `tools.go`, la structure d'entrée avec ses tags `jsonschema`, le paramètre `as`, et `owner_asked` si l'outil écrit ;
  - **le domaine :** la méthode du service ;
  - **la commande :** l'équivalent dans `cli.go` ;
  - **les sessions :** une ligne dans `instructions.md`, qui ne doit pas dépasser 4096 caractères (un test le vérifie) ;
  - **le skill :** le tableau de `skills/use-board/SKILL.md` ;
  - **la conception :** le tableau des outils de DESIGN.md.
- **Ajouter une action gouvernée.** Déclare l'`Action` et ajoute-la à `allActions` dans `policy.go`. Donne-lui une règle dans chaque mode de `DefaultSettings`, et liste-la dans le `Template` des réglages (un test le vérifie). Puis traite-la dans `plan` et `apply`.
- **Changer le schéma.** Ajoute une entrée à la fin de `migrations` dans `store.go`. Ne modifie jamais une entrée publiée ni `schema.sql`.
- **Toucher la page.** `app.js` est du JS vanilla sans build, et tout passe par `h()` et `textContent`. Les couleurs sont des variables CSS, claires et sombres. Un champ de saisie porte la classe `comment`, pour qu'un rafraîchissement n'efface pas ce que le propriétaire tape.
- **Ajouter une route de la page.** Déclare-la dans `web.go`. Une mutation passe par `ownerOnly`, et `authenticate` impose le jeton CSRF pour toute requête de la page autre que GET.

## Pièges

- `launchctl bootout` rend la main avant que launchd ait déchargé le service. `restartAgent` attend puis réessaie : ne pas le simplifier.
- Les sessions de la page vivent en mémoire : chaque redémarrage du démon la déconnecte.
- La base est verrouillée en exclusif : `sqlite3` ne peut pas l'ouvrir tant que le démon tourne, et c'est voulu. Les tests travaillent sur des bases temporaires.
- Dans les tests, `portIdentities` associe les ports TCP aux identités. Le vrai résolveur exige un ancêtre `claude` sous `claude_prefixes`.
- `config.toml` est lu strictement : une clé inconnue fait rejeter tout le fichier, et le démon garde alors ses réglages.
- La prévisualisation écoute sur 7393 (variable `BOARD_PREVIEW_PORT`), le vrai démon sur 7391.

## Commits

Atomiques, au format `type(scope): résumé` en anglais, et chacun passe `go test -race ./...`. Pas de push sans demande du propriétaire.
