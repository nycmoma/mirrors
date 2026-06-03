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
	"sync"
	"testing"
	"time"

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

func TestFetchDownloadsPlannedPackagesWithProgress(t *testing.T) {
	home := t.TempDir()
	repo := newRepoFixtureWithPayloads("http://repo.test/ubuntu", []string{"deb-v1", "deb-v2", "deb-v3"})
	downloader := newFakeDownloader(repo.files)
	progress := &recordingProgressReporter{}
	service := newTestService(t, home, downloader, WithDownloadThreads(2), WithProgressReporter(progress), WithDiskSpaceChecker(&fakeDiskSpaceChecker{available: 1024}))

	result, err := service.Fetch(context.Background(), repo.config)
	if err != nil {
		t.Fatalf("Fetch returned error: %v", err)
	}
	if result.DownloadedCount != 3 || result.ReusedCount != 0 || result.Plan.PackagesToDownload != 3 {
		t.Fatalf("unexpected fetch result: %#v", result)
	}
	if progress.starts != 1 || progress.completes != 3 || progress.errors != 0 || progress.finishes != 1 {
		t.Fatalf("unexpected progress events: %#v", progress.snapshot())
	}
	if progress.bytes != int64(len("deb-v1")+len("deb-v2")+len("deb-v3")) {
		t.Fatalf("unexpected progress bytes: %d", progress.bytes)
	}
}

func TestFetchRespectsDownloadThreadLimit(t *testing.T) {
	home := t.TempDir()
	repo := newRepoFixtureWithPayloads("http://repo.test/ubuntu", []string{"deb-v1", "deb-v2", "deb-v3"})
	downloader := newFakeDownloader(repo.files)
	downloader.started = make(chan string, 3)
	downloader.release = make(chan struct{})
	service := newTestService(t, home, downloader, WithDownloadThreads(2), WithDiskSpaceChecker(&fakeDiskSpaceChecker{available: 1024}))

	done := make(chan error, 1)
	go func() {
		_, err := service.Fetch(context.Background(), repo.config)
		done <- err
	}()

	waitDownloadStart(t, downloader.started)
	waitDownloadStart(t, downloader.started)
	select {
	case started := <-downloader.started:
		t.Fatalf("download %q started before thread slot was released", started)
	case <-time.After(50 * time.Millisecond):
	}
	close(downloader.release)

	if err := <-done; err != nil {
		t.Fatalf("Fetch returned error: %v", err)
	}
	if downloader.maxActiveDownloads() > 2 {
		t.Fatalf("expected at most 2 active downloads, got %d", downloader.maxActiveDownloads())
	}
}

func TestFetchCancelsPendingDownloadsOnFailure(t *testing.T) {
	home := t.TempDir()
	repo := newRepoFixtureWithPayloads("http://repo.test/ubuntu", []string{"deb-v1", "deb-v2", "deb-v3"})
	downloader := newFakeDownloader(repo.files)
	downloader.failURLs = map[string]error{repo.packageURLs[0]: fmt.Errorf("boom")}
	progress := &recordingProgressReporter{}
	service := newTestService(t, home, downloader, WithDownloadThreads(1), WithProgressReporter(progress), WithDiskSpaceChecker(&fakeDiskSpaceChecker{available: 1024}))

	_, err := service.Fetch(context.Background(), repo.config)
	if err == nil {
		t.Fatal("expected download failure")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("unexpected error: %v", err)
	}
	if downloader.downloads[repo.packageURLs[1]] != 0 || downloader.downloads[repo.packageURLs[2]] != 0 {
		t.Fatalf("pending downloads were not canceled: %#v", downloader.downloads)
	}
	if progress.errors != 1 || progress.finishes != 1 {
		t.Fatalf("expected error and finish progress events, got %#v", progress.snapshot())
	}
}

