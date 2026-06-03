package mirror

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"mirrors/internal/debmeta"
	"mirrors/internal/download"
	"mirrors/internal/pool"
)

type progressDownloader interface {
	DownloadPackageWithProgress(ctx context.Context, rawURL, destination string, expected *download.Checksum, onBytes func(int64)) error
}

type downloadedPackage struct {
	identity string
	pkg      debmeta.Package
	poolPath string
}

func (s *Service) downloadMissingPackages(ctx context.Context, baseURL string, packagePool *pool.Pool, plan fetchPlan) (map[string]string, error) {
	downloaded := map[string]string{}
	if len(plan.downloads) == 0 {
		return downloaded, nil
	}

	reporter := newSynchronizedProgressReporter(s.progressReporter)
	reporter.Start(DownloadProgressStart{
		TotalPackages:       len(plan.downloads),
		TotalKnownBytes:     plan.summary.EstimatedDownloadBytes,
		UnknownSizePackages: plan.summary.UnknownSizePackages,
		ReusedPackages:      plan.summary.PackagesReused,
	})

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan plannedDownload)
	results := make(chan downloadedPackage, len(plan.downloads))
	workers := s.downloadThreads
	if workers > len(plan.downloads) {
		workers = len(plan.downloads)
	}

	var wg sync.WaitGroup
	var failMu sync.Mutex
	var firstErr error
	var failedPackage string
	var failedCount int
	fail := func(item plannedDownload, err error) {
		failMu.Lock()
		if firstErr == nil {
			firstErr = err
			failedPackage = item.pkg.Filename
			cancel()
		}
		failedCount++
		failMu.Unlock()
		reporter.Error(DownloadProgressError{Filename: item.pkg.Filename, Err: err})
	}

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range jobs {
				if ctx.Err() != nil {
					return
				}
				reporter.PackageStart(DownloadProgressPackageStart{
					Filename: item.pkg.Filename,
					Size:     item.pkg.Size,
				})
				result, err := s.downloadOnePackage(ctx, baseURL, packagePool, item, func(currentBytes int64) {
					reporter.Bytes(DownloadProgressBytes{
						Filename:     item.pkg.Filename,
						CurrentBytes: currentBytes,
						TotalBytes:   item.pkg.Size,
					})
				})
				if err != nil {
					fail(item, err)
					return
				}
				reporter.PackageComplete(DownloadProgressPackageComplete{
					Filename: item.pkg.Filename,
					Size:     item.pkg.Size,
				})
				results <- result
			}
		}()
	}

	go func() {
		defer close(jobs)
		for _, item := range plan.downloads {
			select {
			case jobs <- item:
			case <-ctx.Done():
				return
			}
		}
	}()

	wg.Wait()
	close(results)

	var downloadedBytes int64
	for result := range results {
		downloaded[result.identity] = result.poolPath
		if result.pkg.Size >= 0 {
			downloadedBytes += result.pkg.Size
		}
	}

	failMu.Lock()
	err := firstErr
	failed := failedPackage
	failedTotal := failedCount
	failMu.Unlock()
	reporter.Finish(DownloadProgressFinish{
		DownloadedPackages:  len(downloaded),
		ReusedPackages:      plan.summary.PackagesReused,
		FailedPackages:      failedTotal,
		TotalPackages:       len(plan.downloads),
		DownloadedBytes:     downloadedBytes,
		TotalKnownBytes:     plan.summary.EstimatedDownloadBytes,
		UnknownSizePackages: plan.summary.UnknownSizePackages,
	})

	if err != nil {
		return nil, fmt.Errorf("download package %q: %w", failed, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return downloaded, nil
}

func (s *Service) downloadOnePackage(ctx context.Context, baseURL string, packagePool *pool.Pool, item plannedDownload, onBytes func(int64)) (downloadedPackage, error) {
	packageURL, err := PackageURL(baseURL, item.pkg.Filename)
	if err != nil {
		return downloadedPackage{}, err
	}
	tmpDir, err := os.MkdirTemp("", "mirrors-fetch-*")
	if err != nil {
		return downloadedPackage{}, err
	}
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	tmpPath := filepath.Join(tmpDir, filepath.Base(item.pkg.Filename))
	byteReporter := onBytes
	if item.pkg.Size < 0 {
		byteReporter = nil
	}
	if downloader, ok := s.downloader.(progressDownloader); ok {
		err = downloader.DownloadPackageWithProgress(ctx, packageURL, tmpPath, downloadChecksum(item.pkg), byteReporter)
	} else {
		err = s.downloader.DownloadPackage(ctx, packageURL, tmpPath, downloadChecksum(item.pkg))
		if err == nil && byteReporter != nil {
			byteReporter(item.pkg.Size)
		}
	}
	if err != nil {
		return downloadedPackage{}, err
	}
	imported, err := packagePool.Import(tmpPath, item.pkg.Filename, poolChecksum(item.pkg))
	if err != nil {
		return downloadedPackage{}, err
	}
	return downloadedPackage{
		identity: item.identity,
		pkg:      item.pkg,
		poolPath: imported.Path,
	}, nil
}
