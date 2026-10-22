package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const (
	backupsKept       = 7
	backupCheckEvery  = time.Hour
	backupPrefix      = "board-"
	backupSuffix      = ".db"
	backupPendingName = ".pending.db"
)

type backupper interface {
	Backup(ctx context.Context, path string) error
}

// runBackups keeps one consistent copy of the database per day.
func runBackups(ctx context.Context, svc backupper, dir string, log *slog.Logger) {
	tick := time.NewTicker(backupCheckEvery)
	defer tick.Stop()
	for {
		if err := backupToday(ctx, svc, dir, time.Now()); err != nil {
			log.Error("backup failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// backupToday writes today's copy if it is missing, then keeps only the
// most recent ones. The copy goes to a temporary name first, so an
// interrupted backup never passes for a complete one.
func backupToday(ctx context.Context, svc backupper, dir string, now time.Time) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	final := filepath.Join(dir, backupPrefix+now.Format(time.DateOnly)+backupSuffix)
	if _, err := os.Stat(final); err == nil {
		return prune(dir)
	}
	pending := filepath.Join(dir, backupPendingName)
	_ = os.Remove(pending)
	if err := svc.Backup(ctx, pending); err != nil {
		return err
	}
	if err := os.Rename(pending, final); err != nil {
		return err
	}
	return prune(dir)
}

func prune(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var copies []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), backupPrefix) && strings.HasSuffix(e.Name(), backupSuffix) {
			copies = append(copies, e.Name())
		}
	}
	slices.Sort(copies)
	for len(copies) > backupsKept {
		if err := os.Remove(filepath.Join(dir, copies[0])); err != nil {
			return err
		}
		copies = copies[1:]
	}
	return nil
}
