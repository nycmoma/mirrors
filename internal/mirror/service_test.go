package mirror

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mirrors/internal/config"
	"mirrors/internal/download"
)

func TestPackagesIndexURL(t *testing.T) {
	got, err := PackagesIndexURL("http://repo.test/ubuntu/", "focal-updates", "main", "amd64", "xz")
	if err != nil {
		t.Fatalf("PackagesIndexURL returned error: %v", err)
	}
	want := "http://repo.test/ubuntu/dists/focal-updates/main/binary-amd64/Packages.xz"
	if got != want {
		t.Fatalf("PackagesIndexURL returned %q, want %q", got, want)
	}
}

func TestFetchDownloadsPackagesAndIsIdempotent(t *testing.T) {
	home := t.TempDir()
	repo := newRepoFixture("http://repo.test/ubuntu", "1.0", "deb-v1")
	downloader := newFakeDownloader(repo.files)
	service := newTestService(t, home, downloader, WithDiskSpaceChecker(&fakeDiskSpaceChecker{available: 1024}))

	result, err := service.Fetch(context.Background(), repo.config)
	if err != nil {
		t.Fatalf("Fetch returned error: %v", err)
	}
	if result.IndexCount != 1 || result.PackageCount != 1 || result.DownloadedCount != 1 || result.ReusedCount != 0 {
		t.Fatalf("unexpected first fetch result: %#v", result)
	}
	if result.Plan.IndexesConsidered != 1 || result.Plan.PackagesToDownload != 1 || result.Plan.EstimatedDownloadBytes != int64(len("deb-v1")) {
		t.Fatalf("unexpected first fetch plan: %#v", result.Plan)
	}
	if result.AddedPackageCount != 1 || result.RemovedPackageCount != 0 || result.Unchanged {
		t.Fatalf("unexpected first fetch diff: %#v", result)
	}

	files, err := os.ReadDir(config.PackageDirForHome(home))
	if err != nil {
		t.Fatalf("read package pool: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("expected package pool files")
	}

	result, err = service.Fetch(context.Background(), repo.config)
	if err != nil {
		t.Fatalf("second Fetch returned error: %v", err)
	}
	if result.DownloadedCount != 0 || result.ReusedCount != 1 || !result.Unchanged {
		t.Fatalf("second fetch should reuse existing package: %#v", result)
	}
	if result.Plan.PackagesToDownload != 0 || result.Plan.EstimatedDownloadBytes != 0 || result.Plan.PackagesReused != 1 {
		t.Fatalf("second fetch should plan zero download bytes: %#v", result.Plan)
	}
	if downloader.downloads[repo.packageURL] != 1 {
		t.Fatalf("expected package file to be downloaded once, got %d", downloader.downloads[repo.packageURL])
	}

	summary, err := service.Info(repo.config.Name)
	if err != nil {
		t.Fatalf("Info returned error: %v", err)
	}
	if summary.Stats.MirrorPackageCount != 1 || summary.Stats.KnownPackageCount != 1 || summary.Stats.LastUpdate == nil {
		t.Fatalf("unexpected summary stats: %#v", summary.Stats)
	}

	summaries, err := service.List()
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(summaries) != 1 || summaries[0].Config.Name != repo.config.Name {
		t.Fatalf("unexpected list summaries: %#v", summaries)
	}
}

func TestFetchFailsBeforeDownloadWhenDiskSpaceIsInsufficient(t *testing.T) {
	home := t.TempDir()
	repo := newRepoFixture("http://repo.test/ubuntu", "1.0", "deb-v1")
	downloader := newFakeDownloader(repo.files)
	service := newTestService(t, home, downloader, WithDiskSpaceChecker(&fakeDiskSpaceChecker{available: 1}))

	_, err := service.Fetch(context.Background(), repo.config)
	if err == nil {
		t.Fatal("expected insufficient disk space error")
	}
	if !strings.Contains(err.Error(), "not enough disk space") {
		t.Fatalf("unexpected error: %v", err)
	}
	if downloader.downloads[repo.packageURL] != 0 {
		t.Fatalf("package download started despite insufficient disk space: %d", downloader.downloads[repo.packageURL])
	}
}

func TestFetchAllowsUnknownPackageSizeWithPlanWarning(t *testing.T) {
	home := t.TempDir()
	repo := newRepoFixtureWithoutSize("http://repo.test/ubuntu", "1.0", "deb-v1")
	downloader := newFakeDownloader(repo.files)
	service := newTestService(t, home, downloader, WithDiskSpaceChecker(&fakeDiskSpaceChecker{available: 0}))

	result, err := service.Fetch(context.Background(), repo.config)
	if err != nil {
		t.Fatalf("Fetch returned error: %v", err)
	}
	if result.Plan.UnknownSizePackages != 1 || result.Plan.EstimatedDownloadBytes != 0 || result.Plan.PackagesToDownload != 1 {
		t.Fatalf("unexpected unknown-size plan: %#v", result.Plan)
	}
	if len(result.Plan.Warnings) == 0 || !strings.Contains(result.Plan.Warnings[0], "unknown size metadata") {
		t.Fatalf("expected unknown-size warning, got %#v", result.Plan.Warnings)
	}
	if result.DownloadedCount != 1 {
		t.Fatalf("expected unknown-size package to download, got result %#v", result)
	}
}

func TestFetchDetectsChangedPackageVersion(t *testing.T) {
	home := t.TempDir()
	repo := newRepoFixture("http://repo.test/ubuntu", "1.0", "deb-v1")
	downloader := newFakeDownloader(repo.files)
	service := newTestService(t, home, downloader)

	if _, err := service.Fetch(context.Background(), repo.config); err != nil {
		t.Fatalf("Fetch returned error: %v", err)
	}

	repoV2 := newRepoFixture("http://repo.test/ubuntu", "2.0", "deb-v2")
	downloader.files = repoV2.files
	result, err := service.Fetch(context.Background(), repoV2.config)
	if err != nil {
		t.Fatalf("second Fetch returned error: %v", err)
	}
	if result.AddedPackageCount != 1 || result.RemovedPackageCount != 1 || result.Unchanged {
		t.Fatalf("expected changed package version diff, got %#v", result)
	}
}

func TestFetchReportsMissingArchitectureOrComponent(t *testing.T) {
	home := t.TempDir()
	repo := newRepoFixture("http://repo.test/ubuntu", "1.0", "deb-v1")
	cfg := repo.config
	cfg.Arch = []string{"arm64"}

	service := newTestService(t, home, newFakeDownloader(repo.files))
	_, err := service.Fetch(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected missing architecture error")
	}
	if !strings.Contains(err.Error(), `does not contain architecture "arm64"`) {
		t.Fatalf("unexpected error: %v", err)
	}

	cfg = repo.config
	cfg.Components = []string{"universe"}
	_, err = service.Fetch(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected missing component error")
	}
	if !strings.Contains(err.Error(), `does not contain component "universe"`) {
		t.Fatalf("unexpected component error: %v", err)
	}
}

func TestDestroyRemovesMirrorState(t *testing.T) {
	home := t.TempDir()
	repo := newRepoFixture("http://repo.test/ubuntu", "1.0", "deb-v1")
	service := newTestService(t, home, newFakeDownloader(repo.files))

	if _, err := service.Create(context.Background(), repo.config); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if err := service.Destroy(repo.config.Name); err != nil {
		t.Fatalf("Destroy returned error: %v", err)
	}
	if _, err := os.Stat(config.DBPathForHome(home, repo.config.Name)); !os.IsNotExist(err) {
		t.Fatalf("expected DB to be removed, stat error: %v", err)
	}
}

func newTestService(t *testing.T, home string, downloader download.Downloader, options ...Option) *Service {
	t.Helper()
	serviceOptions := []Option{WithHome(home), WithDownloader(downloader)}
	serviceOptions = append(serviceOptions, options...)
	service, err := NewService(serviceOptions...)
	if err != nil {
		t.Fatalf("NewService returned error: %v", err)
	}
	return service
}

type repoFixture struct {
	config     config.Mirror
	files      map[string][]byte
	packageURL string
}

func newRepoFixture(baseURL, version, packagePayload string) repoFixture {
	return newRepoFixtureWithSize(baseURL, version, packagePayload, true)
}

func newRepoFixtureWithoutSize(baseURL, version, packagePayload string) repoFixture {
	return newRepoFixtureWithSize(baseURL, version, packagePayload, false)
}

func newRepoFixtureWithSize(baseURL, version, packagePayload string, includeSize bool) repoFixture {
	checksums := checksumBytes([]byte(packagePayload))
	filename := fmt.Sprintf("pool/main/d/demo/demo_%s_amd64.deb", version)
	sizeLine := ""
	if includeSize {
		sizeLine = fmt.Sprintf("Size: %d\n", len(packagePayload))
	}
	packages := fmt.Sprintf(`Package: demo
Version: %s
Architecture: amd64
Filename: %s
%sMD5sum: %s
SHA1: %s
SHA256: %s
SHA512: %s

`, version, filename, sizeLine, checksums.MD5, checksums.SHA1, checksums.SHA256, checksums.SHA512)
	packagesChecksum := checksumBytes([]byte(packages))

	release := fmt.Sprintf(`Origin: Test
Label: Test
Suite: focal
Codename: focal
Architectures: amd64
Components: main
SHA256:
 %s %d main/binary-amd64/Packages

`, packagesChecksum.SHA256, len(packages))

	packageURL, _ := PackageURL(baseURL, filename)
	return repoFixture{
		config: config.Mirror{
			Name:       "ubuntu",
			URL:        baseURL,
			Dists:      []string{"focal"},
			Releases:   []string{"default"},
			Origin:     "Test",
			Label:      "Test",
			Arch:       []string{"amd64"},
			Components: []string{"main"},
			Path:       "ubuntu",
			Signing: config.Signing{
				GPGKey:        "560CE107BECFB86BF8BED1DBD9FEEBA651DA48E7",
				GPGPassphrase: "1234",
			},
		},
		files: map[string][]byte{
			baseURL + "/dists/focal/Release":                    []byte(release),
			baseURL + "/dists/focal/main/binary-amd64/Packages": []byte(packages),
			packageURL: []byte(packagePayload),
		},
		packageURL: packageURL,
	}
}

type fakeDiskSpaceChecker struct {
	available int64
	err       error
}

func (c *fakeDiskSpaceChecker) AvailableBytes(_ string) (int64, error) {
	return c.available, c.err
}

type fakeDownloader struct {
	files     map[string][]byte
	downloads map[string]int
}

func newFakeDownloader(files map[string][]byte) *fakeDownloader {
	return &fakeDownloader{
		files:     files,
		downloads: map[string]int{},
	}
}

func (d *fakeDownloader) FetchMetadata(_ context.Context, rawURL string, _ *download.Checksum) ([]byte, error) {
	data, ok := d.files[rawURL]
	if !ok {
		return nil, &download.HTTPError{URL: rawURL, StatusCode: 404, Status: "404 Not Found"}
	}
	return append([]byte(nil), data...), nil
}

func (d *fakeDownloader) DownloadPackage(_ context.Context, rawURL, destination string, _ *download.Checksum) error {
	data, ok := d.files[rawURL]
	if !ok {
		return &download.HTTPError{URL: rawURL, StatusCode: 404, Status: "404 Not Found"}
	}
	d.downloads[rawURL]++
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return err
	}
	return os.WriteFile(destination, data, 0644)
}

func (d *fakeDownloader) GetLength(_ context.Context, rawURL string) (int64, error) {
	data, ok := d.files[rawURL]
	if !ok {
		return -1, &download.HTTPError{URL: rawURL, StatusCode: 404, Status: "404 Not Found"}
	}
	return int64(len(data)), nil
}

type testChecksums struct {
	MD5    string
	SHA1   string
	SHA256 string
	SHA512 string
}

func checksumBytes(data []byte) testChecksums {
	return testChecksums{
		MD5:    fmt.Sprintf("%x", md5.Sum(data)),
		SHA1:   fmt.Sprintf("%x", sha1.Sum(data)),
		SHA256: fmt.Sprintf("%x", sha256.Sum256(data)),
		SHA512: fmt.Sprintf("%x", sha512.Sum512(data)),
	}
}
