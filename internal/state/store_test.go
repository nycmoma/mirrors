package state

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"mirrors/internal/config"

	_ "github.com/mattn/go-sqlite3"
)

func TestOpenCreatesDBAndRecordsMigration(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "ubuntu.sqlite")

	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer closeStore(t, store)

	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("expected DB file to exist: %v", err)
	}

	version, err := store.SchemaVersion()
	if err != nil {
		t.Fatalf("SchemaVersion returned error: %v", err)
	}
	if version != latestSchemaVersion {
		t.Fatalf("expected schema version %d, got %d", latestSchemaVersion, version)
	}
}

func TestOpenRunsMigrationsIdempotently(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "ubuntu.sqlite")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	closeStore(t, store)

	store, err = Open(dbPath)
	if err != nil {
		t.Fatalf("second Open returned error: %v", err)
	}
	defer closeStore(t, store)

	var count int
	err = store.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, latestSchemaVersion).Scan(&count)
	if err != nil {
		t.Fatalf("QueryRow returned error: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected one migration record, got %d", count)
	}
}

func TestSaveAndLoadMirrorConfig(t *testing.T) {
	store := openTempStore(t)
	defer closeStore(t, store)

	cfg := testConfig()
	if err := store.SaveMirrorConfig(cfg); err != nil {
		t.Fatalf("SaveMirrorConfig returned error: %v", err)
	}

	loaded, err := store.MirrorConfig()
	if err != nil {
		t.Fatalf("MirrorConfig returned error: %v", err)
	}
	if !reflect.DeepEqual(loaded, cfg) {
		t.Fatalf("unexpected config:\nwant %#v\ngot  %#v", cfg, loaded)
	}

	cfg.Path = "updated"
	cfg.Merge = config.Merge{Enabled: true, Depth: 2}
	if err := store.SaveMirrorConfig(cfg); err != nil {
		t.Fatalf("second SaveMirrorConfig returned error: %v", err)
	}

	loaded, err = store.MirrorConfig()
	if err != nil {
		t.Fatalf("second MirrorConfig returned error: %v", err)
	}
	if loaded.Path != "updated" || loaded.Merge.Depth != 2 {
		t.Fatalf("config update was not persisted: %#v", loaded)
	}
}

func TestLoadMirrorConfigKeepsPhase2ReaderCompatibility(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "ubuntu.sqlite")
	createLegacyMirrorDB(t, dbPath)

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

func TestUpsertPackage(t *testing.T) {
	store := openTempStore(t)
	defer closeStore(t, store)

	pkg := testPackage("pkg1", "pool/pkg1.deb")
	key, err := store.UpsertPackage(pkg)
	if err != nil {
		t.Fatalf("UpsertPackage returned error: %v", err)
	}
	if key == "" {
		t.Fatal("expected package key")
	}

	pkg.Size = 456
	pkg.PoolPath = "pool/pkg1-updated.deb"
	key2, err := store.UpsertPackage(pkg)
	if err != nil {
		t.Fatalf("second UpsertPackage returned error: %v", err)
	}
	if key2 != key {
		t.Fatalf("expected same key, got %q and %q", key, key2)
	}

	loaded, err := store.Package(key)
	if err != nil {
		t.Fatalf("Package returned error: %v", err)
	}
	if loaded.Size != 456 || loaded.PoolPath != "pool/pkg1-updated.deb" {
		t.Fatalf("package update was not persisted: %#v", loaded)
	}
}

func TestUpsertPackagePersistsFields(t *testing.T) {
	store := openTempStore(t)
	defer closeStore(t, store)

	pkg := testPackage("pkg1", "pool/pkg1.deb")
	pkg.Fields = map[string]string{"Package": "pkg1", "Priority": "optional"}
	key := upsertTestPackage(t, store, pkg)

	loaded, err := store.Package(key)
	if err != nil {
		t.Fatalf("Package returned error: %v", err)
	}
	if loaded.Fields["Priority"] != "optional" {
		t.Fatalf("package fields were not persisted: %#v", loaded.Fields)
	}
}

