package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	path := writeTempConfig(t, `[mirror]
name = ubuntu-xenial
url = http://archive.ubuntu.com/ubuntu/
dist = xenial, bionic
release = default, updates
origin = default
label = default
arch = amd64, arm64
components = main, restricted
path = ubuntu
merge = 3
server = http://mirror.example.test/
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Name != "ubuntu-xenial" {
		t.Fatalf("unexpected name: %q", cfg.Name)
	}
	if len(cfg.Dists) != 2 || cfg.Dists[0] != "xenial" || cfg.Dists[1] != "bionic" {
		t.Fatalf("unexpected dists: %#v", cfg.Dists)
	}
	if !cfg.Merge.Enabled || cfg.Merge.Depth != 3 {
		t.Fatalf("unexpected merge: %#v", cfg.Merge)
	}
}

func TestValidateRejectsMissingURL(t *testing.T) {
	cfg := Mirror{
		Name:       "ubuntu-xenial",
		Dists:      []string{"xenial"},
		Releases:   []string{"default"},
		Arch:       []string{"amd64"},
		Components: []string{"main"},
		Path:       "ubuntu",
	}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected missing URL validation error")
	}
}

func TestDBPath(t *testing.T) {
	got := DBPath("ubuntu-xenial")
	want := filepath.Join("~", ".mirrors", "db", "ubuntu-xenial.sqlite")
	if got != want {
		t.Fatalf("unexpected DB path: got %q want %q", got, want)
	}
}

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mirror.conf")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	return path
}
