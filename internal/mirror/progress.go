package mirror

import "sync"

// ProgressReporter receives package download progress events.
type ProgressReporter interface {
	Start(DownloadProgressStart)
	PackageStart(DownloadProgressPackageStart)
	Bytes(DownloadProgressBytes)
	PackageComplete(DownloadProgressPackageComplete)
	Error(DownloadProgressError)
	Finish(DownloadProgressFinish)
}

type DownloadProgressStart struct {
	TotalPackages       int
	TotalKnownBytes     int64
	UnknownSizePackages int
	ReusedPackages      int
}

type DownloadProgressPackageStart struct {
	Filename string
	Size     int64
}

type DownloadProgressBytes struct {
	Filename     string
	CurrentBytes int64
	TotalBytes   int64
}

type DownloadProgressPackageComplete struct {
	Filename string
	Size     int64
}

type DownloadProgressError struct {
	Filename string
	Err      error
}

type DownloadProgressFinish struct {
	DownloadedPackages  int
	ReusedPackages      int
	FailedPackages      int
	TotalPackages       int
	DownloadedBytes     int64
	TotalKnownBytes     int64
	UnknownSizePackages int
}

type noopProgressReporter struct{}

func (noopProgressReporter) Start(DownloadProgressStart)                     {}
func (noopProgressReporter) PackageStart(DownloadProgressPackageStart)       {}
func (noopProgressReporter) Bytes(DownloadProgressBytes)                     {}
func (noopProgressReporter) PackageComplete(DownloadProgressPackageComplete) {}
func (noopProgressReporter) Error(DownloadProgressError)                     {}
func (noopProgressReporter) Finish(DownloadProgressFinish)                   {}

type synchronizedProgressReporter struct {
	mu       sync.Mutex
	reporter ProgressReporter
}

func newSynchronizedProgressReporter(reporter ProgressReporter) *synchronizedProgressReporter {
	if reporter == nil {
		reporter = noopProgressReporter{}
	}
	return &synchronizedProgressReporter{reporter: reporter}
}

func (reporter *synchronizedProgressReporter) Start(event DownloadProgressStart) {
	reporter.mu.Lock()
	defer reporter.mu.Unlock()
	reporter.reporter.Start(event)
}

func (reporter *synchronizedProgressReporter) PackageStart(event DownloadProgressPackageStart) {
	reporter.mu.Lock()
	defer reporter.mu.Unlock()
	reporter.reporter.PackageStart(event)
}

func (reporter *synchronizedProgressReporter) Bytes(event DownloadProgressBytes) {
	reporter.mu.Lock()
	defer reporter.mu.Unlock()
	reporter.reporter.Bytes(event)
}

func (reporter *synchronizedProgressReporter) PackageComplete(event DownloadProgressPackageComplete) {
	reporter.mu.Lock()
	defer reporter.mu.Unlock()
	reporter.reporter.PackageComplete(event)
}

func (reporter *synchronizedProgressReporter) Error(event DownloadProgressError) {
	reporter.mu.Lock()
	defer reporter.mu.Unlock()
	reporter.reporter.Error(event)
}

func (reporter *synchronizedProgressReporter) Finish(event DownloadProgressFinish) {
	reporter.mu.Lock()
	defer reporter.mu.Unlock()
	reporter.reporter.Finish(event)
}
