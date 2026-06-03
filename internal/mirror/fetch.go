package mirror

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"time"

	"mirrors/internal/config"
	"mirrors/internal/debmeta"
	"mirrors/internal/download"
	"mirrors/internal/pool"
	"mirrors/internal/state"
)

// FetchResult summarizes a fetch/create run.
type FetchResult struct {
	MirrorName          string
	DBPath              string
	Plan                DownloadPlan
	IndexCount          int
	PackageCount        int
	DownloadedCount     int
	ReusedCount         int
	AddedPackageCount   int
	RemovedPackageCount int
	Unchanged           bool
}

// Fetch downloads upstream package indexes and missing package files, then updates state.
func (s *Service) Fetch(ctx context.Context, cfg config.Mirror) (FetchResult, error) {
	if err := config.Validate(cfg); err != nil {
		return FetchResult{}, err
	}

	store, err := state.Open(s.dbPath(cfg.Name))
	if err != nil {
		return FetchResult{}, err
	}
	defer func() {
		_ = store.Close()
	}()

	packagePool, err := s.packagePool()
	if err != nil {
		return FetchResult{}, err
	}

	startedAt := time.Now()
	result := FetchResult{MirrorName: cfg.Name, DBPath: s.dbPath(cfg.Name)}
	var packageKeys []string
	seen := map[string]bool{}

	oldKeys, err := store.MirrorPackageKeys()
	if err != nil {
		return FetchResult{}, err
	}

	err = store.WithTx(func(tx *state.Tx) error {
		return tx.SaveMirrorConfig(cfg)
	})
	if err != nil {
		return FetchResult{}, err
	}

	plan, err := s.buildFetchPlan(ctx, cfg, packagePool)
	if err != nil {
		_ = recordFetchFailure(store, startedAt, err)
		return FetchResult{}, err
	}
	result.Plan = plan.summary
	if s.downloadPlanReporter != nil {
		s.downloadPlanReporter(plan.summary)
	}

	downloadedPackages, err := s.downloadMissingPackages(ctx, cfg.URL, packagePool, plan)
	if err != nil {
		_ = recordFetchFailure(store, startedAt, err)
		return FetchResult{}, err
	}
	seenDownloadedPackages := map[string]bool{}

	for _, release := range plan.releases {
		if err := store.UpsertUpstreamRelease(release.record); err != nil {
			_ = recordFetchFailure(store, startedAt, err)
			return FetchResult{}, err
		}
	}

	for _, index := range plan.indexes {
		if err := store.UpsertUpstreamIndex(index.record); err != nil {
			_ = recordFetchFailure(store, startedAt, err)
			return FetchResult{}, err
		}
		result.IndexCount++

		for _, pkg := range index.packages {
			key, reused, err := s.recordPlannedPackage(packagePool, store, pkg, downloadedPackages, seenDownloadedPackages)
			if err != nil {
				_ = recordFetchFailure(store, startedAt, err)
				return FetchResult{}, err
			}
			if reused {
				result.ReusedCount++
			} else {
				result.DownloadedCount++
			}
			if !seen[key] {
				seen[key] = true
				packageKeys = append(packageKeys, key)
			}
		}
	}

	if err := store.ReplaceMirrorPackages(packageKeys); err != nil {
		_ = recordFetchFailure(store, startedAt, err)
		return FetchResult{}, err
	}
	if _, err := store.RecordUpdateHistory(state.UpdateRecord{
		Action:     "fetch",
		Status:     "ok",
		Message:    fmt.Sprintf("fetched %d package index(es), %d package(s)", result.IndexCount, len(packageKeys)),
		StartedAt:  startedAt,
		FinishedAt: time.Now(),
	}); err != nil {
		return FetchResult{}, err
	}

	result.PackageCount = len(packageKeys)
	result.AddedPackageCount, result.RemovedPackageCount = packageSetDiff(oldKeys, packageKeys)
	result.Unchanged = result.AddedPackageCount == 0 && result.RemovedPackageCount == 0
	return result, nil
}

