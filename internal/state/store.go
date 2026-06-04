package state

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"mirrors/internal/config"

	_ "github.com/mattn/go-sqlite3"
)

// LoadMirrorConfig loads the normalized mirror config stored in a per-mirror DB.
func LoadMirrorConfig(dbPath string) (config.Mirror, error) {
	store, err := Open(dbPath)
	if err != nil {
		return config.Mirror{}, err
	}
	defer func() {
		_ = store.Close()
	}()

	cfg, err := store.MirrorConfig()
	if err != nil {
		return config.Mirror{}, fmt.Errorf("load mirror config from %s: %w", dbPath, err)
	}
	return cfg, nil
}

// SaveMirrorConfig replaces the one mirror config record stored in this DB.
func (s *Store) SaveMirrorConfig(cfg config.Mirror) error {
	return s.WithTx(func(tx *Tx) error {
		return tx.SaveMirrorConfig(cfg)
	})
}

// SaveMirrorConfig replaces the one mirror config record stored in this DB.
func (tx *Tx) SaveMirrorConfig(cfg config.Mirror) error {
	if err := config.Validate(cfg); err != nil {
		return err
	}

	if _, err := tx.tx.Exec(`DELETE FROM mirror`); err != nil {
		return err
	}
	_, err := tx.tx.Exec(`
INSERT INTO mirror (
	name, url, dist, release, origin, label, arch, components, path, merge,
	server, sign, gpg_home, gpg_key, gpg_passphrase, gpg_passphrase_file,
	config_path, update_policy
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`,
		cfg.Name,
		cfg.URL,
		strings.Join(cfg.Dists, ", "),
		strings.Join(cfg.Releases, ", "),
		cfg.Origin,
		cfg.Label,
		strings.Join(cfg.Arch, ", "),
		strings.Join(cfg.Components, ", "),
		cfg.Path,
		cfg.Merge.String(),
		cfg.Server,
		signValue(!cfg.Signing.Disabled),
		cfg.Signing.GPGHome,
		cfg.Signing.GPGKey,
		cfg.Signing.GPGPassphrase,
		cfg.Signing.GPGPassphraseFile,
		cfg.ConfigPath,
		cfg.UpdatePolicy,
	)
	return err
}

// MirrorConfig returns the one normalized mirror config record in this DB.
func (s *Store) MirrorConfig() (config.Mirror, error) {
	return scanMirrorConfig(s.db.QueryRow(`
SELECT name, url, dist, release, origin, label, arch, components, path, merge,
       server, sign, gpg_home, gpg_key, gpg_passphrase, gpg_passphrase_file,
       config_path, update_policy
FROM mirror
LIMIT 1
`))
}

// MirrorConfig returns the one normalized mirror config record in this transaction.
func (tx *Tx) MirrorConfig() (config.Mirror, error) {
	return scanMirrorConfig(tx.tx.QueryRow(`
SELECT name, url, dist, release, origin, label, arch, components, path, merge,
       server, sign, gpg_home, gpg_key, gpg_passphrase, gpg_passphrase_file,
       config_path, update_policy
FROM mirror
LIMIT 1
`))
}

func scanMirrorConfig(row interface {
	Scan(dest ...interface{}) error
}) (config.Mirror, error) {
	var values config.Values
	var mergeValue string
	var signValue string
	var configPath string
	var updatePolicy string
	err := row.Scan(
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
		&signValue,
		&values.GPGHome,
		&values.GPGKey,
		&values.GPGPassphrase,
		&values.GPGPassphraseFile,
		&configPath,
		&updatePolicy,
	)
	if err != nil {
		return config.Mirror{}, err
	}

	merge, err := config.ParseMerge(mergeValue)
	if err != nil {
		return config.Mirror{}, err
	}
	values.Merge = merge
	signing, err := config.ParseSigning(signValue)
	if err != nil {
		return config.Mirror{}, err
	}
	values.Signing = signing
	values.UpdatePolicy = updatePolicy

	cfg := config.FromValues(values)
	cfg.ConfigPath = configPath
	if err := config.Validate(cfg); err != nil {
		return config.Mirror{}, err
	}
	return cfg, nil
}

