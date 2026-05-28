package state

import (
	"database/sql"
	"fmt"
)

const latestSchemaVersion = 2

type migration struct {
	version int
	sql     string
}

var migrations = []migration{
	{
		version: 1,
		sql: `
CREATE TABLE IF NOT EXISTS mirror (
	name TEXT NOT NULL,
	url TEXT NOT NULL,
	dist TEXT NOT NULL,
	release TEXT NOT NULL,
	origin TEXT NOT NULL,
	label TEXT NOT NULL,
	arch TEXT NOT NULL,
	components TEXT NOT NULL,
	path TEXT NOT NULL,
	merge TEXT NOT NULL,
	server TEXT NOT NULL,
	created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS upstream_indexes (
	path TEXT PRIMARY KEY,
	size INTEGER NOT NULL,
	md5 TEXT NOT NULL,
	sha1 TEXT NOT NULL,
	sha256 TEXT NOT NULL,
	sha512 TEXT NOT NULL,
	fetched_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS packages (
	package_key TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	version TEXT NOT NULL,
	architecture TEXT NOT NULL,
	filename TEXT NOT NULL,
	component TEXT NOT NULL,
	source TEXT NOT NULL,
	size INTEGER NOT NULL,
	md5 TEXT NOT NULL,
	sha1 TEXT NOT NULL,
	sha256 TEXT NOT NULL,
	sha512 TEXT NOT NULL,
	pool_path TEXT NOT NULL UNIQUE,
	created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS mirror_packages (
	package_key TEXT PRIMARY KEY,
	added_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
	FOREIGN KEY(package_key) REFERENCES packages(package_key) ON DELETE RESTRICT
);

CREATE TABLE IF NOT EXISTS snapshots (
	name TEXT PRIMARY KEY,
	kind TEXT NOT NULL,
	created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS snapshot_packages (
	snapshot_name TEXT NOT NULL,
	package_key TEXT NOT NULL,
	added_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
	PRIMARY KEY(snapshot_name, package_key),
	FOREIGN KEY(snapshot_name) REFERENCES snapshots(name) ON DELETE CASCADE,
	FOREIGN KEY(package_key) REFERENCES packages(package_key) ON DELETE RESTRICT
);

CREATE TABLE IF NOT EXISTS published (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	snapshot_name TEXT NOT NULL,
	path TEXT NOT NULL,
	suite TEXT NOT NULL,
	component TEXT NOT NULL,
	published_at TEXT NOT NULL,
	hidden INTEGER NOT NULL DEFAULT 0,
	FOREIGN KEY(snapshot_name) REFERENCES snapshots(name) ON DELETE RESTRICT
);

CREATE TABLE IF NOT EXISTS update_history (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	action TEXT NOT NULL,
	status TEXT NOT NULL,
	message TEXT NOT NULL,
	started_at TEXT NOT NULL,
	finished_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS cleanup_refs (
	pool_path TEXT NOT NULL,
	ref_type TEXT NOT NULL,
	ref_name TEXT NOT NULL,
	created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
	PRIMARY KEY(pool_path, ref_type, ref_name)
);

CREATE INDEX IF NOT EXISTS packages_pool_path_idx ON packages(pool_path);
CREATE INDEX IF NOT EXISTS packages_identity_idx ON packages(name, version, architecture);
CREATE INDEX IF NOT EXISTS snapshot_packages_package_idx ON snapshot_packages(package_key);
CREATE INDEX IF NOT EXISTS cleanup_refs_pool_path_idx ON cleanup_refs(pool_path);
`,
	},
	{
		version: 2,
		sql: `
ALTER TABLE packages ADD COLUMN fields_json TEXT NOT NULL DEFAULT '{}';
ALTER TABLE snapshot_packages ADD COLUMN fields_json TEXT NOT NULL DEFAULT '{}';

CREATE TABLE IF NOT EXISTS upstream_releases (
	suite TEXT PRIMARY KEY,
	origin TEXT NOT NULL,
	label TEXT NOT NULL,
	fetched_at TEXT NOT NULL
);
`,
	},
}

func migrate(db *sql.DB) error {
	if _, err := db.Exec(`
CREATE TABLE IF NOT EXISTS schema_migrations (
	version INTEGER PRIMARY KEY,
	applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
`); err != nil {
		return err
	}

	applied, err := appliedVersions(db)
	if err != nil {
		return err
	}

	for _, migration := range migrations {
		if applied[migration.version] {
			continue
		}
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migration.sql); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %d: %w", migration.version, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations(version) VALUES (?)`, migration.version); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %d: %w", migration.version, err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}

	return nil
}

func appliedVersions(db *sql.DB) (map[int]bool, error) {
	rows, err := db.Query(`SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	versions := map[int]bool{}
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			return nil, err
		}
		versions[version] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return versions, nil
}
