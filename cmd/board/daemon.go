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
	"board/internal/config"
	"board/internal/identity"
	"board/internal/notify"
	"board/internal/server"
	"board/internal/touchid"
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

	userHome, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	notifier := notify.MacOS{Log: log}
	cfgPath := filepath.Join(home, "config.toml")
	cfg, applied, err := config.Load(cfgPath, userHome)
	if err != nil {
		// Fall back on the defaults rather than refuse to start: launchd
		// would restart a failing daemon in a loop. The next valid edit is
		// proposed to the owner like any other.
		log.Error("config.toml is invalid, running on defaults", "err", err)
		notifier.Notify("Board : config.toml invalide", "réglages par défaut appliqués", err.Error())
		cfg = config.Defaults(userHome)
	}

	resolver := identity.LibprocResolver{ClaudePrefixes: cfg.ClaudePrefixes}
	svc, err := board.NewService(db, cfg.Board, resolver, nil)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	approver := touchid.New(cfg.BiometricsOnly)
	app := server.New(svc, resolver, approver, notifier, token, cfg.Server, log)
	srv := app.HTTPServer(ln)
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	go app.Run(ctx)
	watcher := &configWatcher{path: cfgPath, home: userHome, current: cfg, seen: applied, app: app, board: svc}
	go watcher.run(ctx)
	go runBackups(ctx, svc, filepath.Join(home, "backups"), log)
	log.Info("board daemon listening", "addr", ln.Addr().String(), "home", home, "claude_prefixes", resolver.ClaudePrefixes, "proof", approver.Proof())
	go func() {
		unlocked, err := app.Unlock(ctx)
		switch {
		case err != nil:
			log.Error("unlock prompt failed; run `board unlock`", "err", err)
		case !unlocked:
			log.Warn("board stays locked until `board unlock`")
		}
	}()

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
