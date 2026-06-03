package app

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"mirrors/internal/appconfig"
	"mirrors/internal/cli"
	"mirrors/internal/config"
	"mirrors/internal/download"
	"mirrors/internal/mirror"
	"mirrors/internal/state"

	_ "github.com/mattn/go-sqlite3"
)

func TestRunMirrorCommandRejectsAmbiguousIdentity(t *testing.T) {
	isolateAppConfig(t)
	err := runMirrorCommand(cli.Command{
		Name:       "info",
		ConfigPath: "mirror.conf",
		NameRef:    "ubuntu-xenial",
	})
	if err == nil {
		t.Fatal("expected ambiguous identity error")
	}
	if !strings.Contains(err.Error(), "provide only one of --config, --name, or --URL") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunMirrorCommandRequiresIdentity(t *testing.T) {
	isolateAppConfig(t)
	err := runMirrorCommand(cli.Command{Name: "info"})
	if err == nil {
		t.Fatal("expected missing identity error")
	}
	if !strings.Contains(err.Error(), "missing mirror identity") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNotImplementedReportsPlannedPhase(t *testing.T) {
	err := notImplemented("future")
	if err == nil {
		t.Fatal("expected not implemented error")
	}

	want := `action "future" will be implemented in Phase 11: App Workflows.`
	if err.Error() != want {
		t.Fatalf("expected %q, got %q", want, err.Error())
	}
}

func TestRunConfigGenerate(t *testing.T) {
	isolateAppConfig(t)
	oldGenerate := generateConfig
	generateConfig = func(_ context.Context, rawURL string, _ download.Downloader) (config.Mirror, error) {
		if rawURL != "https://archive.example.test/ubuntu/dists/jammy-updates/Release" {
			t.Fatalf("unexpected generate URL: %q", rawURL)
		}
		return config.Mirror{
			Name:       "archive.example.test-ubuntu-jammy-updates",
			URL:        "https://archive.example.test/ubuntu/",
			Dists:      []string{"jammy"},
			Releases:   []string{"updates"},
			Origin:     "Test",
			Label:      "Test",
			Arch:       []string{"amd64"},
			Components: []string{"main"},
			Path:       "archive.example.test-ubuntu-jammy-updates",
		}, nil
	}
	defer func() {
		generateConfig = oldGenerate
	}()

	output, err := captureStdout(func() error {
		return runConfig(cli.Command{
			Name:       "config",
			Subcommand: "generate",
			URL:        "https://archive.example.test/ubuntu/dists/jammy-updates/Release",
		})
	})
	if err != nil {
		t.Fatalf("runConfig returned error: %v", err)
	}
	for _, want := range []string{
		"name = archive.example.test-ubuntu-jammy-updates",
		"url = https://archive.example.test/ubuntu/",
		"dist = jammy",
		"release = updates",
		"origin = Test",
		"label = Test",
		"components = main",
		"arch = amd64",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
}

func TestRunConfigValidatePrintsUpstreamOriginAndLabel(t *testing.T) {
	isolateAppConfig(t)
	oldValidate := validateConfig
	validateConfig = func(_ context.Context, _ appconfig.Config, cfg config.Mirror) ([]config.UpstreamRelease, error) {
		if cfg.Name != "ubuntu" {
			t.Fatalf("unexpected config: %#v", cfg)
		}
		return []config.UpstreamRelease{
			{Suite: "focal", Origin: "Ubuntu", Label: "Ubuntu"},
			{Suite: "focal-updates", Origin: "Ubuntu", Label: "Ubuntu Updates"},
		}, nil
	}
	defer func() {
		validateConfig = oldValidate
	}()
	configPath := writeTempConfig(t)

	output, err := captureStdout(func() error {
		return runConfig(cli.Command{
			Name:       "config",
			Subcommand: "validate",
			ConfigPath: configPath,
		})
	})
	if err != nil {
		t.Fatalf("runConfig returned error: %v", err)
	}
	for _, want := range []string{
		`Config "` + configPath + `" is valid for mirror "ubuntu"`,
		"Upstream values:",
		"- focal: origin = Ubuntu, label = Ubuntu",
		"- focal-updates: origin = Ubuntu, label = Ubuntu Updates",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
}

func TestValidateExistingMirrorConfigAllowsMatchingStoredConfig(t *testing.T) {
	home := t.TempDir()
	setAppTestHome(t, home)
	store := openAppTestStore(t, home, "ubuntu")
	defer func() {
		_ = store.Close()
	}()
	cfg, err := store.MirrorConfig()
	if err != nil {
		t.Fatalf("MirrorConfig returned error: %v", err)
	}
	cfg.ConfigPath = "/tmp/different-path.conf"
	cfg.Server = "http://changed.example.test"
	cfg.Signing.GPGKey = "different-key"

	if err := validateExistingMirrorConfig(cfg); err != nil {
		t.Fatalf("validateExistingMirrorConfig returned error: %v", err)
	}
}

func TestValidateExistingMirrorConfigAllowsSameConfigPath(t *testing.T) {
	home := t.TempDir()
	setAppTestHome(t, home)
	store := openAppTestStore(t, home, "ubuntu")
	defer func() {
		_ = store.Close()
	}()
	cfg, err := store.MirrorConfig()
	if err != nil {
		t.Fatalf("MirrorConfig returned error: %v", err)
	}
	cfg.ConfigPath = "/tmp/ubuntu.conf"
	if err := store.SaveMirrorConfig(cfg); err != nil {
		t.Fatalf("SaveMirrorConfig returned error: %v", err)
	}
	cfg.URL = "http://mirror.example.test/ubuntu/"

	if err := validateExistingMirrorConfig(cfg); err != nil {
		t.Fatalf("validateExistingMirrorConfig returned error: %v", err)
	}
}

func TestValidateExistingMirrorConfigRejectsDifferentStoredConfig(t *testing.T) {
	home := t.TempDir()
	setAppTestHome(t, home)
	store := openAppTestStore(t, home, "ubuntu")
	defer func() {
		_ = store.Close()
	}()
	cfg, err := store.MirrorConfig()
	if err != nil {
		t.Fatalf("MirrorConfig returned error: %v", err)
	}
	cfg.ConfigPath = "/tmp/different-path.conf"
	cfg.URL = "http://mirror.example.test/ubuntu/"

	err = validateExistingMirrorConfig(cfg)
	if err == nil || !strings.Contains(err.Error(), "different config values") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunConfigShowRejectsAmbiguousIdentity(t *testing.T) {
	isolateAppConfig(t)
	err := runConfig(cli.Command{
		Name:       "config",
		Subcommand: "show",
		ConfigPath: writeTempConfig(t),
		NameRef:    "ubuntu",
	})
	if err == nil {
		t.Fatal("expected ambiguous identity error")
	}
	if !strings.Contains(err.Error(), "either --config or --name") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunConfigShowByPath(t *testing.T) {
	isolateAppConfig(t)
	output, err := captureStdout(func() error {
		return runConfig(cli.Command{
			Name:       "config",
			Subcommand: "show",
			ConfigPath: writeTempConfig(t),
		})
	})
	if err != nil {
		t.Fatalf("runConfig returned error: %v", err)
	}
	for _, want := range []string{
		"name = ubuntu",
		"url = http://us.archive.ubuntu.com/ubuntu/",
		"dist = trusty, xenial, bionic, focal, jammy",
		"release = default, security, updates",
		"merge = no",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
}

func TestRunConfigShowByName(t *testing.T) {
	home := t.TempDir()
	appCfg := setAppTestHome(t, home)
	createMirrorDB(t, appCfg.DBPath("ubuntu"))

	output, err := captureStdout(func() error {
		return runConfig(cli.Command{
			Name:       "config",
			Subcommand: "show",
			NameRef:    "ubuntu",
		})
	})
	if err != nil {
		t.Fatalf("runConfig returned error: %v", err)
	}
	for _, want := range []string{
		"name = ubuntu",
		"dist = focal, jammy",
		"release = default, updates",
		"merge = 2",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
}

func TestRunConfigShowByNameUsesAppConfigDataRoot(t *testing.T) {
	appCfg := appconfig.Default()
	appCfg.DataRoot = t.TempDir()
	appCfg.MirrorsRoot = t.TempDir()
	appCfg.LogsRoot = t.TempDir()
	createMirrorDB(t, appCfg.DBPath("ubuntu"))

	output, err := captureStdout(func() error {
		return runConfigWithConfig(cli.Command{
			Name:       "config",
			Subcommand: "show",
			NameRef:    "ubuntu",
		}, appCfg)
	})
	if err != nil {
		t.Fatalf("runConfigWithConfig returned error: %v", err)
	}
	if !strings.Contains(output, "name = ubuntu") {
		t.Fatalf("output missing mirror config:\n%s", output)
	}
}

func TestPeriodicShouldRunSkipsRecentPublishedSnapshot(t *testing.T) {
	home := t.TempDir()
	setAppTestHome(t, home)
	store := openAppTestStore(t, home, "ubuntu")
	defer func() {
		_ = store.Close()
	}()
	today := time.Now().Local().Format("2006-01-02")
	seedPublishedSnapshot(t, store, "ubuntu-focal-main_"+today)

	shouldRun, message, err := periodicShouldRun("ubuntu", "daily")
	if err != nil {
		t.Fatalf("periodicShouldRun returned error: %v", err)
	}
	if shouldRun {
		t.Fatal("expected daily to skip recent published snapshot")
	}
	if !strings.Contains(message, "Daily skipped") {
		t.Fatalf("unexpected skip message: %q", message)
	}
}

func TestPeriodicShouldRunWithoutPublishedSnapshot(t *testing.T) {
	home := t.TempDir()
	setAppTestHome(t, home)
	store := openAppTestStore(t, home, "ubuntu")
	defer func() {
		_ = store.Close()
	}()

	shouldRun, _, err := periodicShouldRun("ubuntu", "daily")
	if err != nil {
		t.Fatalf("periodicShouldRun returned error: %v", err)
	}
	if !shouldRun {
		t.Fatal("expected daily to run without a published snapshot")
	}
}

func TestSummariesByURLReturnsAllMatches(t *testing.T) {
	home := t.TempDir()
	appCfg := setAppTestHome(t, home)
	first := openAppTestStore(t, home, "ubuntu-one")
	second := openAppTestStore(t, home, "ubuntu-two")
	defer func() {
		_ = first.Close()
		_ = second.Close()
	}()
	service, err := mirror.NewService(mirror.WithStorageDirs(appCfg.DBDir(), appCfg.PackageDir()))
	if err != nil {
		t.Fatalf("NewService returned error: %v", err)
	}

	matches, err := summariesByURL(service, "http://us.archive.ubuntu.com/ubuntu")
	if err != nil {
		t.Fatalf("summariesByURL returned error: %v", err)
	}
	var names []string
	for _, match := range matches {
		names = append(names, match.Config.Name)
	}
	if !reflect.DeepEqual(names, []string{"ubuntu-one", "ubuntu-two"}) {
		t.Fatalf("unexpected URL matches: %#v", names)
	}
}

func TestRunInfoReportsMirrorAndSnapshotSizes(t *testing.T) {
	home := t.TempDir()
	setAppTestHome(t, home)
	store := openAppTestStore(t, home, "ubuntu")
	defer func() {
		_ = store.Close()
	}()
	key, err := store.UpsertPackage(state.PackageRecord{
		Name:         "apt",
		Version:      "2.0",
		Architecture: "amd64",
		Filename:     "pool/main/a/apt/apt.deb",
		Component:    "main",
		Source:       "apt",
		Size:         1536,
		MD5:          strings.Repeat("1", 32),
		SHA1:         strings.Repeat("2", 40),
		SHA256:       strings.Repeat("3", 64),
		SHA512:       strings.Repeat("4", 128),
		PoolPath:     "pool/main/a/apt/apt.deb",
		Fields:       map[string]string{"Package": "apt"},
	})
	if err != nil {
		t.Fatalf("UpsertPackage returned error: %v", err)
	}
	if err := store.ReplaceMirrorPackages([]string{key}); err != nil {
		t.Fatalf("ReplaceMirrorPackages returned error: %v", err)
	}
	if err := store.CreateSnapshot(state.SnapshotRecord{
		Name:      "ubuntu-focal-main_2026-05-30",
		Kind:      "regular",
		CreatedAt: time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC),
	}, []string{key}); err != nil {
		t.Fatalf("CreateSnapshot returned error: %v", err)
	}

	output, err := captureStdout(func() error {
		return runMirrorCommand(cli.Command{Name: "info", NameRef: "ubuntu"})
	})
	if err != nil {
		t.Fatalf("runMirrorCommand returned error: %v", err)
	}
	for _, want := range []string{
		"Packages: 1 current, 1 known",
		"Mirror size: 1.5 KiB",
		"- ubuntu-focal-main_2026-05-30 (regular, 1 packages (1.5 KiB), created 2026-05-30T12:00:00Z)",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
}

func TestRunMoreInfoOnlyListsPackages(t *testing.T) {
	home := t.TempDir()
	setAppTestHome(t, home)
	store := openAppTestStore(t, home, "ubuntu")
	defer func() {
		_ = store.Close()
	}()
	_, err := store.UpsertPackage(state.PackageRecord{
		Name:         "apt",
		Version:      "2.0",
		Architecture: "amd64",
		Filename:     "pool/main/a/apt/apt.deb",
		Component:    "main",
		Source:       "apt",
		Size:         1536,
		MD5:          strings.Repeat("1", 32),
		SHA1:         strings.Repeat("2", 40),
		SHA256:       strings.Repeat("3", 64),
		SHA512:       strings.Repeat("4", 128),
		PoolPath:     "pool/main/a/apt/apt.deb",
		Fields:       map[string]string{"Package": "apt"},
	})
	if err != nil {
		t.Fatalf("UpsertPackage returned error: %v", err)
	}

	output, err := captureStdout(func() error {
		return runMirrorCommand(cli.Command{Name: "more-info", NameRef: "ubuntu"})
	})
	if err != nil {
		t.Fatalf("runMirrorCommand returned error: %v", err)
	}
	for _, want := range []string{
		"Known packages: 1",
		"- apt 2.0 amd64",
		"Pool location: pool/main/a/apt/apt.deb",
		"Size: 1.5 KiB",
		"MD5: " + strings.Repeat("1", 32),
		"SHA1: " + strings.Repeat("2", 40),
		"SHA256: " + strings.Repeat("3", 64),
		"SHA512: " + strings.Repeat("4", 128),
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
	for _, unwanted := range []string{
		"Mirror:",
		"DB path:",
		"Config:",
		"Filename:",
		"Snapshot",
		"Package pool disk usage:",
	} {
		if strings.Contains(output, unwanted) {
			t.Fatalf("output should not contain %q:\n%s", unwanted, output)
		}
	}
}

func TestRunCleanupDryRunReportsUnreferencedPackages(t *testing.T) {
	home := t.TempDir()
	setAppTestHome(t, home)
	store := openAppTestStore(t, home, "ubuntu")
	defer func() {
		_ = store.Close()
	}()
	upsertAppTestPackage(t, store, "unused", "pool/main/u/unused/unused.deb")

	result, err := runCleanup("ubuntu", cli.Command{Name: "cleanup"})
	if err != nil {
		t.Fatalf("runCleanup returned error: %v", err)
	}
	if result.Remove {
		t.Fatal("expected dry run")
	}
	if len(result.PackageCandidates) != 1 || result.PackageCandidates[0] != "pool/main/u/unused/unused.deb" {
		t.Fatalf("unexpected package candidates: %#v", result.PackageCandidates)
	}
	if result.PackagesRemoved != 0 {
		t.Fatalf("unexpected removed count: %d", result.PackagesRemoved)
	}
}

func TestRunCleanupRemoveDeletesPackageFile(t *testing.T) {
	home := t.TempDir()
	appCfg := setAppTestHome(t, home)
	store := openAppTestStore(t, home, "ubuntu")
	defer func() {
		_ = store.Close()
	}()
	seedPublishedSnapshot(t, store, "ubuntu-focal-main_2026-05-28")
	poolPath := "pool/main/u/unused/unused.deb"
	upsertAppTestPackage(t, store, "unused", poolPath)
	fullPath := filepath.Join(appCfg.PackageDir(), filepath.FromSlash(poolPath))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.WriteFile(fullPath, []byte("package"), 0644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	result, err := runCleanup("ubuntu", cli.Command{Name: "cleanup", CleanupAll: true})
	if err != nil {
		t.Fatalf("runCleanup returned error: %v", err)
	}
	if result.PackagesRemoved != 1 || result.BytesRemoved != int64(len("package")) {
		t.Fatalf("unexpected cleanup result: %#v", result)
	}
	if _, err := os.Stat(fullPath); !os.IsNotExist(err) {
		t.Fatalf("expected package file to be removed, stat err=%v", err)
	}
	paths, err := store.UnreferencedPoolPaths()
	if err != nil {
		t.Fatalf("UnreferencedPoolPaths returned error: %v", err)
	}
	if len(paths) != 0 {
		t.Fatalf("expected package row cleanup, got candidates %#v", paths)
	}
}

func TestRunCleanupRemoveDeletesStalePackageRow(t *testing.T) {
	home := t.TempDir()
	setAppTestHome(t, home)
	store := openAppTestStore(t, home, "ubuntu")
	defer func() {
		_ = store.Close()
	}()
	seedPublishedSnapshot(t, store, "ubuntu-focal-main_2026-05-28")
	poolPath := "pool/main/u/stale/stale.deb"
	upsertAppTestPackage(t, store, "stale", poolPath)

	result, err := runCleanup("ubuntu", cli.Command{Name: "cleanup", CleanupAll: true})
	if err != nil {
		t.Fatalf("runCleanup returned error: %v", err)
	}
	if result.PackagesRemoved != 1 || result.BytesRemoved != 0 {
		t.Fatalf("unexpected cleanup result: %#v", result)
	}
	paths, err := store.UnreferencedPoolPaths()
	if err != nil {
		t.Fatalf("UnreferencedPoolPaths returned error: %v", err)
	}
	if len(paths) != 0 {
		t.Fatalf("expected stale package row cleanup, got candidates %#v", paths)
	}
}

func TestRunCleanupAllPreservesPublishedMergedPair(t *testing.T) {
	home := t.TempDir()
	setAppTestHome(t, home)
	store := openAppTestStore(t, home, "ubuntu")
	defer func() {
		_ = store.Close()
	}()
	old := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	for _, name := range []string{
		"ubuntu-focal-main_2026-04-01",
		"MERGED-ubuntu-focal-main_2026-04-01",
		"ubuntu-focal-universe_2026-04-01",
		"ubuntu-focal-main_2026-03-31",
	} {
		if err := store.CreateSnapshot(state.SnapshotRecord{Name: name, Kind: "regular", CreatedAt: old}, nil); err != nil {
			t.Fatalf("CreateSnapshot returned error: %v", err)
		}
	}
	if err := store.SetPublished(state.PublishedRecord{
		SnapshotName: "MERGED-ubuntu-focal-main_2026-04-01",
		Path:         "preprod",
		Suite:        "focal",
		Component:    "main",
		PublishedAt:  old,
	}); err != nil {
		t.Fatalf("SetPublished returned error: %v", err)
	}

	result, err := runCleanup("ubuntu", cli.Command{Name: "cleanup", CleanupAll: true})
	if err != nil {
		t.Fatalf("runCleanup returned error: %v", err)
	}
	if !reflect.DeepEqual(result.SnapshotCandidates, []string{"ubuntu-focal-main_2026-03-31", "ubuntu-focal-universe_2026-04-01"}) {
		t.Fatalf("unexpected snapshot candidates: %#v", result.SnapshotCandidates)
	}
	if _, err := store.Snapshot("MERGED-ubuntu-focal-main_2026-04-01"); err != nil {
		t.Fatalf("expected published merged snapshot to remain: %v", err)
	}
	if _, err := store.Snapshot("ubuntu-focal-main_2026-04-01"); err != nil {
		t.Fatalf("expected regular companion snapshot to remain: %v", err)
	}
	if _, err := store.Snapshot("ubuntu-focal-universe_2026-04-01"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected unrelated same-date snapshot to be removed, got err=%v", err)
	}
}

func TestRunCleanupDaysUsesPublishedSnapshotDate(t *testing.T) {
	home := t.TempDir()
	setAppTestHome(t, home)
	store := openAppTestStore(t, home, "ubuntu")
	defer func() {
		_ = store.Close()
	}()
	for _, name := range []string{
		"ubuntu-focal-main_2026-03-31",
		"ubuntu-focal-main_2026-04-02",
		"ubuntu-focal-main_2026-05-01",
	} {
		if err := store.CreateSnapshot(state.SnapshotRecord{Name: name, Kind: "regular", CreatedAt: testCleanupTime(name)}, nil); err != nil {
			t.Fatalf("CreateSnapshot returned error: %v", err)
		}
	}
	if err := store.SetPublished(state.PublishedRecord{
		SnapshotName: "ubuntu-focal-main_2026-05-01",
		Path:         "preprod",
		Suite:        "focal",
		Component:    "main",
		PublishedAt:  testCleanupTime("ubuntu-focal-main_2026-05-01"),
	}); err != nil {
		t.Fatalf("SetPublished returned error: %v", err)
	}

	result, err := runCleanup("ubuntu", cli.Command{Name: "cleanup", CleanupDaysSet: true, CleanupDays: 30})
	if err != nil {
		t.Fatalf("runCleanup returned error: %v", err)
	}
	if result.CutoffDate != "2026-04-01" {
		t.Fatalf("unexpected cutoff: %q", result.CutoffDate)
	}
	if !reflect.DeepEqual(result.SnapshotCandidates, []string{"ubuntu-focal-main_2026-03-31"}) {
		t.Fatalf("unexpected snapshot candidates: %#v", result.SnapshotCandidates)
	}
	if _, err := store.Snapshot("ubuntu-focal-main_2026-04-02"); err != nil {
		t.Fatalf("expected snapshot inside retention window to remain: %v", err)
	}
}

func TestRunDestroyRemovesOnlyUnsharedPackages(t *testing.T) {
	home := t.TempDir()
	appCfg := setAppTestHome(t, home)
	first := openAppTestStore(t, home, "ubuntu-one")
	second := openAppTestStore(t, home, "ubuntu-two")
	defer func() {
		_ = first.Close()
		_ = second.Close()
	}()
	sharedPath := "pool/main/s/shared/shared.deb"
	ownedPath := "pool/main/o/owned/owned.deb"
	upsertAppTestPackage(t, first, "shared", sharedPath)
	upsertAppTestPackage(t, first, "owned", ownedPath)
	upsertAppTestPackage(t, second, "shared", sharedPath)
	writePoolFile(t, home, sharedPath, "shared")
	writePoolFile(t, home, ownedPath, "owned")

	result, err := runDestroy("ubuntu-one")
	if err != nil {
		t.Fatalf("runDestroy returned error: %v", err)
	}
	if result.PackageFiles != 1 || result.SharedPreserved != 1 {
		t.Fatalf("unexpected destroy result: %#v", result)
	}
	if _, err := os.Stat(appCfg.DBPath("ubuntu-one")); !os.IsNotExist(err) {
		t.Fatalf("expected destroyed DB to be removed, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(appCfg.PackageDir(), filepath.FromSlash(ownedPath))); !os.IsNotExist(err) {
		t.Fatalf("expected owned package to be removed, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(appCfg.PackageDir(), filepath.FromSlash(sharedPath))); err != nil {
		t.Fatalf("expected shared package to remain: %v", err)
	}
}

func writeTempConfig(t *testing.T) string {
	t.Helper()
	path := t.TempDir() + "/mirror.conf"
	content := `[mirror]
name = ubuntu
url = http://us.archive.ubuntu.com/ubuntu/
dist = trusty, xenial, bionic, focal, jammy
release = default, security, updates
origin = aptly_default
label = aptly_default
arch = amd64
components = main, restricted, multiverse, universe
path = preprod
server = http://mirror.example.test
gpg_key = 560CE107BECFB86BF8BED1DBD9FEEBA651DA48E7
gpg_passphrase = 1234
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	return path
}

func isolateAppConfig(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
}

func setAppTestHome(t *testing.T, home string) appconfig.Config {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	return appconfig.Default()
}

func openAppTestStore(t *testing.T, home, name string) *state.Store {
	t.Helper()
	appCfg := appconfig.Default()
	store, err := state.Open(appCfg.DBPath(name))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	if err := store.SaveMirrorConfig(config.Mirror{
		Name:       name,
		URL:        "http://us.archive.ubuntu.com/ubuntu/",
		Dists:      []string{"focal"},
		Releases:   []string{"default"},
		Origin:     "default",
		Label:      "default",
		Arch:       []string{"amd64"},
		Components: []string{"main"},
		Path:       "preprod",
	}); err != nil {
		t.Fatalf("SaveMirrorConfig returned error: %v", err)
	}
	return store
}

func writePoolFile(t *testing.T, home, poolPath, content string) {
	t.Helper()
	appCfg := appconfig.Default()
	fullPath := filepath.Join(appCfg.PackageDir(), filepath.FromSlash(poolPath))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
}

func seedPublishedSnapshot(t *testing.T, store *state.Store, name string) {
	t.Helper()
	now := testCleanupTime(name)
	if err := store.CreateSnapshot(state.SnapshotRecord{Name: name, Kind: "regular", CreatedAt: now}, nil); err != nil {
		t.Fatalf("CreateSnapshot returned error: %v", err)
	}
	if err := store.SetPublished(state.PublishedRecord{
		SnapshotName: name,
		Path:         "preprod",
		Suite:        "focal",
		Component:    "main",
		PublishedAt:  now,
	}); err != nil {
		t.Fatalf("SetPublished returned error: %v", err)
	}
}

func testCleanupTime(snapshotName string) time.Time {
	date := snapshotDate(snapshotName)
	if date == "" {
		date = "2026-05-28"
	}
	parsed, err := time.Parse("2006-01-02", date)
	if err != nil {
		panic(err)
	}
	return parsed
}

func upsertAppTestPackage(t *testing.T, store *state.Store, name, poolPath string) string {
	t.Helper()
	key, err := store.UpsertPackage(state.PackageRecord{
		Name:         name,
		Version:      "1.0",
		Architecture: "amd64",
		Filename:     poolPath,
		Component:    "main",
		Source:       name,
		Size:         int64(len("package")),
		MD5:          strings.Repeat("1", 32),
		SHA1:         strings.Repeat("2", 40),
		SHA256:       strings.Repeat("3", 64),
		SHA512:       strings.Repeat("4", 128),
		PoolPath:     poolPath,
		Fields:       map[string]string{"Package": name},
	})
	if err != nil {
		t.Fatalf("UpsertPackage returned error: %v", err)
	}
	return key
}

func createMirrorDB(t *testing.T, dbPath string) {
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

func captureStdout(fn func() error) (string, error) {
	old := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		return "", err
	}
	os.Stdout = writer

	runErr := fn()

	_ = writer.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, copyErr := io.Copy(&buf, reader)
	_ = reader.Close()
	if runErr != nil {
		return buf.String(), runErr
	}
	return buf.String(), copyErr
}
