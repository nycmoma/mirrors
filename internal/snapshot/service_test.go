package snapshot

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"mirrors/internal/config"
	"mirrors/internal/mirror"
	"mirrors/internal/state"
)

func TestCreateCurrentRegeneratesTodaySnapshot(t *testing.T) {
	home := t.TempDir()
	cfg := testConfig(false)
	store := openSnapshotStore(t, home, cfg)
	defer closeSnapshotStore(t, store)
	key1 := upsertSnapshotPackage(t, store, testPackage("demo", "1.0", "demo-v1"))
	key2 := upsertSnapshotPackage(t, store, testPackage("other", "1.0", "other-v1"))

	if err := store.ReplaceMirrorPackages([]string{key1}); err != nil {
		t.Fatalf("ReplaceMirrorPackages returned error: %v", err)
	}
	service := newTestService(t, home, testNow())
	first, err := service.CreateCurrent(cfg)
	if err != nil {
		t.Fatalf("CreateCurrent returned error: %v", err)
	}
	if len(first.Snapshots) != 1 || first.Snapshots[0].Regenerated {
		t.Fatalf("unexpected first result: %#v", first)
	}

	if err := store.ReplaceMirrorPackages([]string{key2}); err != nil {
		t.Fatalf("second ReplaceMirrorPackages returned error: %v", err)
	}
	second, err := service.CreateCurrent(cfg)
	if err != nil {
		t.Fatalf("second CreateCurrent returned error: %v", err)
	}
	if len(second.Snapshots) != 1 || !second.Snapshots[0].Regenerated {
		t.Fatalf("expected regenerated snapshot: %#v", second)
	}

	snapshotName := mirror.SnapshotName("ubuntu-focal-main", "2026-05-27")
	keys, err := store.SnapshotPackageKeys(snapshotName)
	if err != nil {
		t.Fatalf("SnapshotPackageKeys returned error: %v", err)
	}
	if !reflect.DeepEqual(keys, []string{key2}) {
		t.Fatalf("today snapshot membership was not regenerated: %#v", keys)
	}
}

func TestCreateCurrentDoesNotModifyOlderSnapshot(t *testing.T) {
	home := t.TempDir()
	cfg := testConfig(false)
	store := openSnapshotStore(t, home, cfg)
	defer closeSnapshotStore(t, store)
	oldKey := upsertSnapshotPackage(t, store, testPackage("demo", "1.0", "demo-v1"))
	newKey := upsertSnapshotPackage(t, store, testPackage("demo", "2.0", "demo-v2"))
	oldName := mirror.SnapshotName("ubuntu-focal-main", "2026-05-26")
	if err := store.CreateSnapshot(state.SnapshotRecord{Name: oldName, Kind: kindRegular, CreatedAt: testNow().Add(-24 * time.Hour)}, []string{oldKey}); err != nil {
		t.Fatalf("CreateSnapshot returned error: %v", err)
	}
	if err := store.ReplaceMirrorPackages([]string{newKey}); err != nil {
		t.Fatalf("ReplaceMirrorPackages returned error: %v", err)
	}

	service := newTestService(t, home, testNow())
	if _, err := service.CreateCurrent(cfg); err != nil {
		t.Fatalf("CreateCurrent returned error: %v", err)
	}

	keys, err := store.SnapshotPackageKeys(oldName)
	if err != nil {
		t.Fatalf("SnapshotPackageKeys returned error: %v", err)
	}
	if !reflect.DeepEqual(keys, []string{oldKey}) {
		t.Fatalf("older snapshot membership changed: %#v", keys)
	}
}

func TestMergeWarnsAndSelectsNewestChecksumConflict(t *testing.T) {
	home := t.TempDir()
	cfg := testConfig(true)
	store := openSnapshotStore(t, home, cfg)
	defer closeSnapshotStore(t, store)
	oldPkg := testPackage("demo", "1.0", "demo-old")
	newPkg := testPackage("demo", "1.0", "demo-new")
	oldKey := upsertSnapshotPackage(t, store, oldPkg)
	newKey := upsertSnapshotPackage(t, store, newPkg)
	oldName := mirror.SnapshotName("ubuntu-focal-main", "2026-05-26")
	if err := store.CreateSnapshot(state.SnapshotRecord{Name: oldName, Kind: kindRegular, CreatedAt: testNow().Add(-24 * time.Hour)}, []string{oldKey}); err != nil {
		t.Fatalf("CreateSnapshot returned error: %v", err)
	}
	if err := store.ReplaceMirrorPackages([]string{newKey}); err != nil {
		t.Fatalf("ReplaceMirrorPackages returned error: %v", err)
	}

	service := newTestService(t, home, testNow())
	result, err := service.CreateCurrent(cfg)
	if err != nil {
		t.Fatalf("CreateCurrent returned error: %v", err)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "multiple checksums") {
		t.Fatalf("expected checksum warning, got %#v", result.Warnings)
	}
	mergedName := mirror.MergedSnapshotName("ubuntu-focal-main", "2026-05-27")
	keys, err := store.SnapshotPackageKeys(mergedName)
	if err != nil {
		t.Fatalf("SnapshotPackageKeys returned error: %v", err)
	}
	if !reflect.DeepEqual(keys, []string{newKey}) {
		t.Fatalf("merged snapshot should select newest file: %#v", keys)
	}
}

