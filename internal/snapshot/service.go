package snapshot

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"mirrors/internal/config"
	"mirrors/internal/mirror"
	"mirrors/internal/state"
)

const (
	kindRegular = "regular"
	kindMerged  = "merged"
)

// Service coordinates snapshot creation, merge selection, and rollback state.
type Service struct {
	home string
	now  func() time.Time
}

// Option configures a Service.
type Option func(*Service)

// WithHome sets the home directory used for ~/.mirrors state.
func WithHome(home string) Option {
	return func(service *Service) {
		service.home = home
	}
}

// WithNow sets the clock used for local-date snapshot decisions.
func WithNow(now func() time.Time) Option {
	return func(service *Service) {
		service.now = now
	}
}

// NewService creates a snapshot service.
func NewService(options ...Option) (*Service, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	service := &Service{
		home: home,
		now:  time.Now,
	}
	for _, option := range options {
		option(service)
	}
	if strings.TrimSpace(service.home) == "" {
		return nil, fmt.Errorf("home directory is required")
	}
	if service.now == nil {
		return nil, fmt.Errorf("clock is required")
	}
	return service, nil
}

// UpdateResult summarizes snapshot work after a mirror update.
type UpdateResult struct {
	MirrorName       string
	DBPath           string
	Date             string
	Snapshots        []SnapshotResult
	SelectedSnapshot string
	Warnings         []string
}

// SnapshotResult describes one created or regenerated snapshot.
type SnapshotResult struct {
	Name         string
	Kind         string
	PackageCount int
	Regenerated  bool
}

// RollbackResult summarizes selected snapshot switching.
type RollbackResult struct {
	MirrorName        string
	DBPath            string
	SelectedSnapshot  string
	ResolvedSnapshots []string
}

// Summary describes one snapshot for info output.
type Summary struct {
	Record       state.SnapshotRecord
	PackageCount int
}

// CreateCurrent creates or regenerates today's regular and merged snapshots.
func (s *Service) CreateCurrent(cfg config.Mirror) (UpdateResult, error) {
	if err := config.Validate(cfg); err != nil {
		return UpdateResult{}, err
	}
	store, err := state.Open(s.dbPath(cfg.Name))
	if err != nil {
		return UpdateResult{}, err
	}
	defer func() {
		_ = store.Close()
	}()

	now := s.now().Local()
	date := localDate(now)
	result := UpdateResult{
		MirrorName: cfg.Name,
		DBPath:     s.dbPath(cfg.Name),
		Date:       date,
	}

	componentPackages, err := packageKeysByComponent(store)
	if err != nil {
		return UpdateResult{}, err
	}

	var selected string
	for _, target := range snapshotTargets(cfg) {
		keys := componentPackages[target.Component]
		regularName := mirror.SnapshotName(target.ComponentMirrorName, date)
		regenerated, err := s.createOrRegenerate(store, state.SnapshotRecord{
			Name:      regularName,
			Kind:      kindRegular,
			CreatedAt: now,
		}, keys, date)
		if err != nil {
			return UpdateResult{}, err
		}
		result.Snapshots = append(result.Snapshots, SnapshotResult{
			Name:         regularName,
			Kind:         kindRegular,
			PackageCount: len(keys),
			Regenerated:  regenerated,
		})
		selected = regularName

		if cfg.Merge.Enabled {
			mergedPackages, warnings, err := s.mergedPackages(store, target.ComponentMirrorName, cfg.Merge)
			if err != nil {
				return UpdateResult{}, err
			}
			result.Warnings = append(result.Warnings, warnings...)
			mergedName := mirror.MergedSnapshotName(target.ComponentMirrorName, date)
			regenerated, err := s.createOrRegeneratePackages(store, state.SnapshotRecord{
				Name:      mergedName,
				Kind:      kindMerged,
				CreatedAt: now,
			}, mergedPackages, date)
			if err != nil {
				return UpdateResult{}, err
			}
			result.Snapshots = append(result.Snapshots, SnapshotResult{
				Name:         mergedName,
				Kind:         kindMerged,
				PackageCount: len(mergedPackages),
				Regenerated:  regenerated,
			})
			selected = mergedName
		}
	}

	if selected == "" {
		return UpdateResult{}, fmt.Errorf("no snapshot targets resolved for mirror %q", cfg.Name)
	}
	if err := store.SetPublished(state.PublishedRecord{
		SnapshotName: selected,
		Path:         cfg.Path,
		Suite:        firstSuite(cfg),
		Component:    firstComponent(cfg),
		PublishedAt:  now,
	}); err != nil {
		return UpdateResult{}, err
	}
	if _, err := store.RecordUpdateHistory(state.UpdateRecord{
		Action:     "update",
		Status:     "ok",
		Message:    fmt.Sprintf("selected snapshot %s", selected),
		StartedAt:  now,
		FinishedAt: s.now(),
	}); err != nil {
		return UpdateResult{}, err
	}

	result.SelectedSnapshot = selected
	return result, nil
}