// UpsertPackage inserts or updates one package record.
func (s *Store) UpsertPackage(pkg PackageRecord) (string, error) {
	var key string
	err := s.WithTx(func(tx *Tx) error {
		var err error
		key, err = tx.UpsertPackage(pkg)
		return err
	})
	return key, err
}

// UpsertPackage inserts or updates one package record.
func (tx *Tx) UpsertPackage(pkg PackageRecord) (string, error) {
	key, err := normalizePackageKey(pkg)
	if err != nil {
		return "", err
	}
	fieldsJSON, err := encodeFields(pkg.Fields)
	if err != nil {
		return "", err
	}
	_, err = tx.tx.Exec(`
INSERT INTO packages (
	package_key, name, version, architecture, filename, component, source, size,
	md5, sha1, sha256, sha512, pool_path, fields_json, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(package_key) DO UPDATE SET
	name = excluded.name,
	version = excluded.version,
	architecture = excluded.architecture,
	filename = excluded.filename,
	component = excluded.component,
	source = excluded.source,
	size = excluded.size,
	md5 = excluded.md5,
	sha1 = excluded.sha1,
	sha256 = excluded.sha256,
	sha512 = excluded.sha512,
	pool_path = excluded.pool_path,
	fields_json = excluded.fields_json,
	updated_at = excluded.updated_at
`,
		key,
		pkg.Name,
		pkg.Version,
		pkg.Architecture,
		pkg.Filename,
		pkg.Component,
		pkg.Source,
		pkg.Size,
		pkg.MD5,
		pkg.SHA1,
		pkg.SHA256,
		pkg.SHA512,
		pkg.PoolPath,
		fieldsJSON,
		nowString(time.Now()),
	)
	if err != nil {
		return "", err
	}
	return key, nil
}

// Package returns one package record by key.
func (s *Store) Package(key string) (PackageRecord, error) {
	return scanPackage(s.db.QueryRow(`
SELECT package_key, name, version, architecture, filename, component, source, size,
       md5, sha1, sha256, sha512, pool_path, fields_json
FROM packages
WHERE package_key = ?
`, key))
}

