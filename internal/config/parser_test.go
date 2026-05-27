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

func TestLoadDefaultsReleaseOriginAndLabel(t *testing.T) {
	path := writeTempConfig(t, `[mirror]
name = ubuntu-xenial
url = http://archive.ubuntu.com/ubuntu/
dist = xenial
arch = amd64
components = main
path = ubuntu
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(cfg.Releases) != 1 || cfg.Releases[0] != "default" {
		t.Fatalf("unexpected releases: %#v", cfg.Releases)
	}
	if cfg.Origin != "default" {
		t.Fatalf("unexpected origin: %q", cfg.Origin)
	}
	if cfg.Label != "default" {
		t.Fatalf("unexpected label: %q", cfg.Label)
	}
}

func TestLoadTrimsListFields(t *testing.T) {
	path := writeTempConfig(t, `[mirror]
name = ubuntu-xenial
url = http://archive.ubuntu.com/ubuntu/
dist = xenial, bionic , focal
release = default, updates
arch = amd64, arm64
components = main, restricted
path = ubuntu
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	assertStringSlice(t, cfg.Dists, []string{"xenial", "bionic", "focal"})
	assertStringSlice(t, cfg.Releases, []string{"default", "updates"})
	assertStringSlice(t, cfg.Arch, []string{"amd64", "arm64"})
	assertStringSlice(t, cfg.Components, []string{"main", "restricted"})
}

func TestLoadMergeValues(t *testing.T) {
	tests := []struct {
		value   string
		enabled bool
		depth   int
	}{
		{value: "no", enabled: false},
		{value: "0", enabled: false},
		{value: "yes", enabled: true},
		{value: "3", enabled: true, depth: 3},
	}

	for _, tt := range tests {
		path := writeTempConfig(t, `[mirror]
name = ubuntu-xenial
url = http://archive.ubuntu.com/ubuntu/
dist = xenial
release = default
arch = amd64
components = main
path = ubuntu
merge = `+tt.value+`
`)

		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("Load(%q) returned error: %v", tt.value, err)
		}
		if cfg.Merge.Enabled != tt.enabled || cfg.Merge.Depth != tt.depth {
			t.Fatalf("merge %q = %#v, want enabled=%v depth=%d", tt.value, cfg.Merge, tt.enabled, tt.depth)
		}
	}
}

func TestLoadRejectsInvalidMerge(t *testing.T) {
	tests := []string{"sometimes", "true", "false"}
	for _, value := range tests {
		path := writeTempConfig(t, `[mirror]
name = ubuntu-xenial
url = http://archive.ubuntu.com/ubuntu/
dist = xenial
release = default
arch = amd64
components = main
path = ubuntu
merge = `+value+`
`)
		if _, err := Load(path); err == nil {
			t.Fatalf("expected invalid merge error for %q", value)
		}
	}
}

func TestLoadUbuntuStyleConfig(t *testing.T) {
	path := writeTempConfig(t, `[mirror]
name = ubuntu
aptly_cfg = aptly_cfg/prod_mirrors.conf
url = http://us.archive.ubuntu.com/ubuntu/
dist = trusty, xenial, bionic, focal, jammy
release = default, security, updates
label = aptly_default
origin = aptly_default
arch = amd64
components = main, restricted, multiverse, universe
update = weekly
path = preprod
server = http://dlt-ubmirror.datto.com
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Name != "ubuntu" {
		t.Fatalf("unexpected name: %q", cfg.Name)
	}
	assertStringSlice(t, cfg.Dists, []string{"trusty", "xenial", "bionic", "focal", "jammy"})
	assertStringSlice(t, cfg.Releases, []string{"default", "security", "updates"})
	assertStringSlice(t, cfg.Components, []string{"main", "restricted", "multiverse", "universe"})
	if cfg.Merge.Enabled {
		t.Fatalf("expected merge disabled by default, got %#v", cfg.Merge)
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

func TestValidateRejectsInvalidURL(t *testing.T) {
	cfg := Mirror{
		Name:       "ubuntu-xenial",
		URL:        "ftp://archive.ubuntu.com/ubuntu/",
		Dists:      []string{"xenial"},
		Releases:   []string{"default"},
		Arch:       []string{"amd64"},
		Components: []string{"main"},
		Path:       "ubuntu",
	}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected invalid URL validation error")
	}
}

func TestValidateRejectsInvalidName(t *testing.T) {
	cfg := Mirror{
		Name:       "../ubuntu",
		URL:        "http://archive.ubuntu.com/ubuntu/",
		Dists:      []string{"xenial"},
		Releases:   []string{"default"},
		Arch:       []string{"amd64"},
		Components: []string{"main"},
		Path:       "ubuntu",
	}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected invalid name validation error")
	}
}

func TestDBPath(t *testing.T) {
	got := DBPathForHome("/home/tester", "ubuntu-xenial")
	want := filepath.Join("/home/tester", ".mirrors", "db", "ubuntu-xenial.sqlite")
	if got != want {
		t.Fatalf("unexpected DB path: got %q want %q", got, want)
	}
}

func TestPackageDirForHome(t *testing.T) {
	got := PackageDirForHome("/home/tester")
	want := filepath.Join("/home/tester", ".mirrors", "packages")
	if got != want {
		t.Fatalf("unexpected package dir: got %q want %q", got, want)
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

func assertStringSlice(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("slice length mismatch: got %#v want %#v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("slice mismatch: got %#v want %#v", got, want)
		}
	}
}
