package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"board/internal/board"
	"board/internal/identity"
	"board/internal/server"
)

func runDaemon(args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("daemon takes no arguments; use BOARD_HOME and BOARD_ADDR")
	}
	// Everything the daemon creates (database, WAL, token) is owner-only.
	syscall.Umask(0o077)
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	home, err := boardHome()
	if err != nil {
		return err
	}
	addr, err := boardAddr()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(home, 0o700); err != nil {
		return err
	}
	token, err := ensureToken(home)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	db, err := board.OpenDB(ctx, filepath.Join(home, "board.db"))
	if err != nil {
		if strings.Contains(err.Error(), "locked") {
			return fmt.Errorf("the database is held by another process (another board daemon?): %w", err)
		}
		return err
	}
	defer db.Close()

	resolver := identity.LibprocResolver{ClaudePrefixes: claudePrefixes()}
	svc, err := board.NewService(db, board.DefaultSettings(), resolver, nil)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	srv := server.New(svc, resolver, token, log).HTTPServer(ln)
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	log.Info("board daemon listening", "addr", ln.Addr().String(), "home", home, "claude_prefixes", resolver.ClaudePrefixes)

	select {
	case <-ctx.Done():
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(shutdown)
}

// claudePrefixes lists where trusted Claude Code executables live.
func claudePrefixes() []string {
	if v := os.Getenv("BOARD_CLAUDE_PREFIXES"); v != "" {
		return strings.Split(v, ":")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []string{filepath.Join(home, ".local/share/claude/versions") + "/"}
}

func ensureToken(home string) (string, error) {
	path := tokenPath(home)
	if b, err := os.ReadFile(path); err == nil {
		if err := os.Chmod(path, 0o600); err != nil {
			return "", err
		}
		if t := strings.TrimSpace(string(b)); t != "" {
			return t, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return "", err
	}
	return token, nil
}