// Packages returns package records for the provided package keys.
func (s *Store) Packages(keys []string) ([]PackageRecord, error) {
	var records []PackageRecord
	for _, key := range uniqueNonEmpty(keys) {
		record, err := s.Package(key)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

// AllPackages returns every known package record.
func (s *Store) AllPackages() ([]PackageRecord, error) {
	rows, err := s.db.Query(`
SELECT package_key, name, version, architecture, filename, component, source, size,
       md5, sha1, sha256, sha512, pool_path, fields_json
FROM packages
ORDER BY name, version, architecture, filename
`)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	var records []PackageRecord
	for rows.Next() {
		record, err := scanPackage(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

// SnapshotPackages returns package records and stanza fields captured for a snapshot.
func (s *Store) SnapshotPackages(snapshotName string) ([]PackageRecord, error) {
	rows, err := s.db.Query(`
SELECT p.package_key, p.name, p.version, p.architecture, p.filename, p.component, p.source, p.size,
       p.md5, p.sha1, p.sha256, p.sha512, p.pool_path, sp.fields_json
FROM snapshot_packages sp
JOIN packages p ON p.package_key = sp.package_key
WHERE sp.snapshot_name = ?
ORDER BY p.name, p.version, p.architecture, p.filename
`, snapshotName)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	var records []PackageRecord
	for rows.Next() {
		record, err := scanPackage(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

// PackageKey returns the state identity key for a package record.
func PackageKey(pkg PackageRecord) (string, error) {
	return normalizePackageKey(pkg)
}

// ReplaceMirrorPackages replaces the current upstream package membership set.
func (s *Store) ReplaceMirrorPackages(packageKeys []string) error {
	return s.WithTx(func(tx *Tx) error {
		return tx.ReplaceMirrorPackages(packageKeys)
	})
}

// ReplaceMirrorPackages replaces the current upstream package membership set.
func (tx *Tx) ReplaceMirrorPackages(packageKeys []string) error {
	if _, err := tx.tx.Exec(`DELETE FROM mirror_packages`); err != nil {
		return err
	}
	for _, key := range uniqueNonEmpty(packageKeys) {
		if _, err := tx.tx.Exec(`INSERT INTO mirror_packages(package_key) VALUES (?)`, key); err != nil {
			return err
		}
	}
	return nil
}

// MirrorPackageKeys returns the current mirror package membership set.
func (s *Store) MirrorPackageKeys() ([]string, error) {
	return queryStrings(s.db.Query(`SELECT package_key FROM mirror_packages ORDER BY package_key`))
}

// Stats returns package, snapshot, published, and last update summary data.
func (s *Store) Stats() (Stats, error) {
	var stats Stats
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM packages`).Scan(&stats.KnownPackageCount); err != nil {
		return Stats{}, err
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM mirror_packages`).Scan(&stats.MirrorPackageCount); err != nil {
		return Stats{}, err
	}
	if err := s.db.QueryRow(`
SELECT COALESCE(SUM(size), 0)
FROM (
	SELECT MAX(p.size) AS size
	FROM mirror_packages mp
	JOIN packages p ON p.package_key = mp.package_key
	GROUP BY CASE WHEN p.pool_path = '' THEN p.package_key ELSE p.pool_path END
)
`).Scan(&stats.MirrorSizeBytes); err != nil {
		return Stats{}, err
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM snapshots`).Scan(&stats.SnapshotCount); err != nil {
		return Stats{}, err
	}

	published, err := s.Published()
	if err == nil {
		stats.Published = &published
	} else if !isNoRows(err) {
		return Stats{}, err
	}

	lastUpdate, err := s.LastUpdate()
	if err == nil {
		stats.LastUpdate = &lastUpdate
	} else if !isNoRows(err) {
		return Stats{}, err
	}

	return stats, nil
}

// CreateSnapshot records a snapshot and its package membership.
func (s *Store) CreateSnapshot(snapshot SnapshotRecord, packageKeys []string) error {
	return s.WithTx(func(tx *Tx) error {
		return tx.CreateSnapshot(snapshot, packageKeys)
	})
}

// CreateSnapshot records a snapshot and its package membership.
func (tx *Tx) CreateSnapshot(snapshot SnapshotRecord, packageKeys []string) error {
	name := strings.TrimSpace(snapshot.Name)
	if name == "" {
		return fmt.Errorf("snapshot name is required")
	}
	kind := strings.TrimSpace(snapshot.Kind)
	if kind == "" {
		kind = "regular"
	}
	createdAt := snapshot.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}

	result, err := tx.tx.Exec(
		`INSERT OR IGNORE INTO snapshots(name, kind, created_at) VALUES (?, ?, ?)`,
		name,
		kind,
		nowString(createdAt),
	)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("snapshot %q already exists", name)
	}

	if err := tx.insertSnapshotPackages(name, packageKeys); err != nil {
		return err
	}
	return nil
}

// ReplaceSnapshot replaces a snapshot record and its package membership.
func (s *Store) ReplaceSnapshot(snapshot SnapshotRecord, packageKeys []string) error {
	return s.WithTx(func(tx *Tx) error {
		return tx.ReplaceSnapshot(snapshot, packageKeys)
	})
}

// ReplaceSnapshot replaces a snapshot record and its package membership.
func (tx *Tx) ReplaceSnapshot(snapshot SnapshotRecord, packageKeys []string) error {
	name := strings.TrimSpace(snapshot.Name)
	if name == "" {
		return fmt.Errorf("snapshot name is required")
	}
	kind := strings.TrimSpace(snapshot.Kind)
	if kind == "" {
		kind = "regular"
	}
	createdAt := snapshot.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	result, err := tx.tx.Exec(
		`UPDATE snapshots SET kind = ?, created_at = ? WHERE name = ?`,
		kind,
		nowString(createdAt),
		name,
	)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		if _, err := tx.tx.Exec(
			`INSERT INTO snapshots(name, kind, created_at) VALUES (?, ?, ?)`,
			name,
			kind,
			nowString(createdAt),
		); err != nil {
			return err
		}
	}
	if _, err := tx.tx.Exec(`DELETE FROM snapshot_packages WHERE snapshot_name = ?`, name); err != nil {
		return err
	}
	if err := tx.insertSnapshotPackages(name, packageKeys); err != nil {
		return err
	}
	return nil
}

// ReplaceSnapshotPackages replaces a snapshot using explicit package records.
func (s *Store) ReplaceSnapshotPackages(snapshot SnapshotRecord, packages []PackageRecord) error {
	return s.WithTx(func(tx *Tx) error {
		return tx.ReplaceSnapshotPackages(snapshot, packages)
	})
}

// ReplaceSnapshotPackages replaces a snapshot using explicit package records.
func (tx *Tx) ReplaceSnapshotPackages(snapshot SnapshotRecord, packages []PackageRecord) error {
	keys := make([]string, 0, len(packages))
	fieldsByKey := map[string]map[string]string{}
	for _, pkg := range packages {
		key, err := normalizePackageKey(pkg)
		if err != nil {
			return err
		}
		keys = append(keys, key)
		fieldsByKey[key] = pkg.Fields
	}
	if err := tx.ReplaceSnapshot(snapshot, keys); err != nil {
		return err
	}
	for _, key := range uniqueNonEmpty(keys) {
		fieldsJSON, err := encodeFields(fieldsByKey[key])
		if err != nil {
			return err
		}
		if _, err := tx.tx.Exec(
			`UPDATE snapshot_packages SET fields_json = ? WHERE snapshot_name = ? AND package_key = ?`,
			fieldsJSON,
			strings.TrimSpace(snapshot.Name),
			key,
		); err != nil {
			return err
		}
	}
	return nil
}

func (tx *Tx) insertSnapshotPackages(snapshotName string, packageKeys []string) error {
	for _, key := range uniqueNonEmpty(packageKeys) {
		result, err := tx.tx.Exec(
			`INSERT INTO snapshot_packages(snapshot_name, package_key, fields_json)
SELECT ?, package_key, fields_json FROM packages WHERE package_key = ?`,
			snapshotName,
			key,
		)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count == 0 {
			return fmt.Errorf("package %q does not exist", key)
		}
	}
	return nil
}

// SnapshotPackageKeys returns package membership for a snapshot.
func (s *Store) SnapshotPackageKeys(snapshotName string) ([]string, error) {
	return queryStrings(s.db.Query(`
SELECT package_key
FROM snapshot_packages
WHERE snapshot_name = ?
ORDER BY package_key
`, snapshotName))
}

// Snapshot returns one snapshot record by name.
func (s *Store) Snapshot(name string) (SnapshotRecord, error) {
	var record SnapshotRecord
	var createdAt string
	err := s.db.QueryRow(
		`SELECT name, kind, created_at FROM snapshots WHERE name = ?`,
		name,
	).Scan(&record.Name, &record.Kind, &createdAt)
	if err != nil {
		return SnapshotRecord{}, err
	}
	record.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return SnapshotRecord{}, err
	}
	return record, nil
}

// Snapshots returns all snapshot records ordered by creation time and name.
func (s *Store) Snapshots() ([]SnapshotRecord, error) {
	rows, err := s.db.Query(`
SELECT name, kind, created_at
FROM snapshots
ORDER BY created_at, name
`)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	var snapshots []SnapshotRecord
	for rows.Next() {
		var record SnapshotRecord
		var createdAt string
		if err := rows.Scan(&record.Name, &record.Kind, &createdAt); err != nil {
			return nil, err
		}
		parsed, err := parseTime(createdAt)
		if err != nil {
			return nil, err
		}
		record.CreatedAt = parsed
		snapshots = append(snapshots, record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return snapshots, nil
}

// DeleteSnapshots removes snapshots by name. Snapshot package membership is
// removed by the database foreign key cascade.
func (s *Store) DeleteSnapshots(names []string) error {
	return s.WithTx(func(tx *Tx) error {
		return tx.DeleteSnapshots(names)
	})
}

// DeleteSnapshots removes snapshots by name. Snapshot package membership is
// removed by the database foreign key cascade.
func (tx *Tx) DeleteSnapshots(names []string) error {
	for _, name := range names {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("snapshot name is required")
		}
		if _, err := tx.tx.Exec(`DELETE FROM snapshots WHERE name = ?`, name); err != nil {
			return err
		}
	}
	return nil
}

// SetPublished replaces the currently published state.
func (s *Store) SetPublished(record PublishedRecord) error {
	return s.WithTx(func(tx *Tx) error {
		return tx.SetPublished(record)
	})
}

// SetPublished replaces the currently published state.
func (tx *Tx) SetPublished(record PublishedRecord) error {
	if strings.TrimSpace(record.SnapshotName) == "" {
		return fmt.Errorf("published snapshot name is required")
	}
	publishedAt := record.PublishedAt
	if publishedAt.IsZero() {
		publishedAt = time.Now()
	}
	hidden := 0
	if record.Hidden {
		hidden = 1
	}

	_, err := tx.tx.Exec(`
INSERT INTO published (id, snapshot_name, path, suite, component, published_at, hidden)
VALUES (1, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	snapshot_name = excluded.snapshot_name,
	path = excluded.path,
	suite = excluded.suite,
	component = excluded.component,
	published_at = excluded.published_at,
	hidden = excluded.hidden
`,
		record.SnapshotName,
		record.Path,
		record.Suite,
		record.Component,
		nowString(publishedAt),
		hidden,
	)
	return err
}

// Published returns the current published state.
func (s *Store) Published() (PublishedRecord, error) {
	var record PublishedRecord
	var publishedAt string
	var hidden int
	err := s.db.QueryRow(`
SELECT snapshot_name, path, suite, component, published_at, hidden
FROM published
WHERE id = 1
`).Scan(
		&record.SnapshotName,
		&record.Path,
		&record.Suite,
		&record.Component,
		&publishedAt,
		&hidden,
	)
	if err != nil {
		return PublishedRecord{}, err
	}
	record.PublishedAt, err = parseTime(publishedAt)
	if err != nil {
		return PublishedRecord{}, err
	}
	record.Hidden = hidden != 0
	return record, nil
}

// RecordUpdateHistory inserts one workflow history record.
func (s *Store) RecordUpdateHistory(record UpdateRecord) (int64, error) {
	var id int64
	err := s.WithTx(func(tx *Tx) error {
		var err error
		id, err = tx.RecordUpdateHistory(record)
		return err
	})
	return id, err
}

// RecordUpdateHistory inserts one workflow history record.
func (tx *Tx) RecordUpdateHistory(record UpdateRecord) (int64, error) {
	startedAt := record.StartedAt
	if startedAt.IsZero() {
		startedAt = time.Now()
	}
	finishedAt := record.FinishedAt
	if finishedAt.IsZero() {
		finishedAt = startedAt
	}

	result, err := tx.tx.Exec(`
INSERT INTO update_history(action, status, message, started_at, finished_at)
VALUES (?, ?, ?, ?, ?)
`, record.Action, record.Status, record.Message, nowString(startedAt), nowString(finishedAt))
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// LastUpdate returns the most recent update history record.
func (s *Store) LastUpdate() (UpdateRecord, error) {
	var record UpdateRecord
	var startedAt string
	var finishedAt string
	err := s.db.QueryRow(`
SELECT id, action, status, message, started_at, finished_at
FROM update_history
ORDER BY id DESC
LIMIT 1
`).Scan(
		&record.ID,
		&record.Action,
		&record.Status,
		&record.Message,
		&startedAt,
		&finishedAt,
	)
	if err != nil {
		return UpdateRecord{}, err
	}
	record.StartedAt, err = parseTime(startedAt)
	if err != nil {
		return UpdateRecord{}, err
	}
	record.FinishedAt, err = parseTime(finishedAt)
	if err != nil {
		return UpdateRecord{}, err
	}
	return record, nil
}

// UpsertUpstreamIndex inserts or updates metadata fetched from upstream.
func (s *Store) UpsertUpstreamIndex(record UpstreamIndexRecord) error {
	return s.WithTx(func(tx *Tx) error {
		return tx.UpsertUpstreamIndex(record)
	})
}

// UpsertUpstreamIndex inserts or updates metadata fetched from upstream.
func (tx *Tx) UpsertUpstreamIndex(record UpstreamIndexRecord) error {
	if strings.TrimSpace(record.Path) == "" {
		return fmt.Errorf("upstream index path is required")
	}
	fetchedAt := record.FetchedAt
	if fetchedAt.IsZero() {
		fetchedAt = time.Now()
	}

	_, err := tx.tx.Exec(`
INSERT INTO upstream_indexes(path, size, md5, sha1, sha256, sha512, fetched_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(path) DO UPDATE SET
	size = excluded.size,
	md5 = excluded.md5,
	sha1 = excluded.sha1,
	sha256 = excluded.sha256,
	sha512 = excluded.sha512,
	fetched_at = excluded.fetched_at
`, record.Path, record.Size, record.MD5, record.SHA1, record.SHA256, record.SHA512, nowString(fetchedAt))
	return err
}

// UpsertUpstreamRelease inserts or updates upstream Release metadata.
func (s *Store) UpsertUpstreamRelease(record UpstreamReleaseRecord) error {
	return s.WithTx(func(tx *Tx) error {
		return tx.UpsertUpstreamRelease(record)
	})
}

// UpsertUpstreamRelease inserts or updates upstream Release metadata.
func (tx *Tx) UpsertUpstreamRelease(record UpstreamReleaseRecord) error {
	if strings.TrimSpace(record.Suite) == "" {
		return fmt.Errorf("upstream release suite is required")
	}
	fetchedAt := record.FetchedAt
	if fetchedAt.IsZero() {
		fetchedAt = time.Now()
	}
	_, err := tx.tx.Exec(`
INSERT INTO upstream_releases(suite, origin, label, fetched_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(suite) DO UPDATE SET
	origin = excluded.origin,
	label = excluded.label,
	fetched_at = excluded.fetched_at
`, record.Suite, record.Origin, record.Label, nowString(fetchedAt))
	return err
}

// UpstreamRelease returns upstream Release metadata for a suite.
func (s *Store) UpstreamRelease(suite string) (UpstreamReleaseRecord, error) {
	var record UpstreamReleaseRecord
	var fetchedAt string
	err := s.db.QueryRow(`
SELECT suite, origin, label, fetched_at
FROM upstream_releases
WHERE suite = ?
`, suite).Scan(&record.Suite, &record.Origin, &record.Label, &fetchedAt)
	if err != nil {
		return UpstreamReleaseRecord{}, err
	}
	record.FetchedAt, err = parseTime(fetchedAt)
	if err != nil {
		return UpstreamReleaseRecord{}, err
	}
	return record, nil
}

func scanPackage(row interface {
	Scan(dest ...interface{}) error
}) (PackageRecord, error) {
	var pkg PackageRecord
	var fieldsJSON string
	err := row.Scan(
		&pkg.Key,
		&pkg.Name,
		&pkg.Version,
		&pkg.Architecture,
		&pkg.Filename,
		&pkg.Component,
		&pkg.Source,
		&pkg.Size,
		&pkg.MD5,
		&pkg.SHA1,
		&pkg.SHA256,
		&pkg.SHA512,
		&pkg.PoolPath,
		&fieldsJSON,
	)
	if err != nil {
		return pkg, err
	}
	pkg.Fields, err = decodeFields(fieldsJSON)
	return pkg, err
}

func normalizePackageKey(pkg PackageRecord) (string, error) {
	key := strings.TrimSpace(pkg.Key)
	if key != "" {
		return key, nil
	}
	if strings.TrimSpace(pkg.Name) == "" {
		return "", fmt.Errorf("package name is required")
	}
	if strings.TrimSpace(pkg.Version) == "" {
		return "", fmt.Errorf("package version is required")
	}
	if strings.TrimSpace(pkg.Architecture) == "" {
		return "", fmt.Errorf("package architecture is required")
	}
	hash := firstNonEmpty(pkg.SHA256, pkg.SHA512, pkg.SHA1, pkg.MD5, pkg.PoolPath)
	if hash == "" {
		return "", fmt.Errorf("package checksum or pool path is required")
	}
	return strings.Join([]string{pkg.Name, pkg.Version, pkg.Architecture, hash}, "\x1f"), nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func uniqueNonEmpty(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func queryStrings(rows *sql.Rows, err error) ([]string, error) {
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	var values []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return values, nil
}

func isNoRows(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}

func signValue(enabled bool) string {
	if enabled {
		return "yes"
	}
	return "no"
}

func encodeFields(fields map[string]string) (string, error) {
	if len(fields) == 0 {
		return "{}", nil
	}
	data, err := json.Marshal(fields)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func decodeFields(value string) (map[string]string, error) {
	if strings.TrimSpace(value) == "" {
		return map[string]string{}, nil
	}
	var fields map[string]string
	if err := json.Unmarshal([]byte(value), &fields); err != nil {
		return nil, err
	}
	if fields == nil {
		fields = map[string]string{}
	}
	return fields, nil
}