func TestReplaceMirrorPackages(t *testing.T) {
	store := openTempStore(t)
	defer closeStore(t, store)
	key1 := upsertTestPackage(t, store, testPackage("pkg1", "pool/pkg1.deb"))
	key2 := upsertTestPackage(t, store, testPackage("pkg2", "pool/pkg2.deb"))

	if err := store.ReplaceMirrorPackages([]string{key1}); err != nil {
		t.Fatalf("ReplaceMirrorPackages returned error: %v", err)
	}
	if err := store.ReplaceMirrorPackages([]string{key2, key2}); err != nil {
		t.Fatalf("second ReplaceMirrorPackages returned error: %v", err)
	}

	keys, err := store.MirrorPackageKeys()
	if err != nil {
		t.Fatalf("MirrorPackageKeys returned error: %v", err)
	}
	if !reflect.DeepEqual(keys, []string{key2}) {
		t.Fatalf("unexpected mirror package keys: %#v", keys)
	}
}

func TestCreateSnapshotIsImmutable(t *testing.T) {
	store := openTempStore(t)
	defer closeStore(t, store)
	key1 := upsertTestPackage(t, store, testPackage("pkg1", "pool/pkg1.deb"))
	key2 := upsertTestPackage(t, store, testPackage("pkg2", "pool/pkg2.deb"))

	snapshot := SnapshotRecord{Name: "ubuntu-focal-main_2026-05-27", Kind: "regular", CreatedAt: testTime()}
	if err := store.CreateSnapshot(snapshot, []string{key1}); err != nil {
		t.Fatalf("CreateSnapshot returned error: %v", err)
	}
	err := store.CreateSnapshot(snapshot, []string{key2})
	if err == nil {
		t.Fatal("expected duplicate snapshot error")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("unexpected duplicate snapshot error: %v", err)
	}

	keys, err := store.SnapshotPackageKeys(snapshot.Name)
	if err != nil {
		t.Fatalf("SnapshotPackageKeys returned error: %v", err)
	}
	if !reflect.DeepEqual(keys, []string{key1}) {
		t.Fatalf("snapshot membership changed: %#v", keys)
	}
}

func TestReplaceSnapshotReplacesMembership(t *testing.T) {
	store := openTempStore(t)
	defer closeStore(t, store)
	key1 := upsertTestPackage(t, store, testPackage("pkg1", "pool/pkg1.deb"))
	key2 := upsertTestPackage(t, store, testPackage("pkg2", "pool/pkg2.deb"))

	snapshot := SnapshotRecord{Name: "ubuntu-focal-main_2026-05-27", Kind: "regular", CreatedAt: testTime()}
	if err := store.CreateSnapshot(snapshot, []string{key1}); err != nil {
		t.Fatalf("CreateSnapshot returned error: %v", err)
	}
	if err := store.ReplaceSnapshot(snapshot, []string{key2}); err != nil {
		t.Fatalf("ReplaceSnapshot returned error: %v", err)
	}

	keys, err := store.SnapshotPackageKeys(snapshot.Name)
	if err != nil {
		t.Fatalf("SnapshotPackageKeys returned error: %v", err)
	}
	if !reflect.DeepEqual(keys, []string{key2}) {
		t.Fatalf("unexpected replacement membership: %#v", keys)
	}
}

