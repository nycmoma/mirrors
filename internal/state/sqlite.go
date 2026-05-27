package state

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Store owns one per-mirror SQLite database.
type Store struct {
	db *sql.DB
}

// Tx wraps a transaction for multi-table workflow updates.
type Tx struct {
	tx *sql.Tx
}

// Open opens or creates a per-mirror SQLite database and runs migrations.
func Open(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("database path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite3", sqliteDSN(path))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}

	return &Store{db: db}, nil
}

// Close closes the underlying SQLite database.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// SchemaVersion returns the latest applied schema version.
func (s *Store) SchemaVersion() (int, error) {
	var version int
	err := s.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version)
	return version, err
}

// WithTx executes fn in a transaction and rolls back if fn returns an error.
func (s *Store) WithTx(fn func(*Tx) error) (err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	wrapped := &Tx{tx: tx}
	if err = fn(wrapped); err != nil {
		return err
	}
	err = tx.Commit()
	return err
}

func sqliteDSN(path string) string {
	u := url.URL{Scheme: "file", Path: path}
	values := url.Values{}
	values.Set("_foreign_keys", "1")
	u.RawQuery = values.Encode()
	return u.String()
}