func (s *Service) buildFetchPlan(ctx context.Context, cfg config.Mirror, packagePool *pool.Pool) (fetchPlan, error) {
	plan := fetchPlan{
		summary: DownloadPlan{
			MirrorName:      cfg.Name,
			DBPath:          s.dbPath(cfg.Name),
			PackagePoolRoot: cleanPoolRoot(packagePool.Root()),
		},
		packagePool:    packagePool,
		seenDownloadID: map[string]bool{},
	}

	for _, dist := range cfg.Dists {
		for _, release := range cfg.Releases {
			suite := SuiteName(dist, release)
			releaseMeta, err := s.fetchRelease(ctx, cfg.URL, suite)
			if err != nil {
				return fetchPlan{}, err
			}
			if err := validateRelease(releaseMeta, cfg, suite); err != nil {
				return fetchPlan{}, err
			}
			plan.releases = append(plan.releases, plannedRelease{record: state.UpstreamReleaseRecord{
				Suite:     suite,
				Origin:    releaseMeta.Origin,
				Label:     releaseMeta.Label,
				FetchedAt: time.Now(),
			}})

			for _, component := range cfg.Components {
				if !contains(releaseMeta.Components, component) {
					return fetchPlan{}, fmt.Errorf("suite %q does not contain component %q", suite, component)
				}
				for _, arch := range cfg.Arch {
					if !contains(releaseMeta.Architectures, arch) {
						return fetchPlan{}, fmt.Errorf("suite %q does not contain architecture %q", suite, arch)
					}
					indexPackages, indexRecord, err := s.fetchPackageIndex(ctx, cfg.URL, suite, component, arch, releaseMeta)
					if err != nil {
						return fetchPlan{}, err
					}
					plan.summary.IndexesConsidered++
					plan.indexes = append(plan.indexes, plannedIndex{
						record:   indexRecord,
						packages: indexPackages,
					})
					for _, pkg := range indexPackages {
						if err := plan.addPackageCandidate(pkg); err != nil {
							return fetchPlan{}, err
						}
					}
				}
			}
		}
	}

	available, err := s.diskChecker.AvailableBytes(packagePool.Root())
	if err != nil {
		return fetchPlan{}, fmt.Errorf("check available disk space at %s: %w", packagePool.Root(), err)
	}
	plan.summary.AvailableBytes = available
	if plan.summary.EstimatedDownloadBytes > available {
		return fetchPlan{}, fmt.Errorf("not enough disk space in package pool %q: need %d bytes, available %d bytes", packagePool.Root(), plan.summary.EstimatedDownloadBytes, available)
	}
	plan.summary.Warnings = plan.summary.WarningsWithUnknownSize()
	return plan, nil
}

func (plan *fetchPlan) addPackageCandidate(pkg debmeta.Package) error {
	expectedPoolChecksum := poolChecksum(pkg)
	poolPath, err := pool.PathFor(pkg.Filename, expectedPoolChecksum)
	if err == nil {
		ok, err := plan.packagePool.Verify(poolPath, expectedPoolChecksum)
		if err != nil {
			return err
		}
		if ok {
			plan.summary.PackagesReused++
			return nil
		}
	}

	identity := packageIdentity(pkg)
	if plan.seenDownloadID[identity] {
		return nil
	}
	plan.seenDownloadID[identity] = true
	plan.summary.PackagesToDownload++
	plan.downloads = append(plan.downloads, plannedDownload{identity: identity, pkg: pkg})
	if pkg.Size >= 0 {
		plan.summary.EstimatedDownloadBytes += pkg.Size
	} else {
		plan.summary.UnknownSizePackages++
	}
	return nil
}

