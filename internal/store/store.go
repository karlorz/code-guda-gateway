package store

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// Store wraps a SQLite database opened for the gateway control plane.
type Store struct {
	db *sql.DB
}

// DB returns the underlying database handle.
func (s *Store) DB() *sql.DB {
	return s.db
}

// Close closes the database connection.
func (s *Store) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Open opens dbPath, applies pragmas, and runs pending migrations.
// The parent directory must already exist.
func Open(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", sqliteDSN(dbPath))
	if err != nil {
		return nil, fmt.Errorf("sql open: %w", err)
	}

	if err := setPragmas(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	// One connection so a refresh transaction is not failed by a pooled SQLITE_BUSY.
	db.SetMaxOpenConns(1)

	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}

	return &Store{db: db}, nil
}

func sqliteDSN(dbPath string) string {
	// file: DSN so every pooled connection inherits the busy timeout.
	return "file:" + dbPath + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
}

func setPragmas(db *sql.DB) error {
	pragmas := []string{
		`PRAGMA journal_mode = WAL`,
		`PRAGMA busy_timeout = 5000`,
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			return fmt.Errorf("exec %s: %w", p, err)
		}
	}
	return nil
}
