package config

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mirrors/internal/download"
)

func TestGenerateRejectsRepositoryRootURL(t *testing.T) {
	downloader := fakeDownloader{files: map[string][]byte{
		"https://repo.example.test/apt/dists/": []byte(`<a href="jammy/">jammy</a><a href="jammy-updates/">jammy-updates</a>`),
	}}
	_, err := GenerateWithDownloader(context.Background(), "https://repo.example.test/apt", downloader)
	if err == nil {
		t.Fatal("expected repository root URL to be rejected")
	}
}

func TestGenerateFromReleaseURL(t *testing.T) {
	downloader := fakeDownloader{files: map[string][]byte{
		"https://archive.example.test/ubuntu/dists/jammy-updates/Release": []byte(`Origin: Test
Label: Test
Suite: jammy-updates
Architectures: amd64
Components: main
`),
	}}
	cfg, err := GenerateWithDownloader(context.Background(), "https://archive.example.test/ubuntu/dists/jammy-updates/Release", downloader)
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if cfg.URL != "https://archive.example.test/ubuntu/" {
		t.Fatalf("unexpected URL: %q", cfg.URL)
	}
	if cfg.Name != "archive.example.test-jammy-updates" {
		t.Fatalf("unexpected name: %q", cfg.Name)
	}
	if strings.Join(cfg.Dists, ",") != "jammy" {
		t.Fatalf("unexpected dists: %#v", cfg.Dists)
	}
	if strings.Join(cfg.Releases, ",") != "updates" {
		t.Fatalf("unexpected releases: %#v", cfg.Releases)
	}
	if cfg.Origin != "Test" || cfg.Label != "Test" {
		t.Fatalf("unexpected origin/label: %q/%q", cfg.Origin, cfg.Label)
	}
	if strings.Join(cfg.Arch, ",") != "amd64" {
		t.Fatalf("unexpected arch: %#v", cfg.Arch)
	}
	if strings.Join(cfg.Components, ",") != "main" {
		t.Fatalf("unexpected components: %#v", cfg.Components)
	}
}

func TestGenerateRejectsInvalidURL(t *testing.T) {
	_, err := GenerateWithDownloader(context.Background(), "file:///tmp/repo", fakeDownloader{})
	if err == nil {
		t.Fatal("expected invalid URL error")
	}
}

func TestGenerateFromInReleaseURL(t *testing.T) {
	payload := `Origin: Test
Label: Test
Suite: bionic
Architectures: amd64 i386
Components: main restricted
`
	inRelease := "-----BEGIN PGP SIGNED MESSAGE-----\nHash: SHA256\n\n" + payload + "-----BEGIN PGP SIGNATURE-----\n\nfake\n-----END PGP SIGNATURE-----\n"
	downloader := fakeDownloader{files: map[string][]byte{
		"https://archive.example.test/ubuntu/dists/bionic/InRelease": []byte(inRelease),
	}}
	cfg, err := GenerateWithDownloader(context.Background(), "https://archive.example.test/ubuntu/dists/bionic/InRelease", downloader)
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if strings.Join(cfg.Dists, ",") != "bionic" || strings.Join(cfg.Releases, ",") != "default" {
		t.Fatalf("unexpected suite split: %#v / %#v", cfg.Dists, cfg.Releases)
	}
	if cfg.Name != "archive.example.test-bionic" {
		t.Fatalf("unexpected name: %q", cfg.Name)
	}
	if strings.Join(cfg.Components, ",") != "main,restricted" {
		t.Fatalf("unexpected components: %#v", cfg.Components)
	}
}

func TestGeneratedNameFromReleaseURLIdentity(t *testing.T) {
	tests := []struct {
		name     string
		baseURL  string
		suite    string
		expected string
	}{
		{
			name:     "launchpad ppa keeps owner product and url suite",
			baseURL:  "http://ppa.launchpad.net/jan-hoffmann/asterisk16/ubuntu/",
			suite:    "devel",
			expected: "ppa.launchpad.net-jan-hoffmann-asterisk16-devel",
		},
		{
			name:     "docker drops linux path segment and keeps distro",
			baseURL:  "https://download.docker.com/linux/ubuntu/",
			suite:    "lunar",
			expected: "download.docker.com-ubuntu-lunar",
		},
		{
			name:     "cloudlinux keeps product version and stable suite",
			baseURL:  "https://repo.cloudlinux.com/kernelcare-debian/9/",
			suite:    "stable",
			expected: "repo.cloudlinux.com-kernelcare-debian-9-stable",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parsed, err := url.Parse(test.baseURL)
			if err != nil {
				t.Fatalf("parse URL: %v", err)
			}
			if got := generatedName(parsed, test.suite); got != test.expected {
				t.Fatalf("generatedName() = %q, want %q", got, test.expected)
			}
		})
	}
}

func TestValidateUpstream(t *testing.T) {
	cfg := Mirror{
		Name:       "repo",
		URL:        "https://repo.example.test/apt/",
		Dists:      []string{"jammy"},
		Releases:   []string{"default"},
		Origin:     "Test",
		Label:      "Test",
		Arch:       []string{"amd64"},
		Components: []string{"main"},
		Path:       "repo",
	}
	downloader := fakeDownloader{files: map[string][]byte{
		"https://repo.example.test/apt/dists/jammy/Release": []byte(`Origin: Test
Label: Test
Suite: jammy
Architectures: amd64
Components: main
`),
	}}
	details, err := ValidateUpstreamDetails(context.Background(), cfg, downloader)
	if err != nil {
		t.Fatalf("ValidateUpstreamDetails returned error: %v", err)
	}
	if len(details) != 1 || details[0].Suite != "jammy" || details[0].Origin != "Test" || details[0].Label != "Test" {
		t.Fatalf("unexpected upstream details: %#v", details)
	}
	if err := ValidateUpstream(context.Background(), cfg, downloader); err != nil {
		t.Fatalf("ValidateUpstream returned error: %v", err)
	}
	cfg.Arch = []string{"arm64"}
	err = ValidateUpstream(context.Background(), cfg, downloader)
	if err == nil || !strings.Contains(err.Error(), `does not contain architecture "arm64"`) {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

type fakeDownloader struct {
	files map[string][]byte
}

func (d fakeDownloader) FetchMetadata(_ context.Context, rawURL string, _ *download.Checksum) ([]byte, error) {
	data, ok := d.files[rawURL]
	if !ok {
		return nil, &download.HTTPError{URL: rawURL, StatusCode: 404, Status: "404 Not Found"}
	}
	return append([]byte(nil), data...), nil
}

func (d fakeDownloader) DownloadPackage(_ context.Context, rawURL, destination string, _ *download.Checksum) error {
	data, ok := d.files[rawURL]
	if !ok {
		return &download.HTTPError{URL: rawURL, StatusCode: 404, Status: "404 Not Found"}
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return err
	}
	return os.WriteFile(destination, data, 0644)
}

func (d fakeDownloader) GetLength(_ context.Context, rawURL string) (int64, error) {
	data, ok := d.files[rawURL]
	if !ok {
		return -1, &download.HTTPError{URL: rawURL, StatusCode: 404, Status: "404 Not Found"}
	}
	return int64(len(data)), nil
}
