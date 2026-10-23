// Command board is both the board daemon and the owner's command line.
package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
)

const defaultAddr = "127.0.0.1:7391"

const usage = `board - tableau partagé entre toi et tes sessions Claude

  board daemon                         lance le démon (premier plan)
  board projects                       liste les projets
  board project-add <slug> <nom>       demande la création d'un projet
  board ls [-p projet] [-s état] [-o session] [-k kind] [-a]
  board show <id>                      fiche et journal d'un ticket
  board news [-p projet]               quoi de neuf depuis ton dernier passage
  board add [-p projet] [-P 0-3] [-d AAAA-MM-JJ] [-m détails] [-r] [-t] [-doing] <titre>
                                       -r rappel pour toi, -t directement à faire,
                                       -doing déjà en cours (session seulement)
  board next -p <projet> [-for session]  prend la tâche libre la plus urgente
  board note <id> [-s état] [-l lien] <texte>
  board claim <id> -for <session>      donne un ticket à faire à une session
  board assign <id> [-to session] -r <motif>   passe ou relâche un ticket
  board move <id> -p <projet> [-r motif]       déplace un ticket (et ses sous-tickets)

  board requests                       demandes en attente de ta validation
  board ok <id>                        valide une demande en file (option queue)
  board refuse <id> [-r motif]         refuse une demande
  board wait <id> [-timeout 12h]       attend la décision (pour les sessions)
  board unlock                         déverrouille le board (option touch_id)
  board open                           ouvre la page du board dans le navigateur

  board config                         affiche ~/.board/config.toml
  board install                        installe le démon et le MCP pour toutes les sessions
  board uninstall                      les retire (tes données restent)

Une session Claude qui agit parce que tu le lui as demandé ajoute -asked :
ta parole suffit, l'action s'applique.

Environnement : BOARD_HOME (défaut ~/.board), BOARD_ADDR (défaut 127.0.0.1:7391),
BOARD_AS (nom de la session qui utilise la commande, défaut « cli »).
`

func main() {
	if len(os.Args) < 2 || os.Args[1] == "help" || os.Args[1] == "-h" || os.Args[1] == "--help" {
		fmt.Print(usage)
		return
	}
	var err error
	switch os.Args[1] {
	case "daemon":
		err = runDaemon(os.Args[2:])
	case "install":
		err = runInstall()
	case "uninstall":
		err = runUninstall()
	case "config":
		err = showConfig()
	default:
		err = runCLI(os.Args[1], os.Args[2:])
	}
	var exit exitError
	if errors.As(err, &exit) {
		fmt.Println(exit.msg)
		os.Exit(exit.code)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "board:", err)
		os.Exit(1)
	}
}

// exitError ends the command with a meaningful code, for scripts and for
// sessions waiting in the background.
type exitError struct {
	code int
	msg  string
}

func (e exitError) Error() string { return e.msg }

func boardHome() (string, error) {
	if h := os.Getenv("BOARD_HOME"); h != "" {
		return h, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".board"), nil
}

// boardAddr returns the listen address, refusing anything but loopback.
func boardAddr() (string, error) {
	addr := os.Getenv("BOARD_ADDR")
	if addr == "" {
		addr = defaultAddr
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("BOARD_ADDR %q: %w", addr, err)
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return "", fmt.Errorf("BOARD_ADDR must be a loopback IP address, got %q", host)
	}
	return addr, nil
}

func tokenPath(home string) string { return filepath.Join(home, "token") }

func showConfig() error {
	home, err := boardHome()
	if err != nil {
		return err
	}
	path := filepath.Join(home, "config.toml")
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%s: %w (the daemon writes it on first start)", path, err)
	}
	fmt.Printf("# %s\n%s", path, b)
	return nil
}

func readToken(home string) (string, error) {
	b, err := os.ReadFile(tokenPath(home))
	if errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("no token in %s: is the daemon installed? (board daemon)", home)
	}
	return strings.TrimSpace(string(b)), err
}
