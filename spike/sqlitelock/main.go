// Spike: while the daemon holds the database in EXCLUSIVE locking mode, can
// another process (sqlite3 CLI, a Python script) read or write it?
//
//	sqlitelock <db>   creates the db, takes the lock, holds it for 60 s
package main

import (
	"database/sql"
	"log"
	"os"
	"time"

	_ "modernc.org/sqlite"
)

func main() {
	db, err := sql.Open("sqlite", os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	for _, stmt := range []string{
		"PRAGMA locking_mode=EXCLUSIVE",
		"PRAGMA journal_mode=WAL",
		"CREATE TABLE IF NOT EXISTS tickets (id INTEGER PRIMARY KEY, title TEXT)",
		"INSERT INTO tickets (title) VALUES ('held by the daemon')",
	} {
		if _, err := db.Exec(stmt); err != nil {
			log.Fatalf("%s: %v", stmt, err)
		}
	}
	log.Printf("lock held, pid=%d", os.Getpid())
	time.Sleep(60 * time.Second)
}