// Rollback switches selected snapshot state by date or snapshot ID/name.
func (s *Service) Rollback(mirrorName, date, id string) (RollbackResult, error) {
	if strings.TrimSpace(mirrorName) == "" {
		return RollbackResult{}, fmt.Errorf("mirror name is required")
	}
	if strings.TrimSpace(date) != "" && strings.TrimSpace(id) != "" {
		return RollbackResult{}, fmt.Errorf("provide either --date or --id, not both")
	}
	store, err := state.Open(s.dbPath(mirrorName))
	if err != nil {
		return RollbackResult{}, err
	}
	defer func() {
		_ = store.Close()
	}()

	cfg, err := store.MirrorConfig()
	if err != nil {
		return RollbackResult{}, err
	}
	targets, err := s.resolveRollbackTargets(store, cfg, date, id)
	if err != nil {
		return RollbackResult{}, err
	}
	selected := targets[len(targets)-1]
	now := s.now()
	if err := store.SetPublished(state.PublishedRecord{
		SnapshotName: selected,
		Path:         cfg.Path,
		Suite:        firstSuite(cfg),
		Component:    firstComponent(cfg),
		PublishedAt:  now,
	}); err != nil {
		return RollbackResult{}, err
	}
	if _, err := store.RecordUpdateHistory(state.UpdateRecord{
		Action:     "rollback",
		Status:     "ok",
		Message:    fmt.Sprintf("selected snapshot %s", selected),
		StartedAt:  now,
		FinishedAt: now,
	}); err != nil {
		return RollbackResult{}, err
	}

	return RollbackResult{
		MirrorName:        cfg.Name,
		DBPath:            s.dbPath(cfg.Name),
		SelectedSnapshot:  selected,
		ResolvedSnapshots: targets,
	}, nil
}

// List returns snapshot summaries for one mirror.
func (s *Service) List(mirrorName string) ([]Summary, error) {
	store, err := state.Open(s.dbPath(mirrorName))
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = store.Close()
	}()
	snapshots, err := store.Snapshots()
	if err != nil {
		return nil, err
	}
	return snapshotSummaries(store, snapshots)
}

// Snapshot returns one snapshot summary by name.
func (s *Service) Snapshot(mirrorName, snapshotName string) (Summary, error) {
	store, err := state.Open(s.dbPath(mirrorName))
	if err != nil {
		return Summary{}, err
	}
	defer func() {
		_ = store.Close()
	}()
	record, err := store.Snapshot(snapshotName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Summary{}, fmt.Errorf("snapshot %q does not exist", snapshotName)
		}
		return Summary{}, err
	}
	summaries, err := snapshotSummaries(store, []state.SnapshotRecord{record})
	if err != nil {
		return Summary{}, err
	}
	return summaries[0], nil
}