func (s *Service) recordPlannedPackage(packagePool *pool.Pool, store *state.Store, pkg debmeta.Package, downloadedPackages map[string]string, seenDownloadedPackages map[string]bool) (string, bool, error) {
	identity := packageIdentity(pkg)
	if poolPath, ok := downloadedPackages[identity]; ok {
		record := packageRecord(pkg, poolPath)
		key, err := store.UpsertPackage(record)
		if err != nil {
			return "", false, err
		}
		if seenDownloadedPackages[identity] {
			return key, true, nil
		}
		seenDownloadedPackages[identity] = true
		return key, false, nil
	}

	expectedPoolChecksum := poolChecksum(pkg)
	poolPath, err := pool.PathFor(pkg.Filename, expectedPoolChecksum)
	if err != nil {
		return "", false, err
	}
	ok, err := packagePool.Verify(poolPath, expectedPoolChecksum)
	if err != nil {
		return "", false, err
	}
	if !ok {
		return "", false, fmt.Errorf("planned package %q was not downloaded and is not present in package pool", pkg.Filename)
	}

	record := packageRecord(pkg, poolPath)
	key, err := store.UpsertPackage(record)
	return key, true, err
}

func (s *Service) fetchRelease(ctx context.Context, baseURL, suite string) (*debmeta.Release, error) {
	inReleaseURL, err := InReleaseURL(baseURL, suite)
	if err != nil {
		return nil, err
	}
	data, err := s.downloader.FetchMetadata(ctx, inReleaseURL, nil)
	if err == nil {
		release, _, err := debmeta.ParseInRelease(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", inReleaseURL, err)
		}
		return release, nil
	}
	if !isHTTPStatus(err, 404) {
		return nil, err
	}

	releaseURL, err := ReleaseURL(baseURL, suite)
	if err != nil {
		return nil, err
	}
	data, err = s.downloader.FetchMetadata(ctx, releaseURL, nil)
	if err != nil {
		return nil, err
	}
	release, err := debmeta.ParseRelease(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", releaseURL, err)
	}
	return release, nil
}

func (s *Service) fetchPackageIndex(ctx context.Context, baseURL, suite, component, arch string, release *debmeta.Release) ([]debmeta.Package, state.UpstreamIndexRecord, error) {
	for _, compression := range []string{"xz", "gz", ""} {
		indexPath := PackagesIndexPath(component, arch, compression)
		expected := releaseChecksum(release, indexPath)
		if expected == nil {
			continue
		}
		indexURL, err := PackagesIndexURL(baseURL, suite, component, arch, compression)
		if err != nil {
			return nil, state.UpstreamIndexRecord{}, err
		}
		data, err := s.downloader.FetchMetadata(ctx, indexURL, expected)
		if err != nil {
			return nil, state.UpstreamIndexRecord{}, err
		}
		packages, err := parsePackagesBytes(data, compression)
		if err != nil {
			return nil, state.UpstreamIndexRecord{}, fmt.Errorf("parse %s: %w", indexURL, err)
		}
		return packages, upstreamIndexRecord(path.Join("dists", suite, indexPath), expected), nil
	}
	return nil, state.UpstreamIndexRecord{}, fmt.Errorf("missing Packages index for component %q architecture %q", component, arch)
}

func parsePackagesBytes(data []byte, compression string) ([]debmeta.Package, error) {
	if compression == "" {
		return debmeta.ParsePackages(bytes.NewReader(data))
	}

	dir, err := os.MkdirTemp("", "mirrors-index-*")
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = os.RemoveAll(dir)
	}()

	path := filepath.Join(dir, "Packages."+compression)
	if err := os.WriteFile(path, data, 0644); err != nil {
		return nil, err
	}
	return debmeta.ParsePackagesFile(path)
}

