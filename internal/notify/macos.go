// Package notify shows macOS notifications.
package notify

import (
	"context"
	"log/slog"
	"os/exec"
	"time"
)

// The text travels as run-handler arguments after "--": osascript never
// parses or evaluates it, whatever a ticket title contains.
var script = []string{
	"-e", "on run argv",
	"-e", "display notification (item 3 of argv) with title (item 1 of argv) subtitle (item 2 of argv)",
	"-e", "end run",
	"--",
}

type MacOS struct {
	Log *slog.Logger
}

func (m MacOS) Notify(title, subtitle, body string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		args := append(append([]string{}, script...), title, subtitle, body)
		if out, err := exec.CommandContext(ctx, "/usr/bin/osascript", args...).CombinedOutput(); err != nil {
			m.Log.Warn("notification failed", "err", err, "output", string(out))
		}
	}()
}