func TestSnapshotPackagesPreserveCapturedFields(t *testing.T) {
	store := openTempStore(t)
	defer closeStore(t, store)
	pkg := testPackage("pkg1", "pool/pkg1.deb")
	pkg.Fields = map[string]string{"Package": "pkg1", "Description": "first"}
	key := upsertTestPackage(t, store, pkg)

	snapshot := SnapshotRecord{Name: "ubuntu-focal-main_2026-05-27", Kind: "regular", CreatedAt: testTime()}
	if err := store.CreateSnapshot(snapshot, []string{key}); err != nil {
		t.Fatalf("CreateSnapshot returned error: %v", err)
	}

	pkg.Fields = map[string]string{"Package": "pkg1", "Description": "changed"}
	if _, err := store.UpsertPackage(pkg); err != nil {
		t.Fatalf("second UpsertPackage returned error: %v", err)
	}

	packages, err := store.SnapshotPackages(snapshot.Name)
	if err != nil {
		t.Fatalf("SnapshotPackages returned error: %v", err)
	}
	if len(packages) != 1 || packages[0].Fields["Description"] != "first" {
		t.Fatalf("snapshot fields were not preserved: %#v", packages)
	}
}

func TestUpsertUpstreamRelease(t *testing.T) {
	store := openTempStore(t)
	defer closeStore(t, store)

	record := UpstreamReleaseRecord{Suite: "focal", Origin: "Ubuntu", Label: "Ubuntu", FetchedAt: testTime()}
	if err := store.UpsertUpstreamRelease(record); err != nil {
		t.Fatalf("UpsertUpstreamRelease returned error: %v", err)
	}
	record.Label = "Updated"
	if err := store.UpsertUpstreamRelease(record); err != nil {
		t.Fatalf("second UpsertUpstreamRelease returned error: %v", err)
	}

	loaded, err := store.UpstreamRelease("focal")
	if err != nil {
		t.Fatalf("UpstreamRelease returned error: %v", err)
	}
	if loaded.Origin != "Ubuntu" || loaded.Label != "Updated" {
		t.Fatalf("unexpected upstream release: %#v", loaded)
	}
}

func TestSetPublishedSwitchesState(t *testing.T) {
	store := openTempStore(t)
	defer closeStore(t, store)
	key := upsertTestPackage(t, store, testPackage("pkg1", "pool/pkg1.deb"))
	if err := store.CreateSnapshot(SnapshotRecord{Name: "snap1", CreatedAt: testTime()}, []string{key}); err != nil {
		t.Fatalf("CreateSnapshot snap1 returned error: %v", err)
	}
	if err := store.CreateSnapshot(SnapshotRecord{Name: "snap2", CreatedAt: testTime().Add(time.Hour)}, []string{key}); err != nil {
		t.Fatalf("CreateSnapshot snap2 returned error: %v", err)
	}

	if err := store.SetPublished(PublishedRecord{SnapshotName: "snap1", Path: "ubuntu", Suite: "focal"}); err != nil {
		t.Fatalf("SetPublished snap1 returned error: %v", err)
	}
	if err := store.SetPublished(PublishedRecord{SnapshotName: "snap2", Path: "ubuntu", Suite: "focal", Hidden: true}); err != nil {
		t.Fatalf("SetPublished snap2 returned error: %v", err)
	}

	published, err := store.Published()
	if err != nil {
		t.Fatalf("Published returned error: %v", err)
	}
	if published.SnapshotName != "snap2" || !published.Hidden {
		t.Fatalf("unexpected published state: %#v", published)
	}
}