func releaseChecksum(release *debmeta.Release, indexPath string) *download.Checksum {
	result := &download.Checksum{Size: -1}
	found := false
	for _, checksum := range release.Checksums {
		if checksum.Path != indexPath {
			continue
		}
		found = true
		result.Size = checksum.Size
		switch checksum.Algorithm {
		case debmeta.ChecksumMD5:
			result.MD5 = checksum.Value
		case debmeta.ChecksumSHA1:
			result.SHA1 = checksum.Value
		case debmeta.ChecksumSHA256:
			result.SHA256 = checksum.Value
		case debmeta.ChecksumSHA512:
			result.SHA512 = checksum.Value
		}
	}
	if !found {
		return nil
	}
	return result
}

func upstreamIndexRecord(path string, checksum *download.Checksum) state.UpstreamIndexRecord {
	return state.UpstreamIndexRecord{
		Path:      path,
		Size:      checksum.Size,
		MD5:       checksum.MD5,
		SHA1:      checksum.SHA1,
		SHA256:    checksum.SHA256,
		SHA512:    checksum.SHA512,
		FetchedAt: time.Now(),
	}
}

func downloadChecksum(pkg debmeta.Package) *download.Checksum {
	return &download.Checksum{
		Size:   pkg.Size,
		MD5:    pkg.Checksums.MD5,
		SHA1:   pkg.Checksums.SHA1,
		SHA256: pkg.Checksums.SHA256,
		SHA512: pkg.Checksums.SHA512,
	}
}

func poolChecksum(pkg debmeta.Package) pool.Checksum {
	return pool.Checksum{
		Size:   pkg.Size,
		MD5:    pkg.Checksums.MD5,
		SHA1:   pkg.Checksums.SHA1,
		SHA256: pkg.Checksums.SHA256,
		SHA512: pkg.Checksums.SHA512,
	}
}

func packageRecord(pkg debmeta.Package, poolPath string) state.PackageRecord {
	return state.PackageRecord{
		Name:         pkg.Name,
		Version:      pkg.Version,
		Architecture: pkg.Architecture,
		Filename:     pkg.Filename,
		Component:    pkg.Component,
		Source:       pkg.Source,
		Size:         pkg.Size,
		MD5:          pkg.Checksums.MD5,
		SHA1:         pkg.Checksums.SHA1,
		SHA256:       pkg.Checksums.SHA256,
		SHA512:       pkg.Checksums.SHA512,
		PoolPath:     poolPath,
		Fields:       mapFromStanza(pkg.Fields),
	}
}

func mapFromStanza(stanza debmeta.Stanza) map[string]string {
	fields := map[string]string{}
	for key, value := range stanza {
		fields[key] = value
	}
	return fields
}

func validateRelease(release *debmeta.Release, cfg config.Mirror, suite string) error {
	if cfg.Origin != "default" && release.Origin != "" && release.Origin != cfg.Origin {
		return fmt.Errorf("suite %q origin mismatch: got %q, want %q", suite, release.Origin, cfg.Origin)
	}
	if cfg.Label != "default" && release.Label != "" && release.Label != cfg.Label {
		return fmt.Errorf("suite %q label mismatch: got %q, want %q", suite, release.Label, cfg.Label)
	}
	return nil
}

func recordFetchFailure(store *state.Store, startedAt time.Time, err error) error {
	_, recordErr := store.RecordUpdateHistory(state.UpdateRecord{
		Action:     "fetch",
		Status:     "error",
		Message:    err.Error(),
		StartedAt:  startedAt,
		FinishedAt: time.Now(),
	})
	return recordErr
}

func packageSetDiff(oldKeys, newKeys []string) (int, int) {
	oldSet := stringSet(oldKeys)
	newSet := stringSet(newKeys)
	var added int
	var removed int
	for key := range newSet {
		if !oldSet[key] {
			added++
		}
	}
	for key := range oldSet {
		if !newSet[key] {
			removed++
		}
	}
	return added, removed
}

func stringSet(values []string) map[string]bool {
	set := map[string]bool{}
	for _, value := range values {
		set[value] = true
	}
	return set
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func isHTTPStatus(err error, status int) bool {
	var httpErr *download.HTTPError
	return errors.As(err, &httpErr) && httpErr.StatusCode == status
}