func TestFetchReportsEveryStartedFailure(t *testing.T) {
	home := t.TempDir()
	repo := newRepoFixtureWithPayloads("http://repo.test/ubuntu", []string{"deb-v1", "deb-v2", "deb-v3"})
	downloader := newFakeDownloader(repo.files)
	downloader.started = make(chan string, 3)
	downloader.release = make(chan struct{})
	downloader.failURLs = map[string]error{
		repo.packageURLs[0]: fmt.Errorf("boom 1"),
		repo.packageURLs[1]: fmt.Errorf("boom 2"),
	}
	progress := &recordingProgressReporter{}
	service := newTestService(t, home, downloader, WithDownloadThreads(2), WithProgressReporter(progress), WithDiskSpaceChecker(&fakeDiskSpaceChecker{available: 1024}))

	done := make(chan error, 1)
	go func() {
		_, err := service.Fetch(context.Background(), repo.config)
		done <- err
	}()

	waitDownloadStart(t, downloader.started)
	waitDownloadStart(t, downloader.started)
	close(downloader.release)

	err := <-done
	if err == nil {
		t.Fatal("expected download failure")
	}
	snapshot := progress.snapshot()
	if snapshot.packageStarts != 2 || snapshot.errors != 2 || snapshot.lastFinish.FailedPackages != 2 {
		t.Fatalf("expected both started packages to fail in progress accounting, got %#v", snapshot)
	}
	if downloader.downloads[repo.packageURLs[2]] != 0 {
		t.Fatalf("pending package should not start after failures: %#v", downloader.downloads)
	}
}

