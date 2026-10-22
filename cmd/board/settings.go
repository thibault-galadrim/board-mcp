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
	Notify(title, subtitle, body string)
	SetOptions(server.Options)
}

type settingsBoard interface {
	SetSettings(board.Settings) error
}

// configWatcher applies config.toml edits made while the daemon runs and
// tells the owner what changed. An invalid content is reported once and
// ignored until the file changes again.
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
			w.check()
		}
	}
}

func (w *configWatcher) check() {
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
	if err := w.board.SetSettings(next.Board); err != nil {
		w.app.Notify("Board : réglages refusés", "config.toml", err.Error())
		return
	}
	w.app.SetOptions(next.Server)
	w.current = next
	w.app.Notify("Board : réglages appliqués", "config.toml", strings.Join(lines, " ; "))
}
