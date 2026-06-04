package app

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"mirrors/internal/appconfig"
	"mirrors/internal/cli"
	"mirrors/internal/config"
	"mirrors/internal/download"
	"mirrors/internal/logging"
	"mirrors/internal/mirror"
	"mirrors/internal/publish"
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

func TestRunWritesConfiguredLogFileWithoutChangingCommandOutput(t *testing.T) {
	home := t.TempDir()
	xdg := filepath.Join(home, ".config")
	logFile := filepath.Join(home, "logs", "mirrors.log")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	if err := os.MkdirAll(xdg, 0755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(xdg, "mirrors.conf"), []byte("log_level = debug\nlog_file = "+logFile+"\n"), 0644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	output, err := captureStdout(func() error {
		return Run([]string{"list"})
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !strings.Contains(output, "No mirrors found") {
		t.Fatalf("unexpected stdout: %q", output)
	}
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("expected configured log file: %v", err)
	}
	text := string(data)
	for _, want := range []string{`command start name="list"`, `app config path=`, `command complete name="list"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("log missing %q:\n%s", want, text)
		}
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
	validateConfig = func(_ context.Context, _ appconfig.Config, cfg config.Mirror, _ logging.Logger) ([]config.UpstreamRelease, error) {
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

func TestRunConfigShowAnnotatesFileValuesChangedFromDB(t *testing.T) {
	home := t.TempDir()
	appCfg := setAppTestHome(t, home)
	store := openAppTestStore(t, home, "ubuntu")
	defer func() {
		_ = store.Close()
	}()
	configPath := writeAppMirrorConfig(t, "ubuntu", "weekly")
	cfg, err := store.MirrorConfig()
	if err != nil {
		t.Fatalf("MirrorConfig returned error: %v", err)
	}
	cfg.ConfigPath = configPath
	cfg.UpdatePolicy = "never"
	if err := store.SaveMirrorConfig(cfg); err != nil {
		t.Fatalf("SaveMirrorConfig returned error: %v", err)
	}

	output, err := captureStdout(func() error {
		return runConfigWithConfigAndLogger(cli.Command{Name: "config", Subcommand: "show", NameRef: "ubuntu"}, appCfg, logging.Nop())
	})
	if err != nil {
		t.Fatalf("runConfig returned error: %v", err)
	}
	if !strings.Contains(output, `update = weekly (was "never")`) {
		t.Fatalf("output missing changed update annotation:\n%s", output)
	}
}

func TestRunConfigShowWarnsAndFallsBackToDBWhenStoredConfigUnreadable(t *testing.T) {
	home := t.TempDir()
	appCfg := setAppTestHome(t, home)
	store := openAppTestStore(t, home, "ubuntu")
	defer func() {
		_ = store.Close()
	}()
	cfg, err := store.MirrorConfig()
	if err != nil {
		t.Fatalf("MirrorConfig returned error: %v", err)
	}
	cfg.ConfigPath = filepath.Join(home, "missing.conf")
	cfg.UpdatePolicy = "never"
	if err := store.SaveMirrorConfig(cfg); err != nil {
		t.Fatalf("SaveMirrorConfig returned error: %v", err)
	}

	output, err := captureStdout(func() error {
		return runConfigWithConfigAndLogger(cli.Command{Name: "config", Subcommand: "show", NameRef: "ubuntu"}, appCfg, logging.Nop())
	})
	if err != nil {
		t.Fatalf("runConfig returned error: %v", err)
	}
	for _, want := range []string{
		"Warning: could not read stored config",
		"Restore or fix the config file",
		"update = never",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
}

func TestRunConfigShowByConfigAnnotatesFileValuesChangedFromDB(t *testing.T) {
	home := t.TempDir()
	appCfg := setAppTestHome(t, home)
	store := openAppTestStore(t, home, "ubuntu")
	defer func() {
		_ = store.Close()
	}()
	configPath := writeAppMirrorConfig(t, "ubuntu", "weekly")
	cfg, err := store.MirrorConfig()
	if err != nil {
		t.Fatalf("MirrorConfig returned error: %v", err)
	}
	cfg.ConfigPath = configPath
	cfg.UpdatePolicy = "never"
	if err := store.SaveMirrorConfig(cfg); err != nil {
		t.Fatalf("SaveMirrorConfig returned error: %v", err)
	}

	output, err := captureStdout(func() error {
		return runConfigWithConfigAndLogger(cli.Command{Name: "config", Subcommand: "show", ConfigPath: configPath}, appCfg, logging.Nop())
	})
	if err != nil {
		t.Fatalf("runConfig returned error: %v", err)
	}
	if !strings.Contains(output, `update = weekly (was "never")`) {
		t.Fatalf("output missing changed update annotation:\n%s", output)
	}
}

func TestDBBackedMirrorCommandsRefreshStoredConfigBeforeUse(t *testing.T) {
	for _, command := range []string{"rollback", "hide", "cleanup", "info", "more-info"} {
		t.Run(command, func(t *testing.T) {
			home := t.TempDir()
			appCfg := setAppTestHome(t, home)
			store := openAppTestStore(t, home, "ubuntu")
			defer func() {
				_ = store.Close()
			}()
			configPath := writeAppMirrorConfig(t, "ubuntu", "weekly")
			cfg, err := store.MirrorConfig()
			if err != nil {
				t.Fatalf("MirrorConfig returned error: %v", err)
			}
			cfg.ConfigPath = configPath
			cfg.UpdatePolicy = "never"
			cfg.Signing.Disabled = false
			if err := store.SaveMirrorConfig(cfg); err != nil {
				t.Fatalf("SaveMirrorConfig returned error: %v", err)
			}
			cmd := cli.Command{Name: command, NameRef: "ubuntu"}
			prepareDBBackedCommandFixture(t, home, store, &cmd)

			_, err = captureStdout(func() error {
				return runMirrorCommandWithConfigAndLogger(cmd, appCfg, logging.Nop())
			})
			if err != nil {
				t.Fatalf("runMirrorCommand returned error: %v", err)
			}
			refreshed, err := store.MirrorConfig()
			if err != nil {
				t.Fatalf("MirrorConfig refreshed returned error: %v", err)
			}
			if refreshed.UpdatePolicy != "weekly" || !refreshed.Signing.Disabled {
				t.Fatalf("stored config was not refreshed from file: %#v", refreshed)
			}
		})
	}
}

func TestDBBackedMirrorCommandsWarnAndContinueWhenStoredConfigMissing(t *testing.T) {
	for _, command := range []string{"rollback", "hide", "cleanup", "info", "more-info"} {
		t.Run(command, func(t *testing.T) {
			home := t.TempDir()
			appCfg := setAppTestHome(t, home)
			store := openAppTestStore(t, home, "ubuntu")
			defer func() {
				_ = store.Close()
			}()
			cfg, err := store.MirrorConfig()
			if err != nil {
				t.Fatalf("MirrorConfig returned error: %v", err)
			}
			cfg.ConfigPath = filepath.Join(home, "missing.conf")
			cfg.Signing.Disabled = true
			if err := store.SaveMirrorConfig(cfg); err != nil {
				t.Fatalf("SaveMirrorConfig returned error: %v", err)
			}
			cmd := cli.Command{Name: command, NameRef: "ubuntu"}
			prepareDBBackedCommandFixture(t, home, store, &cmd)

			output, err := captureStdout(func() error {
				return runMirrorCommandWithConfigAndLogger(cmd, appCfg, logging.Nop())
			})
			if err != nil {
				t.Fatalf("runMirrorCommand returned error: %v\n%s", err, output)
			}
			for _, want := range []string{"Warning: could not read stored config", "Restore or fix the config file"} {
				if !strings.Contains(output, want) {
					t.Fatalf("output missing %q:\n%s", want, output)
				}
			}
		})
	}
}

func TestPrintDownloadPlanIncludesEstimateAndWarnings(t *testing.T) {
	output, err := captureStdout(func() error {
		printDownloadPlan(mirror.DownloadPlan{
			MirrorName:             "ubuntu",
			PackagePoolRoot:        "/tmp/packages",
			IndexesConsidered:      2,
			PackagesReused:         3,
			PackagesToDownload:     4,
			EstimatedDownloadBytes: 2048,
			AvailableBytes:         4096,
			UnknownSizePackages:    1,
			Warnings:               []string{"1 package(s) have unknown size metadata; estimated download size covers only packages with known sizes"},
		})
		return nil
	})
	if err != nil {
		t.Fatalf("captureStdout returned error: %v", err)
	}
	for _, want := range []string{
		`Download plan for mirror "ubuntu"`,
		"Indexes considered: 2",
		"Packages reused: 3",
		"Packages to download: 4",
		"Estimated download size: 2.0 KiB",
		"Packages with unknown size: 1",
		"Available disk space: 4.0 KiB",
		"Warning: 1 package(s) have unknown size metadata",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
}

func TestTerminalProgressReporterPrintsLineFallbackWhenCaptured(t *testing.T) {
	output, err := captureStdout(func() error {
		reporter := newTerminalProgressReporter()
		reporter.Start(mirror.DownloadProgressStart{
			TotalPackages:   1,
			TotalKnownBytes: 2048,
			ReusedPackages:  3,
		})
		reporter.Bytes(mirror.DownloadProgressBytes{
			Filename:     "pool/main/d/demo/demo_1.0_amd64.deb",
			CurrentBytes: 2048,
			TotalBytes:   2048,
		})
		reporter.PackageComplete(mirror.DownloadProgressPackageComplete{
			Filename: "pool/main/d/demo/demo_1.0_amd64.deb",
			Size:     2048,
		})
		reporter.Finish(mirror.DownloadProgressFinish{
			DownloadedPackages: 1,
			ReusedPackages:     3,
			TotalPackages:      1,
			DownloadedBytes:    2048,
			TotalKnownBytes:    2048,
		})
		return nil
	})
	if err != nil {
		t.Fatalf("captureStdout returned error: %v", err)
	}
	for _, want := range []string{
		"Downloading packages: 1 package(s), 2.0 KiB known",
		"Downloaded 1/1: demo_1.0_amd64.deb",
		"Download complete: downloaded 1, reused 3, failed 0",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
}

func TestTerminalProgressReporterUsesAbsolutePackageBytes(t *testing.T) {
	reporter := newTerminalProgressReporter()
	reporter.Start(mirror.DownloadProgressStart{
		TotalPackages:   1,
		TotalKnownBytes: 2048,
	})
	reporter.Bytes(mirror.DownloadProgressBytes{
		Filename:     "pool/main/d/demo/demo_1.0_amd64.deb",
		CurrentBytes: 1024,
		TotalBytes:   2048,
	})
	reporter.Bytes(mirror.DownloadProgressBytes{
		Filename:     "pool/main/d/demo/demo_1.0_amd64.deb",
		CurrentBytes: 512,
		TotalBytes:   2048,
	})
	reporter.Bytes(mirror.DownloadProgressBytes{
		Filename:     "pool/main/d/demo/demo_1.0_amd64.deb",
		CurrentBytes: 2048,
		TotalBytes:   2048,
	})
	if reporter.knownBytes != 2048 {
		t.Fatalf("expected absolute byte progress to avoid retry over-counting, got %d", reporter.knownBytes)
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

func TestCreateAllowsUpdateNever(t *testing.T) {
	home := t.TempDir()
	appCfg := setAppTestHome(t, home)
	configPath := writeAppMirrorConfig(t, "ubuntu", "never")
	var called bool
	restore := replaceUpdateWorkflow(func(action string, _ *mirror.Service, _ appconfig.Config, cfg config.Mirror, _ logging.Logger) error {
		called = true
		if action != "Create" || cfg.UpdatePolicy != "never" {
			t.Fatalf("unexpected workflow call action=%q cfg=%#v", action, cfg)
		}
		if err := saveMirrorConfigWithConfig(cfg, appCfg); err != nil {
			t.Fatalf("saveMirrorConfigWithConfig returned error: %v", err)
		}
		return nil
	})
	defer restore()

	if err := runConfigDrivenMirrorCommandWithLogger(cli.Command{Name: "create", ConfigPath: configPath}, appCfg, logging.Nop()); err != nil {
		t.Fatalf("create returned error: %v", err)
	}
	if !called {
		t.Fatal("expected create workflow to run")
	}
	loaded, err := state.LoadMirrorConfig(appCfg.DBPath("ubuntu"))
	if err != nil {
		t.Fatalf("LoadMirrorConfig returned error: %v", err)
	}
	if loaded.UpdatePolicy != "never" {
		t.Fatalf("expected stored never policy, got %#v", loaded)
	}
}

func TestUpdateConfigNeverSavesThenRefuses(t *testing.T) {
	home := t.TempDir()
	appCfg := setAppTestHome(t, home)
	configPath := writeAppMirrorConfig(t, "ubuntu", "never")
	restore := replaceUpdateWorkflow(func(string, *mirror.Service, appconfig.Config, config.Mirror, logging.Logger) error {
		t.Fatal("update workflow must not run for update = never")
		return nil
	})
	defer restore()

	err := runUpdateCommandWithLogger(cli.Command{Name: "update", ConfigPath: configPath}, appCfg, logging.Nop())
	if err == nil || !strings.Contains(err.Error(), "update policy is never") {
		t.Fatalf("expected never policy error, got %v", err)
	}
	loaded, loadErr := state.LoadMirrorConfig(appCfg.DBPath("ubuntu"))
	if loadErr != nil {
		t.Fatalf("LoadMirrorConfig returned error: %v", loadErr)
	}
	if loaded.UpdatePolicy != "never" {
		t.Fatalf("expected DB to be saved before refusal, got %#v", loaded)
	}
}

func TestUpdateNameRefreshesConfigBeforePolicyDecision(t *testing.T) {
	home := t.TempDir()
	appCfg := setAppTestHome(t, home)
	configPath := writeAppMirrorConfig(t, "ubuntu", "")
	store := openAppTestStore(t, home, "ubuntu")
	cfg, err := store.MirrorConfig()
	if err != nil {
		t.Fatalf("MirrorConfig returned error: %v", err)
	}
	cfg.ConfigPath = configPath
	cfg.UpdatePolicy = "never"
	if err := store.SaveMirrorConfig(cfg); err != nil {
		t.Fatalf("SaveMirrorConfig returned error: %v", err)
	}
	_ = store.Close()
	var called bool
	restore := replaceUpdateWorkflow(func(action string, _ *mirror.Service, _ appconfig.Config, cfg config.Mirror, _ logging.Logger) error {
		called = true
		if action != "Update" || cfg.UpdatePolicy != "" {
			t.Fatalf("unexpected refreshed config: action=%q cfg=%#v", action, cfg)
		}
		return nil
	})
	defer restore()

	if err := runUpdateCommandWithLogger(cli.Command{Name: "update", NameRef: "ubuntu"}, appCfg, logging.Nop()); err != nil {
		t.Fatalf("update returned error after removing never: %v", err)
	}
	if !called {
		t.Fatal("expected update workflow to run after refreshed config removed never")
	}
	loaded, err := state.LoadMirrorConfig(appCfg.DBPath("ubuntu"))
	if err != nil {
		t.Fatalf("LoadMirrorConfig returned error: %v", err)
	}
	if loaded.UpdatePolicy != "" {
		t.Fatalf("expected refreshed DB policy, got %#v", loaded)
	}
}

func TestUpdateNameRefreshesNeverAndRefuses(t *testing.T) {
	home := t.TempDir()
	appCfg := setAppTestHome(t, home)
	configPath := writeAppMirrorConfig(t, "ubuntu", "never")
	store := openAppTestStore(t, home, "ubuntu")
	cfg, err := store.MirrorConfig()
	if err != nil {
		t.Fatalf("MirrorConfig returned error: %v", err)
	}
	cfg.ConfigPath = configPath
	cfg.UpdatePolicy = ""
	if err := store.SaveMirrorConfig(cfg); err != nil {
		t.Fatalf("SaveMirrorConfig returned error: %v", err)
	}
	_ = store.Close()
	restore := replaceUpdateWorkflow(func(string, *mirror.Service, appconfig.Config, config.Mirror, logging.Logger) error {
		t.Fatal("update workflow must not run after refreshed never policy")
		return nil
	})
	defer restore()

	err = runUpdateCommandWithLogger(cli.Command{Name: "update", NameRef: "ubuntu"}, appCfg, logging.Nop())
	if err == nil || !strings.Contains(err.Error(), "update policy is never") {
		t.Fatalf("expected never policy error, got %v", err)
	}
}

func TestPeriodicShouldRunUsesCalendarMonth(t *testing.T) {
	published := state.PublishedRecord{SnapshotName: "ubuntu-focal-main_2026-01-31"}
	shouldRun, message, err := periodicShouldRunForPublished("ubuntu", "monthly", published, time.Date(2026, 2, 27, 12, 0, 0, 0, time.Local))
	if err != nil {
		t.Fatalf("periodicShouldRunForPublished returned error: %v", err)
	}
	if shouldRun {
		t.Fatalf("expected monthly to skip before calendar month: %q", message)
	}
	shouldRun, message, err = periodicShouldRunForPublished("ubuntu", "monthly", published, time.Date(2026, 2, 28, 12, 0, 0, 0, time.Local))
	if err != nil {
		t.Fatalf("periodicShouldRunForPublished returned error: %v", err)
	}
	if !shouldRun {
		t.Fatalf("expected monthly to run on clamped next-month date: %q", message)
	}
}

func TestPeriodicBatchSkipsWithTerminalAndInfoLogReasons(t *testing.T) {
	home := t.TempDir()
	appCfg := setAppTestHome(t, home)
	logPath := filepath.Join(t.TempDir(), "mirrors.log")
	logger, err := logging.OpenFile(logPath, logging.Info)
	if err != nil {
		t.Fatalf("OpenFile returned error: %v", err)
	}
	defer func() {
		_ = logger.Close()
	}()
	store := openAppTestStore(t, home, "ubuntu")
	cfg, err := store.MirrorConfig()
	if err != nil {
		t.Fatalf("MirrorConfig returned error: %v", err)
	}
	configPath := writeAppMirrorConfig(t, "ubuntu", "")
	cfg.ConfigPath = configPath
	if err := store.SaveMirrorConfig(cfg); err != nil {
		t.Fatalf("SaveMirrorConfig returned error: %v", err)
	}
	seedPublishedSnapshot(t, store, "ubuntu-focal-main_"+time.Now().Local().Format("2006-01-02"))
	_ = store.Close()

	output, err := captureStdout(func() error {
		return runPeriodicBatchCommandWithLogger("daily", appCfg, logger)
	})
	if err != nil {
		t.Fatalf("runPeriodicBatchCommandWithLogger returned error: %v", err)
	}
	if !strings.Contains(output, "no update policy configured") {
		t.Fatalf("terminal output missing skip reason:\n%s", output)
	}
	if err := logger.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if !strings.Contains(string(data), "no update policy configured") {
		t.Fatalf("info log missing skip reason:\n%s", string(data))
	}
}

func TestPeriodicBatchRunsOnlyMatchingDuePublishedMirrors(t *testing.T) {
	home := t.TempDir()
	appCfg := setAppTestHome(t, home)
	var updated []string
	restore := replaceUpdateWorkflow(func(_ string, _ *mirror.Service, _ appconfig.Config, cfg config.Mirror, _ logging.Logger) error {
		updated = append(updated, cfg.Name)
		return nil
	})
	defer restore()
	createPeriodicMirror(t, home, "due", "daily", time.Now().Local().AddDate(0, 0, -2).Format("2006-01-02"), false)
	createPeriodicMirror(t, home, "weekly", "weekly", time.Now().Local().AddDate(0, 0, -10).Format("2006-01-02"), false)
	createPeriodicMirror(t, home, "never", "never", time.Now().Local().AddDate(0, 0, -2).Format("2006-01-02"), false)
	createPeriodicMirror(t, home, "unpublished", "daily", "", false)
	createPeriodicMirror(t, home, "not-due", "daily", time.Now().Local().Format("2006-01-02"), false)

	output, err := captureStdout(func() error {
		return runPeriodicBatchCommandWithLogger("daily", appCfg, logging.Nop())
	})
	if err != nil {
		t.Fatalf("runPeriodicBatchCommandWithLogger returned error: %v", err)
	}
	if !reflect.DeepEqual(updated, []string{"due"}) {
		t.Fatalf("unexpected updated mirrors: %#v", updated)
	}
	for _, want := range []string{"update policy is weekly", "update policy is never", "mirror is not published", "Daily skipped for mirror \"not-due\""} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
}

func TestPeriodicBatchContinuesAfterOrdinaryFailureAndStopsOnDiskFailure(t *testing.T) {
	home := t.TempDir()
	appCfg := setAppTestHome(t, home)
	createPeriodicMirror(t, home, "first", "daily", time.Now().Local().AddDate(0, 0, -2).Format("2006-01-02"), false)
	createPeriodicMirror(t, home, "second", "daily", time.Now().Local().AddDate(0, 0, -2).Format("2006-01-02"), false)
	var updated []string
	restore := replaceUpdateWorkflow(func(_ string, _ *mirror.Service, _ appconfig.Config, cfg config.Mirror, _ logging.Logger) error {
		updated = append(updated, cfg.Name)
		if cfg.Name == "first" {
			return errors.New("ordinary failure")
		}
		return nil
	})
	err := runPeriodicBatchCommandWithLogger("daily", appCfg, logging.Nop())
	restore()
	if err == nil || !strings.Contains(err.Error(), "1 mirror failure") {
		t.Fatalf("expected aggregate failure, got %v", err)
	}
	if !reflect.DeepEqual(updated, []string{"first", "second"}) {
		t.Fatalf("expected batch to continue after ordinary failure, got %#v", updated)
	}

	updated = nil
	restore = replaceUpdateWorkflow(func(_ string, _ *mirror.Service, _ appconfig.Config, cfg config.Mirror, _ logging.Logger) error {
		updated = append(updated, cfg.Name)
		return mirror.ErrInsufficientDiskSpace
	})
	err = runPeriodicBatchCommandWithLogger("daily", appCfg, logging.Nop())
	restore()
	if !errors.Is(err, mirror.ErrInsufficientDiskSpace) {
		t.Fatalf("expected disk failure, got %v", err)
	}
	if !reflect.DeepEqual(updated, []string{"first"}) {
		t.Fatalf("expected batch to stop after disk failure, got %#v", updated)
	}
}

func TestPeriodicBatchStopsOnDiskFailureDuringConfigRefresh(t *testing.T) {
	home := t.TempDir()
	appCfg := setAppTestHome(t, home)
	createPeriodicMirror(t, home, "first", "daily", time.Now().Local().AddDate(0, 0, -2).Format("2006-01-02"), false)
	createPeriodicMirror(t, home, "second", "daily", time.Now().Local().AddDate(0, 0, -2).Format("2006-01-02"), false)
	oldRefresh := refreshMirrorConfigForPeriodic
	refreshMirrorConfigForPeriodic = func(name string, appCfg appconfig.Config, logger logging.Logger) (config.Mirror, error) {
		if name == "first" {
			return config.Mirror{}, syscall.ENOSPC
		}
		return oldRefresh(name, appCfg, logger)
	}
	defer func() {
		refreshMirrorConfigForPeriodic = oldRefresh
	}()
	var updated []string
	restore := replaceUpdateWorkflow(func(_ string, _ *mirror.Service, _ appconfig.Config, cfg config.Mirror, _ logging.Logger) error {
		updated = append(updated, cfg.Name)
		return nil
	})
	defer restore()

	err := runPeriodicBatchCommandWithLogger("daily", appCfg, logging.Nop())
	if !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("expected ENOSPC to stop batch, got %v", err)
	}
	if len(updated) != 0 {
		t.Fatalf("expected no updates after config refresh disk failure, got %#v", updated)
	}
}

func TestRollbackWorkflowCommitsPublishedStateAfterPublishAndSign(t *testing.T) {
	home := t.TempDir()
	appCfg := setAppTestHome(t, home)
	store := openAppTestStore(t, home, "ubuntu")
	defer func() {
		_ = store.Close()
	}()
	cfg, err := store.MirrorConfig()
	if err != nil {
		t.Fatalf("MirrorConfig returned error: %v", err)
	}
	cfg.Signing.Disabled = true
	if err := store.SaveMirrorConfig(cfg); err != nil {
		t.Fatalf("SaveMirrorConfig returned error: %v", err)
	}
	oldSnapshot := "ubuntu-focal-main_2026-05-27"
	newSnapshot := "ubuntu-focal-main_2026-05-28"
	seedPublishedSnapshot(t, store, oldSnapshot)
	key := upsertAppTestPackage(t, store, "apt", "pool/main/a/apt/apt.deb")
	writePoolFile(t, home, "pool/main/a/apt/apt.deb", "package")
	if err := store.CreateSnapshot(state.SnapshotRecord{Name: newSnapshot, Kind: "regular", CreatedAt: testCleanupTime(newSnapshot)}, []string{key}); err != nil {
		t.Fatalf("CreateSnapshot returned error: %v", err)
	}

	output, err := captureStdout(func() error {
		return runMirrorCommandWithConfigAndLogger(cli.Command{Name: "rollback", NameRef: "ubuntu", ID: newSnapshot}, appCfg, logging.Nop())
	})
	if err != nil {
		t.Fatalf("runMirrorCommand returned error: %v\n%s", err, output)
	}
	published, err := store.Published()
	if err != nil {
		t.Fatalf("Published returned error: %v", err)
	}
	if published.SnapshotName != newSnapshot {
		t.Fatalf("published state was not committed after successful rollback: %#v", published)
	}
	if published.Path != filepath.Join(appCfg.MirrorsRoot, "preprod") {
		t.Fatalf("expected actual published path, got %#v", published)
	}
	last, err := store.LastUpdate()
	if err != nil {
		t.Fatalf("LastUpdate returned error: %v", err)
	}
	if last.Action != "rollback" || !strings.Contains(last.Message, newSnapshot) {
		t.Fatalf("unexpected update history: %#v", last)
	}
}

func TestRollbackWorkflowDoesNotCommitPublishedStateWhenSigningFails(t *testing.T) {
	home := t.TempDir()
	appCfg := setAppTestHome(t, home)
	store := openAppTestStore(t, home, "ubuntu")
	defer func() {
		_ = store.Close()
	}()
	cfg, err := store.MirrorConfig()
	if err != nil {
		t.Fatalf("MirrorConfig returned error: %v", err)
	}
	cfg.Signing.Disabled = false
	cfg.Signing.GPGKey = "test-key"
	cfg.Signing.GPGPassphraseFile = filepath.Join(home, "missing-passphrase")
	if err := store.SaveMirrorConfig(cfg); err != nil {
		t.Fatalf("SaveMirrorConfig returned error: %v", err)
	}
	oldSnapshot := "ubuntu-focal-main_2026-05-27"
	newSnapshot := "ubuntu-focal-main_2026-05-28"
	seedPublishedSnapshot(t, store, oldSnapshot)
	key := upsertAppTestPackage(t, store, "apt", "pool/main/a/apt/apt.deb")
	writePoolFile(t, home, "pool/main/a/apt/apt.deb", "package")
	if err := store.CreateSnapshot(state.SnapshotRecord{Name: newSnapshot, Kind: "regular", CreatedAt: testCleanupTime(newSnapshot)}, []string{key}); err != nil {
		t.Fatalf("CreateSnapshot returned error: %v", err)
	}

	_, err = captureStdout(func() error {
		return runMirrorCommandWithConfigAndLogger(cli.Command{Name: "rollback", NameRef: "ubuntu", ID: newSnapshot}, appCfg, logging.Nop())
	})
	if err == nil {
		t.Fatal("expected signing failure")
	}
	published, err := store.Published()
	if err != nil {
		t.Fatalf("Published returned error: %v", err)
	}
	if published.SnapshotName != oldSnapshot {
		t.Fatalf("published state changed despite signing failure: %#v", published)
	}
}

func TestRunPublishUpdateDoesNotCreateSnapshotWhenFetchFails(t *testing.T) {
	home := t.TempDir()
	appCfg := setAppTestHome(t, home)
	repo := newAppRepoFixture("http://repo.test/ubuntu", "1.0", "package")
	service, err := mirror.NewService(
		mirror.WithStorageDirs(appCfg.DBDir(), appCfg.PackageDir()),
		mirror.WithDownloader(&appFakeDownloader{files: map[string][]byte{}}),
		mirror.WithDiskSpaceChecker(appDiskSpaceChecker{available: 1024}),
	)
	if err != nil {
		t.Fatalf("NewService returned error: %v", err)
	}

	_, err = captureStdout(func() error {
		return runPublishUpdateWithLogger("Update", service, appCfg, repo.config, logging.Nop())
	})
	if err == nil {
		t.Fatal("expected fetch failure")
	}
	store, err := state.Open(appCfg.DBPath(repo.config.Name))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer func() {
		_ = store.Close()
	}()
	snapshots, err := store.Snapshots()
	if err != nil {
		t.Fatalf("Snapshots returned error: %v", err)
	}
	if len(snapshots) != 0 {
		t.Fatalf("failed fetch created snapshots: %#v", snapshots)
	}
}

func TestRunPublishUpdateDoesNotCommitPublishedStateWhenPublishFails(t *testing.T) {
	home := t.TempDir()
	appCfg := setAppTestHome(t, home)
	repo := newAppRepoFixture("http://repo.test/ubuntu", "1.0", "package")
	oldSnapshot := "ubuntu-focal-main_2026-05-27"
	store := openAppTestStore(t, home, repo.config.Name)
	seedPublishedSnapshot(t, store, oldSnapshot)
	_ = store.Close()

	service, err := mirror.NewService(
		mirror.WithStorageDirs(appCfg.DBDir(), appCfg.PackageDir()),
		mirror.WithDownloader(newAppFakeDownloader(repo.files)),
		mirror.WithDiskSpaceChecker(appDiskSpaceChecker{available: 1024}),
	)
	if err != nil {
		t.Fatalf("NewService returned error: %v", err)
	}
	restore := replaceWorkflowPublisher(func(_ appconfig.Config) (workflowPublisher, error) {
		return failingWorkflowPublisher{err: fmt.Errorf("publish failed")}, nil
	})
	defer restore()

	_, err = captureStdout(func() error {
		return runPublishUpdateWithLogger("Update", service, appCfg, repo.config, logging.Nop())
	})
	if err == nil || !strings.Contains(err.Error(), "publish failed") {
		t.Fatalf("expected publish failure, got %v", err)
	}
	store, err = state.Open(appCfg.DBPath(repo.config.Name))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer func() {
		_ = store.Close()
	}()
	published, err := store.Published()
	if err != nil {
		t.Fatalf("Published returned error: %v", err)
	}
	if published.SnapshotName != oldSnapshot {
		t.Fatalf("published state changed despite publish failure: %#v", published)
	}
	if _, err := store.Snapshot(mirror.SnapshotName("ubuntu-focal-main", localDate(time.Now()).Format("2006-01-02"))); err != nil {
		t.Fatalf("clean fetch should create current snapshot before publish failure: %v", err)
	}
}

func TestRunPublishUpdateDoesNotCommitPublishedStateWhenSigningFails(t *testing.T) {
	home := t.TempDir()
	appCfg := setAppTestHome(t, home)
	repo := newAppRepoFixture("http://repo.test/ubuntu", "1.0", "package")
	repo.config.Signing.Disabled = false
	repo.config.Signing.GPGKey = "test-key"
	repo.config.Signing.GPGPassphraseFile = filepath.Join(home, "missing-passphrase")
	oldSnapshot := "ubuntu-focal-main_2026-05-27"
	store := openAppTestStore(t, home, repo.config.Name)
	seedPublishedSnapshot(t, store, oldSnapshot)
	_ = store.Close()

	service, err := mirror.NewService(
		mirror.WithStorageDirs(appCfg.DBDir(), appCfg.PackageDir()),
		mirror.WithDownloader(newAppFakeDownloader(repo.files)),
		mirror.WithDiskSpaceChecker(appDiskSpaceChecker{available: 1024}),
	)
	if err != nil {
		t.Fatalf("NewService returned error: %v", err)
	}
	_, err = captureStdout(func() error {
		return runPublishUpdateWithLogger("Update", service, appCfg, repo.config, logging.Nop())
	})
	if err == nil {
		t.Fatal("expected signing failure")
	}
	store, err = state.Open(appCfg.DBPath(repo.config.Name))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer func() {
		_ = store.Close()
	}()
	published, err := store.Published()
	if err != nil {
		t.Fatalf("Published returned error: %v", err)
	}
	if published.SnapshotName != oldSnapshot {
		t.Fatalf("published state changed despite signing failure: %#v", published)
	}
}

func TestRollbackWorkflowDoesNotCommitPublishedStateWhenPublishFails(t *testing.T) {
	home := t.TempDir()
	appCfg := setAppTestHome(t, home)
	store := openAppTestStore(t, home, "ubuntu")
	defer func() {
		_ = store.Close()
	}()
	cfg, err := store.MirrorConfig()
	if err != nil {
		t.Fatalf("MirrorConfig returned error: %v", err)
	}
	cfg.Signing.Disabled = true
	if err := store.SaveMirrorConfig(cfg); err != nil {
		t.Fatalf("SaveMirrorConfig returned error: %v", err)
	}
	oldSnapshot := "ubuntu-focal-main_2026-05-27"
	newSnapshot := "ubuntu-focal-main_2026-05-28"
	seedPublishedSnapshot(t, store, oldSnapshot)
	if err := store.CreateSnapshot(state.SnapshotRecord{Name: newSnapshot, Kind: "regular", CreatedAt: testCleanupTime(newSnapshot)}, nil); err != nil {
		t.Fatalf("CreateSnapshot returned error: %v", err)
	}
	restore := replaceWorkflowPublisher(func(_ appconfig.Config) (workflowPublisher, error) {
		return failingWorkflowPublisher{err: fmt.Errorf("publish failed")}, nil
	})
	defer restore()

	_, err = captureStdout(func() error {
		return runMirrorCommandWithConfigAndLogger(cli.Command{Name: "rollback", NameRef: "ubuntu", ID: newSnapshot}, appCfg, logging.Nop())
	})
	if err == nil || !strings.Contains(err.Error(), "publish failed") {
		t.Fatalf("expected publish failure, got %v", err)
	}
	published, err := store.Published()
	if err != nil {
		t.Fatalf("Published returned error: %v", err)
	}
	if published.SnapshotName != oldSnapshot {
		t.Fatalf("published state changed despite rollback publish failure: %#v", published)
	}
}

func TestHideDoesNotMarkPublishedStateHiddenWhenRemovalFails(t *testing.T) {
	home := t.TempDir()
	appCfg := setAppTestHome(t, home)
	store := openAppTestStore(t, home, "ubuntu")
	defer func() {
		_ = store.Close()
	}()
	cfg, err := store.MirrorConfig()
	if err != nil {
		t.Fatalf("MirrorConfig returned error: %v", err)
	}
	cfg.Path = string(filepath.Separator)
	cfg.Signing.Disabled = true
	if err := store.SaveMirrorConfig(cfg); err != nil {
		t.Fatalf("SaveMirrorConfig returned error: %v", err)
	}
	seedPublishedSnapshot(t, store, "ubuntu-focal-main_2026-05-27")

	_, err = captureStdout(func() error {
		return runMirrorCommandWithConfigAndLogger(cli.Command{Name: "hide", NameRef: "ubuntu"}, appCfg, logging.Nop())
	})
	if err == nil {
		t.Fatal("expected hide removal failure")
	}
	published, err := store.Published()
	if err != nil {
		t.Fatalf("Published returned error: %v", err)
	}
	if published.Hidden {
		t.Fatalf("published state was marked hidden despite removal failure: %#v", published)
	}
}

// TestPeriodicBatchContinuesAfterBrokenMirrorDB covers defensive DB enumeration:
// one corrupt mirror DB must be reported as a per-mirror failure without
// preventing healthy mirrors from being processed.
func TestPeriodicBatchContinuesAfterBrokenMirrorDB(t *testing.T) {
	home := t.TempDir()
	appCfg := setAppTestHome(t, home)

	// Create a healthy mirror that is due for a daily run (published 2+ days ago).
	dueDate := time.Now().Local().AddDate(0, 0, -2).Format("2006-01-02")
	createPeriodicMirror(t, home, "ubuntu-valid", "daily", dueDate, false)

	// Write garbage bytes to a DB file named so it sorts alphabetically before
	// ubuntu-valid. The batch must report this DB as one failed mirror and keep
	// processing later DB files.
	brokenDBPath := filepath.Join(appCfg.DBDir(), "aaa-broken.sqlite")
	if err := os.MkdirAll(filepath.Dir(brokenDBPath), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(brokenDBPath, []byte("not a sqlite database"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	var workflowCalledFor []string
	restore := replaceUpdateWorkflow(func(_ string, _ *mirror.Service, _ appconfig.Config, cfg config.Mirror, _ logging.Logger) error {
		workflowCalledFor = append(workflowCalledFor, cfg.Name)
		return nil
	})
	defer restore()

	_, err := captureStdout(func() error {
		return runPeriodicBatchCommandWithLogger("daily", appCfg, logging.Nop())
	})

	// The batch must continue past the broken DB and still attempt ubuntu-valid.
	if !reflect.DeepEqual(workflowCalledFor, []string{"ubuntu-valid"}) {
		t.Errorf("expected batch to process ubuntu-valid after broken DB; workflows called for: %v", workflowCalledFor)
	}
	// The per-mirror failure for aaa-broken must be reflected in the aggregate error.
	if err == nil || !strings.Contains(err.Error(), "1 mirror failure") {
		t.Errorf("expected aggregate failure count error, got: %v", err)
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

func TestRunListReportsCompactMirrorNameAndFullSize(t *testing.T) {
	home := t.TempDir()
	setAppTestHome(t, home)
	store := openAppTestStore(t, home, "ubuntu")
	defer func() {
		_ = store.Close()
	}()
	key := upsertAppTestPackage(t, store, "apt", "pool/main/a/apt/apt.deb")
	knownOnlyKey := upsertAppTestPackage(t, store, "known-only", "pool/main/k/known/known.deb")
	pkg, err := store.Package(key)
	if err != nil {
		t.Fatalf("Package returned error: %v", err)
	}
	pkg.Size = 1536
	if _, err := store.UpsertPackage(pkg); err != nil {
		t.Fatalf("UpsertPackage returned error: %v", err)
	}
	knownOnly, err := store.Package(knownOnlyKey)
	if err != nil {
		t.Fatalf("Package known-only returned error: %v", err)
	}
	knownOnly.Size = 4096
	if _, err := store.UpsertPackage(knownOnly); err != nil {
		t.Fatalf("UpsertPackage known-only returned error: %v", err)
	}
	if err := store.ReplaceMirrorPackages([]string{key}); err != nil {
		t.Fatalf("ReplaceMirrorPackages returned error: %v", err)
	}

	output, err := captureStdout(func() error {
		return runList(cli.Command{Name: "list"})
	})
	if err != nil {
		t.Fatalf("runList returned error: %v", err)
	}
	fields := strings.Fields(output)
	if len(fields) != 2 || fields[0] != "ubuntu" {
		t.Fatalf("expected compact list row '<name> <size>', got:\n%s", output)
	}
	if strings.Contains(output, "Mirror:") || strings.Contains(output, "Mirror size:") {
		t.Fatalf("list should not print detailed summary output:\n%s", output)
	}
	if fields[1] == "1.5KB" {
		t.Fatalf("list size should include known package bytes beyond current package membership, got:\n%s", output)
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
	last, err := store.LastUpdate()
	if err != nil {
		t.Fatalf("LastUpdate returned error: %v", err)
	}
	if last.Action != "cleanup" || !strings.Contains(last.Message, "removed") {
		t.Fatalf("unexpected cleanup history: %#v", last)
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

func writeAppMirrorConfig(t *testing.T, name, updatePolicy string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name+".conf")
	content := `[mirror]
name = ` + name + `
url = http://us.archive.ubuntu.com/ubuntu/
dist = focal
release = default
origin = default
label = default
arch = amd64
components = main
path = preprod
sign = no
`
	if updatePolicy != "" {
		content += "update = " + updatePolicy + "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	return path
}

func replaceUpdateWorkflow(fn func(string, *mirror.Service, appconfig.Config, config.Mirror, logging.Logger) error) func() {
	old := runUpdateWorkflow
	runUpdateWorkflow = fn
	return func() {
		runUpdateWorkflow = old
	}
}

func replaceWorkflowPublisher(fn func(appconfig.Config) (workflowPublisher, error)) func() {
	old := newWorkflowPublisher
	newWorkflowPublisher = fn
	return func() {
		newWorkflowPublisher = old
	}
}

type failingWorkflowPublisher struct {
	err error
}

func (p failingWorkflowPublisher) PublishSnapshot(config.Mirror, string) (publish.Result, error) {
	return publish.Result{}, p.err
}

func prepareDBBackedCommandFixture(t *testing.T, home string, store *state.Store, cmd *cli.Command) {
	t.Helper()
	switch cmd.Name {
	case "rollback":
		target := "ubuntu-focal-main_2026-05-28"
		if err := store.CreateSnapshot(state.SnapshotRecord{Name: target, Kind: "regular", CreatedAt: testCleanupTime(target)}, nil); err != nil {
			t.Fatalf("CreateSnapshot returned error: %v", err)
		}
		cmd.ID = target
	case "hide":
		seedPublishedSnapshot(t, store, "ubuntu-focal-main_2026-05-27")
	case "cleanup":
		// Summary mode must run without a published snapshot.
	case "info", "more-info":
		// Read-only commands need only the stored mirror config.
	default:
		t.Fatalf("unexpected command %q", cmd.Name)
	}
}

type appRepoFixture struct {
	config     config.Mirror
	files      map[string][]byte
	packageURL string
}

func newAppRepoFixture(baseURL, version, payload string) appRepoFixture {
	filename := fmt.Sprintf("pool/main/d/demo/demo_%s_amd64.deb", version)
	packageURL := strings.TrimRight(baseURL, "/") + "/" + filename
	payloadBytes := []byte(payload)
	payloadSums := appChecksumBytes(payloadBytes)
	packages := []byte(fmt.Sprintf(`Package: demo
Version: %s
Architecture: amd64
Filename: %s
Size: %d
MD5sum: %s
SHA1: %s
SHA256: %s
SHA512: %s

`, version, filename, len(payloadBytes), payloadSums.md5, payloadSums.sha1, payloadSums.sha256, payloadSums.sha512))
	packagesSums := appChecksumBytes(packages)
	release := []byte(fmt.Sprintf(`Origin: Test
Label: Test
Suite: focal
Codename: focal
Architectures: amd64
Components: main
SHA256:
 %s %d main/binary-amd64/Packages

`, packagesSums.sha256, len(packages)))
	files := map[string][]byte{
		strings.TrimRight(baseURL, "/") + "/dists/focal/Release":                    release,
		strings.TrimRight(baseURL, "/") + "/dists/focal/main/binary-amd64/Packages": packages,
		packageURL: []byte(payload),
	}
	return appRepoFixture{
		config: config.Mirror{
			Name:       "ubuntu",
			URL:        strings.TrimRight(baseURL, "/"),
			Dists:      []string{"focal"},
			Releases:   []string{"default"},
			Origin:     "Test",
			Label:      "Test",
			Arch:       []string{"amd64"},
			Components: []string{"main"},
			Path:       "preprod",
			Signing: config.Signing{
				Disabled: true,
			},
		},
		files:      files,
		packageURL: packageURL,
	}
}

type appChecksums struct {
	md5    string
	sha1   string
	sha256 string
	sha512 string
}

func appChecksumBytes(data []byte) appChecksums {
	md5Sum := md5.Sum(data)
	sha1Sum := sha1.Sum(data)
	sha256Sum := sha256.Sum256(data)
	sha512Sum := sha512.Sum512(data)
	return appChecksums{
		md5:    fmt.Sprintf("%x", md5Sum),
		sha1:   fmt.Sprintf("%x", sha1Sum),
		sha256: fmt.Sprintf("%x", sha256Sum),
		sha512: fmt.Sprintf("%x", sha512Sum),
	}
}

type appFakeDownloader struct {
	files map[string][]byte
}

func newAppFakeDownloader(files map[string][]byte) *appFakeDownloader {
	copied := map[string][]byte{}
	for key, value := range files {
		copied[key] = append([]byte(nil), value...)
	}
	return &appFakeDownloader{files: copied}
}

func (d *appFakeDownloader) FetchMetadata(_ context.Context, rawURL string, _ *download.Checksum) ([]byte, error) {
	data, ok := d.files[rawURL]
	if !ok {
		return nil, &download.HTTPError{URL: rawURL, StatusCode: 404, Status: "404 Not Found"}
	}
	return append([]byte(nil), data...), nil
}

func (d *appFakeDownloader) DownloadPackage(_ context.Context, rawURL, destination string, _ *download.Checksum) error {
	data, ok := d.files[rawURL]
	if !ok {
		return &download.HTTPError{URL: rawURL, StatusCode: 404, Status: "404 Not Found"}
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return err
	}
	return os.WriteFile(destination, data, 0644)
}

func (d *appFakeDownloader) GetLength(_ context.Context, rawURL string) (int64, error) {
	data, ok := d.files[rawURL]
	if !ok {
		return 0, &download.HTTPError{URL: rawURL, StatusCode: 404, Status: "404 Not Found"}
	}
	return int64(len(data)), nil
}

type appDiskSpaceChecker struct {
	available int64
	err       error
}

func (c appDiskSpaceChecker) AvailableBytes(string) (int64, error) {
	return c.available, c.err
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

func createPeriodicMirror(t *testing.T, home, name, policy, publishedDate string, hidden bool) {
	t.Helper()
	configPath := writeAppMirrorConfig(t, name, policy)
	store := openAppTestStore(t, home, name)
	cfg, err := store.MirrorConfig()
	if err != nil {
		t.Fatalf("MirrorConfig returned error: %v", err)
	}
	cfg.ConfigPath = configPath
	cfg.UpdatePolicy = policy
	if err := store.SaveMirrorConfig(cfg); err != nil {
		t.Fatalf("SaveMirrorConfig returned error: %v", err)
	}
	if publishedDate != "" {
		snapshotName := name + "-focal-main_" + publishedDate
		createdAt := testCleanupTime(snapshotName)
		if err := store.CreateSnapshot(state.SnapshotRecord{Name: snapshotName, Kind: "regular", CreatedAt: createdAt}, nil); err != nil {
			t.Fatalf("CreateSnapshot returned error: %v", err)
		}
		if err := store.SetPublished(state.PublishedRecord{
			SnapshotName: snapshotName,
			Path:         "preprod",
			Suite:        "focal",
			Component:    "main",
			PublishedAt:  createdAt,
			Hidden:       hidden,
		}); err != nil {
			t.Fatalf("SetPublished returned error: %v", err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
}

func setAppTestUpdatePolicy(t *testing.T, store *state.Store, policy string) {
	t.Helper()
	cfg, err := store.MirrorConfig()
	if err != nil {
		t.Fatalf("MirrorConfig returned error: %v", err)
	}
	cfg.UpdatePolicy = policy
	if err := store.SaveMirrorConfig(cfg); err != nil {
		t.Fatalf("SaveMirrorConfig returned error: %v", err)
	}
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
