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
  board add [-p projet] [-P 0-3] [-d AAAA-MM-JJ] [-m détails] [-r] [-t] <titre>
                                       -r rappel pour toi, -t directement à faire
  board note <id> [-s état] [-l lien] <texte>
  board claim <id> -for <session>      donne un ticket à faire à une session
  board assign <id> [-to session] -r <motif>   passe ou relâche un ticket

Environnement : BOARD_HOME (défaut ~/.board), BOARD_ADDR (défaut 127.0.0.1:7391).
`

func main() {
	if len(os.Args) < 2 || os.Args[1] == "help" || os.Args[1] == "-h" || os.Args[1] == "--help" {
		fmt.Print(usage)
		return
	}
	var err error
	if os.Args[1] == "daemon" {
		err = runDaemon(os.Args[2:])
	} else {
		err = runCLI(os.Args[1], os.Args[2:])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "board:", err)
		os.Exit(1)
	}
}

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

func readToken(home string) (string, error) {
	b, err := os.ReadFile(tokenPath(home))
	if errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("no token in %s: is the daemon installed? (board daemon)", home)
	}
	return strings.TrimSpace(string(b)), err
}
