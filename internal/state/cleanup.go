package state

import (
	"fmt"
	"time"
)

const timeFormat = time.RFC3339Nano

// AddCleanupRef records an explicit cleanup reference to a pool file.
func (s *Store) AddCleanupRef(ref CleanupRef) error {
	return s.WithTx(func(tx *Tx) error {
		return tx.AddCleanupRef(ref)
	})
}

// AddCleanupRef records an explicit cleanup reference to a pool file.
func (tx *Tx) AddCleanupRef(ref CleanupRef) error {
	if ref.PoolPath == "" {
		return fmt.Errorf("cleanup ref pool path is required")
	}
	if ref.RefType == "" {
		return fmt.Errorf("cleanup ref type is required")
	}
	if ref.RefName == "" {
		return fmt.Errorf("cleanup ref name is required")
	}
	_, err := tx.tx.Exec(`
INSERT OR IGNORE INTO cleanup_refs(pool_path, ref_type, ref_name)
VALUES (?, ?, ?)
`, ref.PoolPath, ref.RefType, ref.RefName)
	return err
}

// RemoveCleanupRef removes one explicit cleanup reference to a pool file.
func (s *Store) RemoveCleanupRef(ref CleanupRef) error {
	return s.WithTx(func(tx *Tx) error {
		_, err := tx.tx.Exec(`
DELETE FROM cleanup_refs
WHERE pool_path = ? AND ref_type = ? AND ref_name = ?
`, ref.PoolPath, ref.RefType, ref.RefName)
		return err
	})
}

// IsReferenced reports whether a pool-relative file path is still referenced.
func (s *Store) IsReferenced(poolPath string) (bool, error) {
	var count int
	err := s.db.QueryRow(`
SELECT COUNT(*)
FROM packages p
WHERE p.pool_path = ?
  AND (
    EXISTS (SELECT 1 FROM mirror_packages mp WHERE mp.package_key = p.package_key)
    OR EXISTS (SELECT 1 FROM snapshot_packages sp WHERE sp.package_key = p.package_key)
    OR EXISTS (SELECT 1 FROM cleanup_refs cr WHERE cr.pool_path = p.pool_path)
  )
`, poolPath).Scan(&count)
	return count > 0, err
}

// UnreferencedPoolPaths returns known package pool paths that are not referenced.
func (s *Store) UnreferencedPoolPaths() ([]string, error) {
	return queryStrings(s.db.Query(`
SELECT p.pool_path
FROM packages p
WHERE NOT EXISTS (SELECT 1 FROM mirror_packages mp WHERE mp.package_key = p.package_key)
  AND NOT EXISTS (SELECT 1 FROM snapshot_packages sp WHERE sp.package_key = p.package_key)
  AND NOT EXISTS (SELECT 1 FROM cleanup_refs cr WHERE cr.pool_path = p.pool_path)
ORDER BY p.pool_path
`))
}

// DeleteUnreferencedPackage removes one package row only when no mirror,
// snapshot, or explicit cleanup reference still points at its pool path.
func (s *Store) DeleteUnreferencedPackage(poolPath string) error {
	return s.WithTx(func(tx *Tx) error {
		return tx.DeleteUnreferencedPackage(poolPath)
	})
}

// DeleteUnreferencedPackage removes one package row only when no mirror,
// snapshot, or explicit cleanup reference still points at its pool path.
func (tx *Tx) DeleteUnreferencedPackage(poolPath string) error {
	if poolPath == "" {
		return fmt.Errorf("package pool path is required")
	}
	result, err := tx.tx.Exec(`
DELETE FROM packages
WHERE pool_path = ?
  AND NOT EXISTS (SELECT 1 FROM mirror_packages mp WHERE mp.package_key = packages.package_key)
  AND NOT EXISTS (SELECT 1 FROM snapshot_packages sp WHERE sp.package_key = packages.package_key)
  AND NOT EXISTS (SELECT 1 FROM cleanup_refs cr WHERE cr.pool_path = packages.pool_path)
`, poolPath)
	if err != nil {
		return err
	}
	removed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if removed == 0 {
		return fmt.Errorf("refusing to delete referenced or unknown package row: %s", poolPath)
	}
	return nil
}

func nowString(t time.Time) string {
	return t.UTC().Format(timeFormat)
}

func parseTime(value string) (time.Time, error) {
	return time.Parse(timeFormat, value)
}