func TestFetchDoesNotUpdatePackageStateOnDownloadFailure(t *testing.T) {
	home := t.TempDir()
	repo := newRepoFixture("http://repo.test/ubuntu", "1.0", "deb-v1")
	downloader := newFakeDownloader(repo.files)
	service := newTestService(t, home, downloader, WithDiskSpaceChecker(&fakeDiskSpaceChecker{available: 1024}))

	if _, err := service.Fetch(context.Background(), repo.config); err != nil {
		t.Fatalf("initial Fetch returned error: %v", err)
	}

	repoV2 := newRepoFixtureWithPayloads("http://repo.test/ubuntu", []string{"deb-v2", "deb-v3"})
	downloader.files = repoV2.files
	downloader.failURLs = map[string]error{repoV2.packageURLs[1]: fmt.Errorf("boom")}
	_, err := service.Fetch(context.Background(), repoV2.config)
	if err == nil {
		t.Fatal("expected download failure")
	}

	summary, err := service.Info(repo.config.Name)
	if err != nil {
		t.Fatalf("Info returned error: %v", err)
	}
	if summary.Stats.MirrorPackageCount != 1 {
		t.Fatalf("failed fetch should not replace mirror package membership: %#v", summary.Stats)
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
	config      config.Mirror
	files       map[string][]byte
	packageURL  string
	packageURLs []string
}

func newRepoFixture(baseURL, version, packagePayload string) repoFixture {
	return newRepoFixtureWithSize(baseURL, version, packagePayload, true)
}

func newRepoFixtureWithoutSize(baseURL, version, packagePayload string) repoFixture {
	return newRepoFixtureWithSize(baseURL, version, packagePayload, false)
}

func newRepoFixtureWithSize(baseURL, version, packagePayload string, includeSize bool) repoFixture {
	return newRepoFixtureWithVersionsAndSize(baseURL, []string{version}, []string{packagePayload}, includeSize)
}

func newRepoFixtureWithPayloads(baseURL string, packagePayloads []string) repoFixture {
	versions := make([]string, 0, len(packagePayloads))
	for i := range packagePayloads {
		versions = append(versions, fmt.Sprintf("%d.0", i+1))
	}
	return newRepoFixtureWithVersionsAndSize(baseURL, versions, packagePayloads, true)
}

func newRepoFixtureWithVersionsAndSize(baseURL string, versions []string, packagePayloads []string, includeSize bool) repoFixture {
	var packages strings.Builder
	files := map[string][]byte{}
	var packageURLs []string
	for i, packagePayload := range packagePayloads {
		version := versions[i]
		checksums := checksumBytes([]byte(packagePayload))
		filename := fmt.Sprintf("pool/main/d/demo/demo_%s_amd64.deb", version)
		sizeLine := ""
		if includeSize {
			sizeLine = fmt.Sprintf("Size: %d\n", len(packagePayload))
		}
		packages.WriteString(fmt.Sprintf(`Package: demo
Version: %s
Architecture: amd64
Filename: %s
%sMD5sum: %s
SHA1: %s
SHA256: %s
SHA512: %s

`, version, filename, sizeLine, checksums.MD5, checksums.SHA1, checksums.SHA256, checksums.SHA512))
		packageURL, _ := PackageURL(baseURL, filename)
		files[packageURL] = []byte(packagePayload)
		packageURLs = append(packageURLs, packageURL)
	}

	packagesBytes := []byte(packages.String())
	packagesChecksum := checksumBytes(packagesBytes)

	release := fmt.Sprintf(`Origin: Test
Label: Test
Suite: focal
Codename: focal
Architectures: amd64
Components: main
SHA256:
 %s %d main/binary-amd64/Packages

`, packagesChecksum.SHA256, len(packagesBytes))

	files[baseURL+"/dists/focal/Release"] = []byte(release)
	files[baseURL+"/dists/focal/main/binary-amd64/Packages"] = packagesBytes
	fixture := repoFixture{
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
		files:       files,
		packageURL:  packageURLs[0],
		packageURLs: packageURLs,
	}
	return fixture
}

type fakeDiskSpaceChecker struct {
	available int64
	err       error
}

func (c *fakeDiskSpaceChecker) AvailableBytes(_ string) (int64, error) {
	return c.available, c.err
}

type fakeDownloader struct {
	mu        sync.Mutex
	files     map[string][]byte
	downloads map[string]int
	failURLs  map[string]error
	started   chan string
	release   chan struct{}
	active    int
	maxActive int
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

func (d *fakeDownloader) DownloadPackage(ctx context.Context, rawURL, destination string, expected *download.Checksum) error {
	return d.DownloadPackageWithProgress(ctx, rawURL, destination, expected, nil)
}

func (d *fakeDownloader) DownloadPackageWithProgress(ctx context.Context, rawURL, destination string, _ *download.Checksum, onBytes func(int64)) error {
	data, ok := d.files[rawURL]
	if !ok {
		return &download.HTTPError{URL: rawURL, StatusCode: 404, Status: "404 Not Found"}
	}
	d.mu.Lock()
	d.downloads[rawURL]++
	d.active++
	if d.active > d.maxActive {
		d.maxActive = d.active
	}
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		d.active--
		d.mu.Unlock()
	}()
	if d.started != nil {
		d.started <- rawURL
	}
	if d.release != nil {
		select {
		case <-d.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if err := d.failURLs[rawURL]; err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return err
	}
	if onBytes != nil {
		mid := len(data) / 2
		if mid > 0 {
			onBytes(int64(mid))
		}
		onBytes(int64(len(data)))
	}
	return os.WriteFile(destination, data, 0644)
}

func (d *fakeDownloader) maxActiveDownloads() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.maxActive
}

func (d *fakeDownloader) GetLength(_ context.Context, rawURL string) (int64, error) {
	data, ok := d.files[rawURL]
	if !ok {
		return -1, &download.HTTPError{URL: rawURL, StatusCode: 404, Status: "404 Not Found"}
	}
	return int64(len(data)), nil
}

func waitDownloadStart(t *testing.T, started <-chan string) string {
	t.Helper()
	select {
	case rawURL := <-started:
		return rawURL
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for download to start")
		return ""
	}
}

type recordingProgressReporter struct {
	mu            sync.Mutex
	starts        int
	packageStarts int
	completes     int
	errors        int
	finishes      int
	bytes         int64
	packageBytes  map[string]int64
	lastFinish    DownloadProgressFinish
}

func (r *recordingProgressReporter) Start(DownloadProgressStart) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.starts++
	r.packageBytes = map[string]int64{}
}

func (r *recordingProgressReporter) PackageStart(DownloadProgressPackageStart) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.packageStarts++
}

func (r *recordingProgressReporter) Bytes(event DownloadProgressBytes) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.packageBytes == nil {
		r.packageBytes = map[string]int64{}
	}
	r.packageBytes[event.Filename] = event.CurrentBytes
	r.bytes = 0
	for _, current := range r.packageBytes {
		r.bytes += current
	}
}

func (r *recordingProgressReporter) PackageComplete(DownloadProgressPackageComplete) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.completes++
}

func (r *recordingProgressReporter) Error(DownloadProgressError) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errors++
}

func (r *recordingProgressReporter) Finish(event DownloadProgressFinish) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.finishes++
	r.lastFinish = event
}

type progressSnapshot struct {
	starts        int
	packageStarts int
	completes     int
	errors        int
	finishes      int
	bytes         int64
	lastFinish    DownloadProgressFinish
}

func (r *recordingProgressReporter) snapshot() progressSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return progressSnapshot{
		starts:        r.starts,
		packageStarts: r.packageStarts,
		completes:     r.completes,
		errors:        r.errors,
		finishes:      r.finishes,
		bytes:         r.bytes,
		lastFinish:    r.lastFinish,
	}
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
