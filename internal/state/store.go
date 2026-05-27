package state

import (
	"database/sql"
	"fmt"

	"mirrors/internal/config"

	_ "github.com/mattn/go-sqlite3"
)

// LoadMirrorConfig loads the normalized mirror config stored in a per-mirror DB.
func LoadMirrorConfig(dbPath string) (config.Mirror, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return config.Mirror{}, err
	}
	defer func() {
		_ = db.Close()
	}()

	var values config.Values
	var mergeValue string
	err = db.QueryRow(`
SELECT name, url, dist, release, origin, label, arch, components, path, merge, server
FROM mirror
LIMIT 1
`).Scan(
		&values.Name,
		&values.URL,
		&values.Dist,
		&values.Release,
		&values.Origin,
		&values.Label,
		&values.Arch,
		&values.Components,
		&values.Path,
		&mergeValue,
		&values.Server,
	)
	if err != nil {
		return config.Mirror{}, fmt.Errorf("load mirror config from %s: %w", dbPath, err)
	}

	merge, err := config.ParseMerge(mergeValue)
	if err != nil {
		return config.Mirror{}, err
	}
	values.Merge = merge

	cfg := config.FromValues(values)
	if err := config.Validate(cfg); err != nil {
		return config.Mirror{}, err
	}
	return cfg, nil
}