func TestCleanupReferenceQueries(t *testing.T) {
	store := openTempStore(t)
	defer closeStore(t, store)
	key1 := upsertTestPackage(t, store, testPackage("pkg1", "pool/pkg1.deb"))
	upsertTestPackage(t, store, testPackage("pkg2", "pool/pkg2.deb"))

	if err := store.ReplaceMirrorPackages([]string{key1}); err != nil {
		t.Fatalf("ReplaceMirrorPackages returned error: %v", err)
	}
	if err := store.AddCleanupRef(CleanupRef{PoolPath: "pool/pkg2.deb", RefType: "publish", RefName: "current"}); err != nil {
		t.Fatalf("AddCleanupRef returned error: %v", err)
	}

	referenced, err := store.IsReferenced("pool/pkg1.deb")
	if err != nil {
		t.Fatalf("IsReferenced returned error: %v", err)
	}
	if !referenced {
		t.Fatal("expected pool/pkg1.deb to be referenced")
	}

	paths, err := store.UnreferencedPoolPaths()
	if err != nil {
		t.Fatalf("UnreferencedPoolPaths returned error: %v", err)
	}
	if len(paths) != 0 {
		t.Fatalf("expected no unreferenced paths, got %#v", paths)
	}

	if err := store.RemoveCleanupRef(CleanupRef{PoolPath: "pool/pkg2.deb", RefType: "publish", RefName: "current"}); err != nil {
		t.Fatalf("RemoveCleanupRef returned error: %v", err)
	}
	paths, err = store.UnreferencedPoolPaths()
	if err != nil {
		t.Fatalf("second UnreferencedPoolPaths returned error: %v", err)
	}
	if !reflect.DeepEqual(paths, []string{"pool/pkg2.deb"}) {
		t.Fatalf("unexpected unreferenced paths: %#v", paths)
	}
}

func TestWithTxRollsBackOnError(t *testing.T) {
	store := openTempStore(t)
	defer closeStore(t, store)
	forced := errors.New("forced rollback")

	err := store.WithTx(func(tx *Tx) error {
		if _, err := tx.UpsertPackage(testPackage("pkg1", "pool/pkg1.deb")); err != nil {
			return err
		}
		return forced
	})
	if !errors.Is(err, forced) {
		t.Fatalf("expected forced error, got %v", err)
	}

	_, err = store.Package(packageKeyForTest("pkg1"))
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected package insert rollback, got %v", err)
	}
}

func TestRecordUpdateHistory(t *testing.T) {
	store := openTempStore(t)
	defer closeStore(t, store)

	id, err := store.RecordUpdateHistory(UpdateRecord{
		Action:  "fetch",
		Status:  "ok",
		Message: "downloaded indexes",
	})
	if err != nil {
		t.Fatalf("RecordUpdateHistory returned error: %v", err)
	}
	if id == 0 {
		t.Fatal("expected update history id")
	}
}

func openTempStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "ubuntu.sqlite"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	return store
}

func closeStore(t *testing.T, store *Store) {
	t.Helper()
	if err := store.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
}

func upsertTestPackage(t *testing.T, store *Store, pkg PackageRecord) string {
	t.Helper()
	key, err := store.UpsertPackage(pkg)
	if err != nil {
		t.Fatalf("UpsertPackage returned error: %v", err)
	}
	return key
}

func testConfig() config.Mirror {
	return config.Mirror{
		Name:       "ubuntu",
		URL:        "http://us.archive.ubuntu.com/ubuntu/",
		Dists:      []string{"focal", "jammy"},
		Releases:   []string{"default", "updates"},
		Origin:     "Ubuntu",
		Label:      "Ubuntu",
		Arch:       []string{"amd64"},
		Components: []string{"main", "restricted"},
		Path:       "preprod",
		Merge:      config.Merge{Enabled: true},
		Server:     "http://mirror.example.test",
	}
}

func testPackage(name, poolPath string) PackageRecord {
	return PackageRecord{
		Key:          packageKeyForTest(name),
		Name:         name,
		Version:      "1.0",
		Architecture: "amd64",
		Filename:     "pool/main/" + name + ".deb",
		Component:    "main",
		Source:       name,
		Size:         123,
		MD5:          name + "-md5",
		SHA1:         name + "-sha1",
		SHA256:       name + "-sha256",
		SHA512:       name + "-sha512",
		PoolPath:     poolPath,
	}
}

func packageKeyForTest(name string) string {
	return name + "|1.0|amd64|" + name + "-sha256"
}

func testTime() time.Time {
	return time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
}

func createLegacyMirrorDB(t *testing.T, dbPath string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
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
