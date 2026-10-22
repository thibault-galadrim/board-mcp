package main

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const agentLabel = "dev.board.daemon"

func agentPlistPath(home string) string {
	return filepath.Join(home, "Library", "LaunchAgents", agentLabel+".plist")
}

func xmlText(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// agentPlist runs the daemon in the owner's GUI session (Touch ID needs
// it) and restarts it if it dies.
func agentPlist(bin, boardHome, addr string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + agentLabel + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + xmlText(bin) + `</string>
		<string>daemon</string>
	</array>
	<key>EnvironmentVariables</key>
	<dict>
		<key>BOARD_HOME</key>
		<string>` + xmlText(boardHome) + `</string>
		<key>BOARD_ADDR</key>
		<string>` + xmlText(addr) + `</string>
	</dict>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>ThrottleInterval</key>
	<integer>10</integer>
	<key>LimitLoadToSessionType</key>
	<string>Aqua</string>
	<key>Umask</key>
	<integer>63</integer>
	<key>StandardOutPath</key>
	<string>` + xmlText(filepath.Join(boardHome, "daemon.log")) + `</string>
	<key>StandardErrorPath</key>
	<string>` + xmlText(filepath.Join(boardHome, "daemon.log")) + `</string>
</dict>
</plist>
`
}

func run(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func copyExecutable(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// runInstall puts board in ~/.local/bin, starts the daemon at login and
// registers the MCP server for every Claude Code session.
func runInstall() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	boardDir, err := boardHome()
	if err != nil {
		return err
	}
	addr, err := boardAddr()
	if err != nil {
		return err
	}
	claude, err := exec.LookPath("claude")
	if err != nil {
		return errors.New("claude CLI not found in PATH")
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return err
	}
	bin := filepath.Join(home, ".local", "bin", "board")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		return err
	}
	if exe != bin {
		if err := copyExecutable(exe, bin); err != nil {
			return fmt.Errorf("install %s: %w", bin, err)
		}
	}

	if err := os.MkdirAll(boardDir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(boardDir, 0o700); err != nil {
		return err
	}
	token, err := ensureToken(boardDir)
	if err != nil {
		return err
	}

	plist := agentPlistPath(home)
	if err := os.MkdirAll(filepath.Dir(plist), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(plist, []byte(agentPlist(bin, boardDir, addr)), 0o644); err != nil {
		return err
	}
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	_ = run("launchctl", "bootout", domain+"/"+agentLabel)
	if err := run("launchctl", "bootstrap", domain, plist); err != nil {
		return err
	}

	_ = run(claude, "mcp", "remove", "--scope", "user", "board")
	if err := run(claude, "mcp", "add", "--scope", "user", "--transport", "http", "board",
		"http://"+addr+"/mcp", "--header", "Authorization: Bearer "+token); err != nil {
		return err
	}

	fmt.Printf(`board installé :
  binaire       %s
  données       %s (réglages : config.toml)
  démon         %s, lancé à chaque ouverture de session
  MCP           « board » enregistré pour toutes tes sessions Claude

Une invite Touch ID va te demander de déverrouiller le board.
Les nouvelles sessions Claude voient le board ; une session déjà ouverte le verra une fois relancée.
`, bin, boardDir, plist)
	return nil
}

func runUninstall() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	_ = run("launchctl", "bootout", domain+"/"+agentLabel)
	if err := os.Remove(agentPlistPath(home)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if claude, err := exec.LookPath("claude"); err == nil {
		_ = run(claude, "mcp", "remove", "--scope", "user", "board")
	}
	boardDir, _ := boardHome()
	fmt.Printf("board désinstallé : démon arrêté, MCP retiré. Tes données restent dans %s et le binaire dans ~/.local/bin/board.\n", boardDir)
	return nil
}
