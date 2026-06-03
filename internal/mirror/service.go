package mirror

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"mirrors/internal/config"
	"mirrors/internal/download"
	"mirrors/internal/pool"
	"mirrors/internal/state"
)

// Service coordinates mirror state, downloads, and package pool imports.
type Service struct {
	home                 string
	dbDir                string
	packageDir           string
	downloader           download.Downloader
	diskChecker          DiskSpaceChecker
	downloadPlanReporter func(DownloadPlan)
	progressReporter     ProgressReporter
	downloadThreads      int
}

// Option configures a Service.
type Option func(*Service)

// WithHome sets the home directory used for ~/.mirrors state.
func WithHome(home string) Option {
	return func(service *Service) {
		service.home = home
		service.dbDir = config.DBDirForHome(home)
		service.packageDir = config.PackageDirForHome(home)
	}
}

// WithStorageDirs sets explicit storage directories for DB files and packages.
func WithStorageDirs(dbDir, packageDir string) Option {
	return func(service *Service) {
		service.dbDir = dbDir
		service.packageDir = packageDir
	}
}

// WithDownloader sets the downloader used by fetch workflows.
func WithDownloader(downloader download.Downloader) Option {
	return func(service *Service) {
		service.downloader = downloader
	}
}

// WithDiskSpaceChecker sets the disk-space checker used before package downloads.
func WithDiskSpaceChecker(checker DiskSpaceChecker) Option {
	return func(service *Service) {
		service.diskChecker = checker
	}
}

// WithDownloadPlanReporter sets a callback invoked after planning and before package downloads.
func WithDownloadPlanReporter(reporter func(DownloadPlan)) Option {
	return func(service *Service) {
		service.downloadPlanReporter = reporter
	}
}

// WithProgressReporter sets the reporter used for package download progress.
func WithProgressReporter(reporter ProgressReporter) Option {
	return func(service *Service) {
		service.progressReporter = reporter
	}
}

// WithDownloadThreads sets package download concurrency. Values below 1 become 1.
func WithDownloadThreads(threads int) Option {
	return func(service *Service) {
		service.downloadThreads = threads
	}
}

// NewService creates a mirror service.
func NewService(options ...Option) (*Service, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	service := &Service{
		home:             home,
		dbDir:            config.DBDirForHome(home),
		packageDir:       config.PackageDirForHome(home),
		downloader:       download.NewClient(),
		diskChecker:      statfsDiskSpaceChecker{},
		progressReporter: noopProgressReporter{},
		downloadThreads:  4,
	}
	for _, option := range options {
		option(service)
	}
	if strings.TrimSpace(service.home) == "" {
		return nil, fmt.Errorf("home directory is required")
	}
	if strings.TrimSpace(service.dbDir) == "" {
		return nil, fmt.Errorf("DB directory is required")
	}
	if strings.TrimSpace(service.packageDir) == "" {
		return nil, fmt.Errorf("package directory is required")
	}
	if service.downloader == nil {
		return nil, fmt.Errorf("downloader is required")
	}
	if service.diskChecker == nil {
		return nil, fmt.Errorf("disk space checker is required")
	}
	if service.progressReporter == nil {
		service.progressReporter = noopProgressReporter{}
	}
	if service.downloadThreads < 1 {
		service.downloadThreads = 1
	}
	return service, nil
}

// Summary describes one mirror for list/info output.
type Summary struct {
	Config config.Mirror
	DBPath string
	Stats  state.Stats
}

// Create stores the mirror config and fetches current upstream packages.
func (s *Service) Create(ctx context.Context, cfg config.Mirror) (FetchResult, error) {
	return s.Fetch(ctx, cfg)
}

// Info returns one mirror summary by name.
func (s *Service) Info(name string) (Summary, error) {
	dbPath := s.dbPath(name)
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return Summary{}, fmt.Errorf("mirror %q does not exist", name)
		}
		return Summary{}, err
	}
	store, err := state.Open(dbPath)
	if err != nil {
		return Summary{}, err
	}
	defer func() {
		_ = store.Close()
	}()

	cfg, err := store.MirrorConfig()
	if err != nil {
		return Summary{}, err
	}
	stats, err := store.Stats()
	if err != nil {
		return Summary{}, err
	}
	return Summary{Config: cfg, DBPath: dbPath, Stats: stats}, nil
}

// List returns summaries for existing mirror DB files.
func (s *Service) List() ([]Summary, error) {
	entries, err := os.ReadDir(s.dbDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var summaries []Summary
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sqlite" {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".sqlite")
		summary, err := s.Info(name)
		if err != nil {
			return nil, fmt.Errorf("load mirror %q: %w", name, err)
		}
		summaries = append(summaries, summary)
	}
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].Config.Name < summaries[j].Config.Name
	})
	return summaries, nil
}

// Destroy removes one mirror DB file. Package pool cleanup is handled by cleanup workflows.
func (s *Service) Destroy(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("mirror name is required")
	}
	err := os.Remove(s.dbPath(name))
	if os.IsNotExist(err) {
		return fmt.Errorf("mirror %q does not exist", name)
	}
	return err
}

func (s *Service) dbPath(name string) string {
	return filepath.Join(s.dbDir, name+".sqlite")
}

func (s *Service) packagePool() (*pool.Pool, error) {
	return pool.New(s.packageDir)
}