func (s *Service) createOrRegenerate(store *state.Store, snapshot state.SnapshotRecord, packageKeys []string, today string) (bool, error) {
	existing, err := store.Snapshot(snapshot.Name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, store.CreateSnapshot(snapshot, packageKeys)
		}
		return false, err
	}
	existingDate, err := dateFromSnapshotName(existing.Name)
	if err != nil {
		return false, err
	}
	if existingDate != today {
		return false, fmt.Errorf("snapshot %q is immutable because local date %s has passed", existing.Name, existingDate)
	}
	return true, store.ReplaceSnapshot(snapshot, packageKeys)
}

func (s *Service) createOrRegeneratePackages(store *state.Store, snapshot state.SnapshotRecord, packages []state.PackageRecord, today string) (bool, error) {
	existing, err := store.Snapshot(snapshot.Name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, store.ReplaceSnapshotPackages(snapshot, packages)
		}
		return false, err
	}
	existingDate, err := dateFromSnapshotName(existing.Name)
	if err != nil {
		return false, err
	}
	if existingDate != today {
		return false, fmt.Errorf("snapshot %q is immutable because local date %s has passed", existing.Name, existingDate)
	}
	return true, store.ReplaceSnapshotPackages(snapshot, packages)
}

func snapshotSummaries(store *state.Store, snapshots []state.SnapshotRecord) ([]Summary, error) {
	var summaries []Summary
	for _, record := range snapshots {
		keys, err := store.SnapshotPackageKeys(record.Name)
		if err != nil {
			return nil, err
		}
		summaries = append(summaries, Summary{
			Record:       record,
			PackageCount: len(keys),
		})
	}
	return summaries, nil
}

func (s *Service) mergedPackages(store *state.Store, componentMirrorName string, merge config.Merge) ([]state.PackageRecord, []string, error) {
	inputs, err := regularSnapshotsFor(store, componentMirrorName)
	if err != nil {
		return nil, nil, err
	}
	if len(inputs) == 0 {
		return nil, nil, fmt.Errorf("no regular snapshots available for %q", componentMirrorName)
	}
	limit := len(inputs)
	if merge.Depth > 0 && merge.Depth+1 < limit {
		limit = merge.Depth + 1
	}
	inputs = inputs[:limit]

	selectedByIdentity := map[string]string{}
	selectedKeys := map[string]bool{}
	selectedPackages := map[string]state.PackageRecord{}
	warned := map[string]bool{}
	var warnings []string
	for _, snapshot := range inputs {
		packages, err := store.SnapshotPackages(snapshot.Name)
		if err != nil {
			return nil, nil, err
		}
		for _, pkg := range packages {
			if selectedKeys[pkg.Key] {
				continue
			}
			identity := packageIdentity(pkg)
			if existingKey, ok := selectedByIdentity[identity]; ok && existingKey != pkg.Key {
				if !warned[identity] {
					warnings = append(warnings, fmt.Sprintf("package %s has multiple checksums across merge inputs; selected newest file", identity))
					warned[identity] = true
				}
				continue
			}
			selectedByIdentity[identity] = pkg.Key
			selectedKeys[pkg.Key] = true
			selectedPackages[pkg.Key] = pkg
		}
	}
	var keys []string
	for key := range selectedPackages {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	packages := make([]state.PackageRecord, 0, len(keys))
	for _, key := range keys {
		packages = append(packages, selectedPackages[key])
	}
	return packages, warnings, nil
}

func (s *Service) resolveRollbackTargets(store *state.Store, cfg config.Mirror, rawDate, rawID string) ([]string, error) {
	if strings.TrimSpace(rawID) != "" {
		id := strings.TrimSpace(rawID)
		if isDate(id) {
			rawDate = id
		} else {
			if _, err := store.Snapshot(id); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil, fmt.Errorf("snapshot %q does not exist", id)
				}
				return nil, err
			}
			return []string{id}, nil
		}
	}
	date := strings.TrimSpace(rawDate)
	if date == "" {
		return nil, fmt.Errorf("missing rollback target. Use --date YYYY-MM-DD or --id <snapshot_id>")
	}
	if !isDate(date) {
		return nil, fmt.Errorf("invalid rollback date %q: use YYYY-MM-DD", date)
	}

	var targets []string
	for _, target := range snapshotTargets(cfg) {
		name := mirror.SnapshotName(target.ComponentMirrorName, date)
		if cfg.Merge.Enabled {
			name = mirror.MergedSnapshotName(target.ComponentMirrorName, date)
		}
		if _, err := store.Snapshot(name); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, fmt.Errorf("snapshot %q does not exist", name)
			}
			return nil, err
		}
		targets = append(targets, name)
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("no rollback targets resolved for mirror %q", cfg.Name)
	}
	return targets, nil
}