func TestRollbackByDateSelectsMergedSnapshotWhenMergeEnabled(t *testing.T) {
	home := t.TempDir()
	cfg := testConfig(true)
	store := openSnapshotStore(t, home, cfg)
	defer closeSnapshotStore(t, store)
	key := upsertSnapshotPackage(t, store, testPackage("demo", "1.0", "demo-v1"))
	regularName := mirror.SnapshotName("ubuntu-focal-main", "2026-05-26")
	mergedName := mirror.MergedSnapshotName("ubuntu-focal-main", "2026-05-26")
	if err := store.CreateSnapshot(state.SnapshotRecord{Name: regularName, Kind: kindRegular, CreatedAt: testNow().Add(-24 * time.Hour)}, []string{key}); err != nil {
		t.Fatalf("CreateSnapshot regular returned error: %v", err)
	}
	if err := store.CreateSnapshot(state.SnapshotRecord{Name: mergedName, Kind: kindMerged, CreatedAt: testNow().Add(-24 * time.Hour)}, []string{key}); err != nil {
		t.Fatalf("CreateSnapshot merged returned error: %v", err)
	}

	service := newTestService(t, home, testNow())
	result, err := service.Rollback(cfg.Name, "2026-05-26", "")
	if err != nil {
		t.Fatalf("Rollback returned error: %v", err)
	}
	if result.SelectedSnapshot != mergedName {
		t.Fatalf("expected merged snapshot selection, got %#v", result)
	}
	published, err := store.Published()
	if err != nil {
		t.Fatalf("Published returned error: %v", err)
	}
	if published.SnapshotName != mergedName {
		t.Fatalf("selected snapshot was not persisted: %#v", published)
	}
}

func TestListAndSnapshotLookup(t *testing.T) {
	home := t.TempDir()
	cfg := testConfig(false)
	store := openSnapshotStore(t, home, cfg)
	defer closeSnapshotStore(t, store)
	key := upsertSnapshotPackage(t, store, testPackage("demo", "1.0", "demo-v1"))
	snapshotName := mirror.SnapshotName("ubuntu-focal-main", "2026-05-27")
	if err := store.CreateSnapshot(state.SnapshotRecord{Name: snapshotName, Kind: kindRegular, CreatedAt: testNow()}, []string{key}); err != nil {
		t.Fatalf("CreateSnapshot returned error: %v", err)
	}

	service := newTestService(t, home, testNow())
	summaries, err := service.List(cfg.Name)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(summaries) != 1 || summaries[0].Record.Name != snapshotName || summaries[0].PackageCount != 1 || summaries[0].PackageSizeBytes == 0 {
		t.Fatalf("unexpected summaries: %#v", summaries)
	}
	summary, err := service.Snapshot(cfg.Name, snapshotName)
	if err != nil {
		t.Fatalf("Snapshot returned error: %v", err)
	}
	if summary.Record.Name != snapshotName || summary.PackageCount != 1 || summary.PackageSizeBytes == 0 {
		t.Fatalf("unexpected snapshot summary: %#v", summary)
	}
}

func newTestService(t *testing.T, home string, now time.Time) *Service {
	t.Helper()
	service, err := NewService(WithHome(home), WithNow(func() time.Time { return now }))
	if err != nil {
		t.Fatalf("NewService returned error: %v", err)
	}
	return service
}

func openSnapshotStore(t *testing.T, home string, cfg config.Mirror) *state.Store {
	t.Helper()
	store, err := state.Open(config.DBPathForHome(home, cfg.Name))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	if err := store.SaveMirrorConfig(cfg); err != nil {
		t.Fatalf("SaveMirrorConfig returned error: %v", err)
	}
	return store
}

func closeSnapshotStore(t *testing.T, store *state.Store) {
	t.Helper()
	if err := store.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
}

func upsertSnapshotPackage(t *testing.T, store *state.Store, pkg state.PackageRecord) string {
	t.Helper()
	key, err := store.UpsertPackage(pkg)
	if err != nil {
		t.Fatalf("UpsertPackage returned error: %v", err)
	}
	return key
}

func testConfig(mergeEnabled bool) config.Mirror {
	return config.Mirror{
		Name:       "ubuntu",
		URL:        "http://repo.example.test/ubuntu",
		Dists:      []string{"focal"},
		Releases:   []string{"default"},
		Origin:     "Test",
		Label:      "Test",
		Arch:       []string{"amd64"},
		Components: []string{"main"},
		Path:       "ubuntu",
		Merge:      config.Merge{Enabled: mergeEnabled},
		Signing: config.Signing{
			GPGKey:        "560CE107BECFB86BF8BED1DBD9FEEBA651DA48E7",
			GPGPassphrase: "1234",
		},
	}
}

func testPackage(name, version, checksum string) state.PackageRecord {
	return state.PackageRecord{
		Name:         name,
		Version:      version,
		Architecture: "amd64",
		Filename:     "pool/main/" + name + "_" + version + "_amd64.deb",
		Component:    "main",
		Source:       name,
		Size:         123,
		MD5:          checksum + "-md5",
		SHA1:         checksum + "-sha1",
		SHA256:       checksum + "-sha256",
		SHA512:       checksum + "-sha512",
		PoolPath:     "pool/" + checksum + ".deb",
	}
}

func testNow() time.Time {
	return time.Date(2026, 5, 27, 12, 0, 0, 0, time.Local)
}
