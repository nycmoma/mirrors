package mirror

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"mirrors/internal/debmeta"
	"mirrors/internal/pool"
	"mirrors/internal/state"
)

// DownloadPlan summarizes package download work before files are downloaded.
type DownloadPlan struct {
	MirrorName             string
	DBPath                 string
	PackagePoolRoot        string
	IndexesConsidered      int
	PackagesReused         int
	PackagesToDownload     int
	EstimatedDownloadBytes int64
	AvailableBytes         int64
	UnknownSizePackages    int
	Warnings               []string
}

// DiskSpaceChecker reports available bytes at a filesystem path.
type DiskSpaceChecker interface {
	AvailableBytes(path string) (int64, error)
}

type statfsDiskSpaceChecker struct{}

func (statfsDiskSpaceChecker) AvailableBytes(path string) (int64, error) {
	if path == "" {
		return 0, fmt.Errorf("disk space path is required")
	}
	if err := os.MkdirAll(path, 0755); err != nil {
		return 0, err
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return int64(stat.Bavail) * int64(stat.Bsize), nil
}

type fetchPlan struct {
	summary        DownloadPlan
	releases       []plannedRelease
	indexes        []plannedIndex
	downloads      []plannedDownload
	packagePool    *pool.Pool
	seenDownloadID map[string]bool
}

type plannedRelease struct {
	record state.UpstreamReleaseRecord
}

type plannedIndex struct {
	record   state.UpstreamIndexRecord
	packages []debmeta.Package
}

type plannedDownload struct {
	identity string
	pkg      debmeta.Package
}

func packageIdentity(pkg debmeta.Package) string {
	return pkg.Name + "\x00" + pkg.Version + "\x00" + pkg.Architecture + "\x00" + pkg.Filename + "\x00" + pkg.Checksums.MD5 + "\x00" + pkg.Checksums.SHA1 + "\x00" + pkg.Checksums.SHA256 + "\x00" + pkg.Checksums.SHA512
}

func (plan DownloadPlan) WarningsWithUnknownSize() []string {
	if plan.UnknownSizePackages == 0 {
		return plan.Warnings
	}
	warnings := append([]string(nil), plan.Warnings...)
	warnings = append(warnings, fmt.Sprintf("%d package(s) have unknown size metadata; estimated download size covers only packages with known sizes", plan.UnknownSizePackages))
	return warnings
}

func cleanPoolRoot(root string) string {
	if root == "" {
		return root
	}
	if abs, err := filepath.Abs(root); err == nil {
		return abs
	}
	return root
}