func regularSnapshotsFor(store *state.Store, componentMirrorName string) ([]state.SnapshotRecord, error) {
	snapshots, err := store.Snapshots()
	if err != nil {
		return nil, err
	}
	prefix := componentMirrorName + "_"
	var matches []state.SnapshotRecord
	for _, snapshot := range snapshots {
		if snapshot.Kind != kindRegular {
			continue
		}
		if strings.HasPrefix(snapshot.Name, "MERGED-") {
			continue
		}
		if !strings.HasPrefix(snapshot.Name, prefix) {
			continue
		}
		if _, err := dateFromSnapshotName(snapshot.Name); err != nil {
			continue
		}
		matches = append(matches, snapshot)
	}
	sort.Slice(matches, func(i, j int) bool {
		left, _ := dateFromSnapshotName(matches[i].Name)
		right, _ := dateFromSnapshotName(matches[j].Name)
		if left == right {
			return matches[i].Name > matches[j].Name
		}
		return left > right
	})
	return matches, nil
}

type snapshotTarget struct {
	ComponentMirrorName string
	Component           string
}

func snapshotTargets(cfg config.Mirror) []snapshotTarget {
	var targets []snapshotTarget
	for _, dist := range cfg.Dists {
		for _, release := range cfg.Releases {
			for _, component := range cfg.Components {
				targets = append(targets, snapshotTarget{
					ComponentMirrorName: mirror.ComponentMirrorName(cfg.Name, dist, release, component),
					Component:           component,
				})
			}
		}
	}
	return targets
}

func packageKeysByComponent(store *state.Store) (map[string][]string, error) {
	keys, err := store.MirrorPackageKeys()
	if err != nil {
		return nil, err
	}
	packages, err := store.Packages(keys)
	if err != nil {
		return nil, err
	}
	byComponent := map[string][]string{}
	for _, pkg := range packages {
		byComponent[pkg.Component] = append(byComponent[pkg.Component], pkg.Key)
	}
	for component := range byComponent {
		sort.Strings(byComponent[component])
	}
	return byComponent, nil
}

func packageIdentity(pkg state.PackageRecord) string {
	return strings.Join([]string{pkg.Name, pkg.Version, pkg.Architecture}, "/")
}

func dateFromSnapshotName(name string) (string, error) {
	index := strings.LastIndex(name, "_")
	if index < 0 || index == len(name)-1 {
		return "", fmt.Errorf("snapshot %q does not contain a date suffix", name)
	}
	date := name[index+1:]
	if !isDate(date) {
		return "", fmt.Errorf("snapshot %q has invalid date suffix %q", name, date)
	}
	return date, nil
}

func isDate(value string) bool {
	_, err := time.Parse("2006-01-02", value)
	return err == nil
}

func localDate(value time.Time) string {
	return value.Local().Format("2006-01-02")
}

func firstSuite(cfg config.Mirror) string {
	if len(cfg.Dists) == 0 {
		return ""
	}
	release := "default"
	if len(cfg.Releases) > 0 {
		release = cfg.Releases[0]
	}
	return mirror.SuiteName(cfg.Dists[0], release)
}

func firstComponent(cfg config.Mirror) string {
	if len(cfg.Components) == 0 {
		return ""
	}
	return cfg.Components[0]
}

func (s *Service) dbPath(name string) string {
	return config.DBPathForHome(s.home, name)
}
