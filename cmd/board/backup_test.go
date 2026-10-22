package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

type fakeBackup struct{ calls int }

func (f *fakeBackup) Backup(_ context.Context, path string) error {
	f.calls++
	if _, err := os.Stat(path); err == nil {
		return errors.New("exists")
	}
	return os.WriteFile(path, []byte("copy"), 0o600)
}

func TestOneBackupPerDayAndSevenKept(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "backups")
	svc := &fakeBackup{}
	day := time.Date(2026, 10, 1, 12, 0, 0, 0, time.Local)
	for i := range 10 {
		for range 3 {
			if err := backupToday(context.Background(), svc, dir, day.AddDate(0, 0, i)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if svc.calls != 10 {
		t.Fatalf("one copy per day, got %d", svc.calls)
	}
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	want := []string{"board-2026-10-04.db", "board-2026-10-05.db", "board-2026-10-06.db", "board-2026-10-07.db",
		"board-2026-10-08.db", "board-2026-10-09.db", "board-2026-10-10.db"}
	if !slices.Equal(names, want) {
		t.Fatalf("got %v", names)
	}
}
