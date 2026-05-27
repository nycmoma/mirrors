package state

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestLoadMirrorConfig(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "ubuntu.sqlite")
	createMirrorDB(t, dbPath)

	cfg, err := LoadMirrorConfig(dbPath)
	if err != nil {
		t.Fatalf("LoadMirrorConfig returned error: %v", err)
	}
	if cfg.Name != "ubuntu" {
		t.Fatalf("unexpected name: %q", cfg.Name)
	}
	if len(cfg.Dists) != 2 || cfg.Dists[0] != "focal" || cfg.Dists[1] != "jammy" {
		t.Fatalf("unexpected dists: %#v", cfg.Dists)
	}
	if !cfg.Merge.Enabled || cfg.Merge.Depth != 2 {
		t.Fatalf("unexpected merge: %#v", cfg.Merge)
	}
}

func createMirrorDB(t *testing.T, dbPath string) {
	t.Helper()
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer func() {
		_ = db.Close()
	}()

	_, err = db.Exec(`
CREATE TABLE mirror (
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
	server TEXT NOT NULL
);
INSERT INTO mirror (
	name, url, dist, release, origin, label, arch, components, path, merge, server
) VALUES (
	'ubuntu',
	'http://us.archive.ubuntu.com/ubuntu/',
	'focal, jammy',
	'default, updates',
	'Ubuntu',
	'Ubuntu',
	'amd64',
	'main, restricted',
	'preprod',
	'2',
	'http://mirror.example.test'
);
`)
	if err != nil {
		t.Fatalf("Exec returned error: %v", err)
	}
}
