package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"time"

	"board/internal/board"
	"board/internal/config"
	"board/internal/server"
)

const configCheckEvery = 3 * time.Second

type settingsApp interface {
	ConfirmSettings(ctx context.Context, reason string) (bool, error)
	Notify(title, subtitle, body string)
	SetOptions(server.Options)
}

type settingsBoard interface {
	SetSettings(board.Settings) error
}

// configWatcher applies config.toml edits made while the daemon runs, once
// the owner approves the difference with Touch ID. A refused or invalid
// content is not proposed again until the file changes.
type configWatcher struct {
	path, home string
	current    config.Config
	seen       []byte
	app        settingsApp
	board      settingsBoard
}

func (w *configWatcher) run(ctx context.Context) {
	tick := time.NewTicker(configCheckEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			w.check(ctx)
		}
	}
}

func (w *configWatcher) check(ctx context.Context) {
	data, err := os.ReadFile(w.path)
	if err != nil || bytes.Equal(data, w.seen) {
		return
	}
	w.seen = data
	next, err := config.Parse(data, w.home)
	if err != nil {
		w.app.Notify("Board : config.toml invalide", "modification ignorée", err.Error())
		return
	}
	lines := config.Diff(w.current, next)
	if len(lines) == 0 {
		return
	}
	approved, err := w.app.ConfirmSettings(ctx, "appliquer les réglages : "+strings.Join(lines, " ; "))
	if err != nil || !approved {
		w.app.Notify("Board : réglages non appliqués", "config.toml", "le fichier diffère des réglages en cours ; modifie-le à nouveau pour les reproposer")
		return
	}
	if err := w.board.SetSettings(next.Board); err != nil {
		w.app.Notify("Board : réglages refusés", "config.toml", err.Error())
		return
	}
	w.app.SetOptions(next.Server)
	w.current = next
	w.app.Notify("Board : réglages appliqués", "config.toml", strings.Join(lines, " ; "))
}
