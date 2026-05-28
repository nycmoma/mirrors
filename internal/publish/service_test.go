package publish

import (
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mirrors/internal/config"
	"mirrors/internal/mirror"
	"mirrors/internal/state"
)

func TestPublishSelectedWritesUnsignedRepository(t *testing.T) {
	home := t.TempDir()
	cfg := testConfig()
	snapshotName := seedPublishedSnapshot(t, home, cfg, "Ubuntu", "Ubuntu")

	service := newTestService(t, home)
	result, err := service.PublishSelected(cfg)
	if err != nil {
		t.Fatalf("PublishSelected returned error: %v", err)
	}
	root := filepath.Join(home, cfg.Path)
	if result.Path != root || result.Packages != 1 || result.Indexes != 2 {
		t.Fatalf("unexpected publish result: %#v", result)
	}
	if len(result.Snapshots) != 1 || result.Snapshots[0] != snapshotName {
		t.Fatalf("unexpected snapshots: %#v", result.Snapshots)
	}

	packagesPath := filepath.Join(root, "dists", "focal", "main", "binary-amd64", "Packages")
	packagesData := readFile(t, packagesPath)
	for _, want := range []string{
		"Package: demo\n",
		"Version: 1.0\n",
		"Architecture: amd64\n",
		"Filename: pool/main/d/demo/demo_1.0_amd64.deb\n",
		"Priority: optional\n",
	} {
		if !strings.Contains(string(packagesData), want) {
			t.Fatalf("Packages missing %q:\n%s", want, packagesData)
		}
	}
	if strings.Index(string(packagesData), "Package:") > strings.Index(string(packagesData), "Version:") {
		t.Fatalf("Packages fields are not in stable preferred order:\n%s", packagesData)
	}

	gzData := readGzip(t, filepath.Join(root, "dists", "focal", "main", "binary-amd64", "Packages.gz"))
	if string(gzData) != string(packagesData) {
		t.Fatalf("Packages.gz content mismatch:\n%s\n%s", gzData, packagesData)
	}

	release := string(readFile(t, filepath.Join(root, "dists", "focal", "Release")))
	for _, want := range []string{
		"Origin: Ubuntu\n",
		"Label: Ubuntu\n",
		"Suite: focal\n",
		"Architectures: amd64\n",
		"Components: main\n",
		"MD5Sum:\n",
		"SHA256:\n",
		"main/binary-amd64/Packages",
		"main/binary-amd64/Packages.gz",
	} {
		if !strings.Contains(release, want) {
			t.Fatalf("Release missing %q:\n%s", want, release)
		}
	}

	if _, err := os.Stat(filepath.Join(root, "pool", "main", "d", "demo", "demo_1.0_amd64.deb")); err != nil {
		t.Fatalf("published package missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "dists", "focal", "InRelease")); !os.IsNotExist(err) {
		t.Fatalf("InRelease should not be generated in Phase 9, stat error: %v", err)
	}
}

func TestReleaseUsesExplicitOriginAndLabel(t *testing.T) {
	home := t.TempDir()
	cfg := testConfig()
	cfg.Origin = "Custom Origin"
	cfg.Label = "Custom Label"
	seedPublishedSnapshot(t, home, cfg, "Ubuntu", "Ubuntu")

	service := newTestService(t, home)
	if _, err := service.PublishSelected(cfg); err != nil {
		t.Fatalf("PublishSelected returned error: %v", err)
	}
	release := string(readFile(t, filepath.Join(home, cfg.Path, "dists", "focal", "Release")))
	if !strings.Contains(release, "Origin: Custom Origin\n") || !strings.Contains(release, "Label: Custom Label\n") {
		t.Fatalf("Release did not use explicit origin/label:\n%s", release)
	}
}

func TestHideRemovesPublishedOutputAndPreservesState(t *testing.T) {
	home := t.TempDir()
	cfg := testConfig()
	snapshotName := seedPublishedSnapshot(t, home, cfg, "Ubuntu", "Ubuntu")
	service := newTestService(t, home)
	if _, err := service.PublishSelected(cfg); err != nil {
		t.Fatalf("PublishSelected returned error: %v", err)
	}

	result, err := service.Hide(cfg.Name)
	if err != nil {
		t.Fatalf("Hide returned error: %v", err)
	}
	if !result.Hidden {
		t.Fatalf("expected hidden result: %#v", result)
	}
	if _, err := os.Stat(filepath.Join(home, cfg.Path)); !os.IsNotExist(err) {
		t.Fatalf("published path should be removed, stat error: %v", err)
	}

	store, err := state.Open(config.DBPathForHome(home, cfg.Name))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()
	if _, err := store.Snapshot(snapshotName); err != nil {
		t.Fatalf("snapshot should be preserved: %v", err)
	}
	published, err := store.Published()
	if err != nil {
		t.Fatalf("Published returned error: %v", err)
	}
	if !published.Hidden {
		t.Fatalf("published state should be hidden: %#v", published)
	}
}

func newTestService(t *testing.T, home string) *Service {
	t.Helper()
	service, err := NewService(WithHome(home), WithNow(func() time.Time {
		return time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	}))
	if err != nil {
		t.Fatalf("NewService returned error: %v", err)
	}
	return service
}

func seedPublishedSnapshot(t *testing.T, home string, cfg config.Mirror, origin, label string) string {
	t.Helper()
	store, err := state.Open(config.DBPathForHome(home, cfg.Name))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()
	if err := store.SaveMirrorConfig(cfg); err != nil {
		t.Fatalf("SaveMirrorConfig returned error: %v", err)
	}
	if err := store.UpsertUpstreamRelease(state.UpstreamReleaseRecord{Suite: "focal", Origin: origin, Label: label, FetchedAt: time.Now()}); err != nil {
		t.Fatalf("UpsertUpstreamRelease returned error: %v", err)
	}

	content := []byte("package")
	poolPath := filepath.Join("aa", "bb", "demo.deb")
	fullPoolPath := filepath.Join(config.PackageDirForHome(home), poolPath)
	if err := os.MkdirAll(filepath.Dir(fullPoolPath), 0755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.WriteFile(fullPoolPath, content, 0644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	pkg := state.PackageRecord{
		Name:         "demo",
		Version:      "1.0",
		Architecture: "amd64",
		Filename:     "pool/main/d/demo/demo_1.0_amd64.deb",
		Component:    "main",
		Source:       "demo",
		Size:         int64(len(content)),
		MD5:          "md5",
		SHA1:         "sha1",
		SHA256:       "sha256",
		SHA512:       "sha512",
		PoolPath:     filepath.ToSlash(poolPath),
		Fields: map[string]string{
			"Package":      "demo",
			"Version":      "1.0",
			"Architecture": "amd64",
			"Filename":     "pool/main/d/demo/demo_1.0_amd64.deb",
			"Size":         "7",
			"Priority":     "optional",
		},
	}
	key, err := store.UpsertPackage(pkg)
	if err != nil {
		t.Fatalf("UpsertPackage returned error: %v", err)
	}
	snapshotName := mirror.SnapshotName("ubuntu-focal-main", "2026-05-27")
	if err := store.CreateSnapshot(state.SnapshotRecord{Name: snapshotName, Kind: "regular", CreatedAt: time.Now()}, []string{key}); err != nil {
		t.Fatalf("CreateSnapshot returned error: %v", err)
	}
	if err := store.SetPublished(state.PublishedRecord{SnapshotName: snapshotName, Path: cfg.Path, Suite: "focal", Component: "main", PublishedAt: time.Now()}); err != nil {
		t.Fatalf("SetPublished returned error: %v", err)
	}
	return snapshotName
}

func testConfig() config.Mirror {
	return config.Mirror{
		Name:       "ubuntu",
		URL:        "http://repo.example.test/ubuntu",
		Dists:      []string{"focal"},
		Releases:   []string{"default"},
		Origin:     "default",
		Label:      "default",
		Arch:       []string{"amd64"},
		Components: []string{"main"},
		Path:       "published/ubuntu",
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile %s returned error: %v", path, err)
	}
	return data
}

func readGzip(t *testing.T, path string) []byte {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("Open %s returned error: %v", path, err)
	}
	defer file.Close()
	reader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatalf("NewReader returned error: %v", err)
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll returned error: %v", err)
	}
	return data
}
