package appconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadUsesDefaultsWhenConfigIsMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "missing"))

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.DataRoot != filepath.Join(home, ".mirrors", ".data") {
		t.Fatalf("unexpected data root: %q", cfg.DataRoot)
	}
	if cfg.MirrorsRoot != filepath.Join(home, ".mirrors", "mirrors") {
		t.Fatalf("unexpected mirrors root: %q", cfg.MirrorsRoot)
	}
	if cfg.LogsRoot != filepath.Join(home, ".mirrors", ".logs") {
		t.Fatalf("unexpected logs root: %q", cfg.LogsRoot)
	}
	if cfg.HTTPTimeout != 30*time.Second {
		t.Fatalf("unexpected timeout: %v", cfg.HTTPTimeout)
	}
	if cfg.HTTPRetries != 3 {
		t.Fatalf("unexpected retries: %d", cfg.HTTPRetries)
	}
	if cfg.HTTPRetryDelay != time.Second {
		t.Fatalf("unexpected retry delay: %v", cfg.HTTPRetryDelay)
	}
	if cfg.DownloadThreads != 1 {
		t.Fatalf("unexpected download threads: %d", cfg.DownloadThreads)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "mirrors.conf")); err != nil {
		t.Fatalf("expected default config to be created: %v", err)
	}
	for _, path := range []string{cfg.DataRoot, cfg.MirrorsRoot, cfg.LogsRoot} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected default path %q to be usable: %v", path, err)
		}
	}
}

func TestLoadContinuesWhenDefaultConfigCannotBeCreatedButDefaultsWork(t *testing.T) {
	home := t.TempDir()
	xdgFile := filepath.Join(t.TempDir(), "xdg-file")
	if err := os.WriteFile(xdgFile, []byte("not a directory"), 0644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdgFile)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	for _, path := range []string{cfg.DataRoot, cfg.MirrorsRoot, cfg.LogsRoot} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected default path %q to be usable: %v", path, err)
		}
	}
}

func TestLoadFailsWhenDefaultConfigIsCreatedAndDefaultsFail(t *testing.T) {
	home := t.TempDir()
	xdg := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".mirrors"), []byte("not a directory"), 0644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)

	_, err := Load()
	if err == nil {
		t.Fatal("expected Load to fail")
	}
	if !strings.Contains(err.Error(), "data_root") || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(xdg, "mirrors.conf")); statErr != nil {
		t.Fatalf("expected default config to be created before validation failed: %v", statErr)
	}
}

func TestLoadFailsWhenDefaultConfigCannotBeCreatedAndDefaultsFail(t *testing.T) {
	home := t.TempDir()
	xdgFile := filepath.Join(t.TempDir(), "xdg-file")
	if err := os.WriteFile(xdgFile, []byte("not a directory"), 0644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".mirrors"), []byte("not a directory"), 0644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdgFile)

	_, err := Load()
	if err == nil {
		t.Fatal("expected Load to fail")
	}
	if !strings.Contains(err.Error(), "default paths are not usable") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadFromXDGConfigHome(t *testing.T) {
	home := t.TempDir()
	xdg := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	writeConfig(t, filepath.Join(xdg, "mirrors.conf"), `[app]
data_root = ~/mirror-data
mirrors_root = ~/published
logs_root = ~/mirror-logs
http_timeout = 45s
http_retries = 5
http_retry_delay = 250ms
download_threads = 4
`)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.DataRoot != filepath.Join(home, "mirror-data") {
		t.Fatalf("unexpected data root: %q", cfg.DataRoot)
	}
	if cfg.MirrorsRoot != filepath.Join(home, "published") {
		t.Fatalf("unexpected mirrors root: %q", cfg.MirrorsRoot)
	}
	if cfg.LogsRoot != filepath.Join(home, "mirror-logs") {
		t.Fatalf("unexpected logs root: %q", cfg.LogsRoot)
	}
	if cfg.DBPath("ubuntu") != filepath.Join(home, "mirror-data", "db", "ubuntu.sqlite") {
		t.Fatalf("unexpected DB path: %q", cfg.DBPath("ubuntu"))
	}
	if cfg.PackageDir() != filepath.Join(home, "mirror-data", "packages") {
		t.Fatalf("unexpected package dir: %q", cfg.PackageDir())
	}
	if cfg.HTTPTimeout != 45*time.Second {
		t.Fatalf("unexpected timeout: %v", cfg.HTTPTimeout)
	}
	if cfg.HTTPRetries != 5 {
		t.Fatalf("unexpected retries: %d", cfg.HTTPRetries)
	}
	if cfg.HTTPRetryDelay != 250*time.Millisecond {
		t.Fatalf("unexpected retry delay: %v", cfg.HTTPRetryDelay)
	}
	if cfg.DownloadThreads != 4 {
		t.Fatalf("unexpected download threads: %d", cfg.DownloadThreads)
	}
}

func TestLoadFromHomeConfigFallback(t *testing.T) {
	home := t.TempDir()
	dataRoot := filepath.Join(t.TempDir(), "data")
	mirrorsRoot := filepath.Join(t.TempDir(), "mirrors")
	logsRoot := filepath.Join(t.TempDir(), "logs")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	path := filepath.Join(home, ".config", "mirrors.conf")
	writeConfig(t, path, "data_root = "+dataRoot+"\nmirrors_root = "+mirrorsRoot+"\nlogs_root = "+logsRoot)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.DataRoot != dataRoot {
		t.Fatalf("unexpected data root: %q", cfg.DataRoot)
	}
	if cfg.MirrorsRoot != mirrorsRoot {
		t.Fatalf("unexpected mirrors root: %q", cfg.MirrorsRoot)
	}
	if cfg.LogsRoot != logsRoot {
		t.Fatalf("unexpected logs root: %q", cfg.LogsRoot)
	}
	if cfg.Path != path {
		t.Fatalf("unexpected path: %q", cfg.Path)
	}
}

func TestLoadRejectsInvalidRootPaths(t *testing.T) {
	home := t.TempDir()
	xdg := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	dataRootFile := filepath.Join(t.TempDir(), "data-root-file")
	if err := os.WriteFile(dataRootFile, []byte("not a directory"), 0644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	writeConfig(t, filepath.Join(xdg, "mirrors.conf"), "data_root = "+dataRootFile)

	_, err := Load()
	if err == nil {
		t.Fatal("expected Load to fail")
	}
	if !strings.Contains(err.Error(), "data_root") || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{name: "timeout", content: "http_timeout = soon"},
		{name: "retries", content: "http_retries = -1"},
		{name: "retry delay", content: "http_retry_delay = -1s"},
		{name: "threads", content: "download_threads = 0"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			xdg := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", xdg)
			writeConfig(t, filepath.Join(xdg, "mirrors.conf"), test.content)

			if _, err := Load(); err == nil {
				t.Fatal("expected Load to fail")
			}
		})
	}
}

func writeConfig(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
}
