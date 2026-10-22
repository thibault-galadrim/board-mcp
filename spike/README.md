# Essai technique - 22/10/2026

Machine : macOS, Go 1.26.5, Claude Code 2.1.308, SDK MCP Go officiel v1.8.0, `modernc.org/sqlite` v1.61.0.

| # | Question | Résultat |
|---|---|---|
| 1 | Touch ID depuis un binaire Go | **Oui.** Ça marche depuis un terminal et depuis un LaunchAgent, sans application et avec la signature ad hoc par défaut. Validation et refus (« Authentication canceled. ») remontent correctement. Délai géré par `invalidate`. (`touchid/`) |
| 2a | MCP HTTP avec jeton | **Oui.** `claude mcp add --transport http ... --header "Authorization: Bearer ..."`. Sans jeton : 401. Avec un `Host` étranger : 403 (protection DNS rebinding intégrée au SDK). Avec une autre `Origin` : 403 (`http.NewCrossOriginProtection`). Les instructions du serveur arrivent bien dans le contexte du modèle (mot repère retrouvé). (`mcphttp/`) |
| 2b | Redémarrage du démon en cours de session | **Transparent.** Claude Code reçoit un 404 sur sa session périmée, se ré-initialise tout seul, et l'appel d'outil passe sans `/mcp`. Pas besoin du mode sans état. |
| 2c | Identifier la session appelante | **Oui, par le système.** À partir du port source de la connexion, on retrouve le process (`lsof`), puis on remonte jusqu'au `claude` ancêtre. Un `curl` lancé par un agent est rattaché à sa propre session (`curl < zsh < claude:<pid>`). Un en-tête d'identité falsifié est écrasé. À faire en production : passer par `libproc` plutôt que `lsof`, avec un cache par connexion. |
| 3 | Interdire `~/.board` aux agents | **Partiel sans sandbox.** Les règles deny `Read(~/.board/**)` (qui bloquent aussi Edit/Write) couvrent les outils fichiers et les commandes reconnues (`cat`, `sed`, redirections), mais pas `sqlite3` ni un script Python. Le sandbox de Claude Code couvre tout (`filesystem.denyRead/denyWrite`, `allowUnsandboxedCommands: false`), mais il s'appliquerait à toutes les commandes de toutes les sessions. **Parade légère vérifiée :** le démon tient la base en `locking_mode=EXCLUSIVE`. Tant qu'il tourne, `sqlite3` en lecture comme en écriture, et Python, reçoivent « database is locked ». (`sqlitelock/`) |
| 4 | Clé de scellement dans le Trousseau | **Abandonné, sans essai.** Pour qu'un binaire recompilé garde l'accès, il faut une identité de signature stable. Si `codesign` peut l'utiliser sans demande, un agent signe son propre programme et lit la clé. Sinon, la sécurité repose sur un clic de l'utilisateur, pas mieux qu'un Touch ID. |

Limites documentées utiles pour la suite : instructions du serveur coupées à 4 096 caractères ; avertissement au-delà de 10 000 tokens de résultat, plafond par défaut à 25 000 ; reconnexion automatique jusqu'à 5 fois.
