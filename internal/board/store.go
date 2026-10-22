package board

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"net/url"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schema string

// migrations upgrade a database from version i+1 to i+2; schema.sql is
// version 1. Never edit a shipped entry, append a new one.
var migrations = []string{
	`CREATE TABLE notified (
		ticket_id INTEGER NOT NULL REFERENCES tickets(id),
		kind TEXT NOT NULL,
		day TEXT NOT NULL,
		PRIMARY KEY (ticket_id, kind, day)
	) WITHOUT ROWID`,
}

var schemaVersion = len(migrations) + 1

// OpenDB opens the board database and takes an exclusive lock on it for the
// life of the process: no other process (sqlite3, a script) can read or
// write it while the daemon runs. A second daemon fails here too.
func OpenDB(ctx context.Context, path string) (*sql.DB, error) {
	dsn := "file:" + (&url.URL{Path: path}).EscapedPath() + "?_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection that never closes: the exclusive lock belongs to it,
	// and database/sql hands it to one transaction at a time.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	db.SetConnMaxIdleTime(0)

	for _, pragma := range []string{
		"PRAGMA locking_mode = EXCLUSIVE",
		"PRAGMA journal_mode = WAL",
		"PRAGMA foreign_keys = ON",
		"PRAGMA synchronous = NORMAL",
	} {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("%s: %w", pragma, err)
		}
	}
	if err := migrate(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func migrate(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("lock database: %w", err)
	}
	defer tx.Rollback()

	var version int
	err = tx.QueryRowContext(ctx, "SELECT CAST(value AS INTEGER) FROM meta WHERE key = 'schema_version'").Scan(&version)
	switch {
	case err == nil:
	case isMissingTable(err):
		if _, err := tx.ExecContext(ctx, schema); err != nil {
			return fmt.Errorf("create schema: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO meta (key, value) VALUES ('schema_version', '1')"); err != nil {
			return err
		}
		version = 1
	default:
		return err
	}
	if version > schemaVersion {
		return fmt.Errorf("database schema %d is newer than this binary (%d)", version, schemaVersion)
	}
	for v := version; v < schemaVersion; v++ {
		if _, err := tx.ExecContext(ctx, migrations[v-1]); err != nil {
			return fmt.Errorf("migrate to schema %d: %w", v+1, err)
		}
	}
	if _, err := tx.ExecContext(ctx, "UPDATE meta SET value = ? WHERE key = 'schema_version'", fmt.Sprint(schemaVersion)); err != nil {
		return err
	}
	return tx.Commit()
}

func isMissingTable(err error) bool {
	return err != nil && !errors.Is(err, sql.ErrNoRows) && strings.Contains(strings.ToLower(err.Error()), "no such table")
}
