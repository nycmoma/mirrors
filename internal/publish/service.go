package publish

import (
	"bytes"
	"compress/gzip"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"mirrors/internal/config"
	"mirrors/internal/mirror"
	"mirrors/internal/state"
)

// Service generates unsigned apt repository output for selected snapshots.
type Service struct {
	home        string
	dbDir       string
	packageDir  string
	mirrorsRoot string
	now         func() time.Time
}

// Option configures a Service.
type Option func(*Service)

// WithHome sets the home directory used for state, package pool, and relative publish paths.
func WithHome(home string) Option {
	return func(service *Service) {
		service.home = home
		service.dbDir = config.DBDirForHome(home)
		service.packageDir = config.PackageDirForHome(home)
		service.mirrorsRoot = home
	}
}

// WithStorageDirs sets explicit directories for DB files, packages, and published mirrors.
func WithStorageDirs(dbDir, packageDir, mirrorsRoot string) Option {
	return func(service *Service) {
		service.dbDir = dbDir
		service.packageDir = packageDir
		service.mirrorsRoot = mirrorsRoot
	}
}

// WithNow sets the clock used for generated Release metadata.
func WithNow(now func() time.Time) Option {
	return func(service *Service) {
		service.now = now
	}
}

// NewService creates a publish service.
func NewService(options ...Option) (*Service, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	service := &Service{
		home:        home,
		dbDir:       config.DBDirForHome(home),
		packageDir:  config.PackageDirForHome(home),
		mirrorsRoot: home,
		now:         time.Now,
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
	if strings.TrimSpace(service.mirrorsRoot) == "" {
		return nil, fmt.Errorf("mirrors root is required")
	}
	if service.now == nil {
		return nil, fmt.Errorf("clock is required")
	}
	return service, nil
}

// Result summarizes published output.
type Result struct {
	MirrorName string
	Path       string
	Suite      string
	Snapshots  []string
	Packages   int
	Indexes    int
	Hidden     bool
}

// PublishSelected publishes the currently selected snapshot group for cfg.
func (s *Service) PublishSelected(cfg config.Mirror) (Result, error) {
	if err := config.Validate(cfg); err != nil {
		return Result{}, err
	}
	store, err := state.Open(s.dbPath(cfg.Name))
	if err != nil {
		return Result{}, err
	}
	defer func() {
		_ = store.Close()
	}()

	published, err := store.Published()
	if err != nil {
		return Result{}, err
	}
	snapshots, err := s.resolveSnapshotGroup(store, cfg, published.SnapshotName)
	if err != nil {
		return Result{}, err
	}
	result, err := s.publishSnapshots(store, cfg, snapshots)
	if err != nil {
		return Result{}, err
	}
	if err := store.SetPublished(state.PublishedRecord{
		SnapshotName: published.SnapshotName,
		Path:         cfg.Path,
		Suite:        result.Suite,
		Component:    firstComponent(cfg),
		PublishedAt:  s.now(),
	}); err != nil {
		return Result{}, err
	}
	return result, nil
}

// Hide removes published repository output while preserving DB state and packages.
func (s *Service) Hide(mirrorName string) (Result, error) {
	store, err := state.Open(s.dbPath(mirrorName))
	if err != nil {
		return Result{}, err
	}
	defer func() {
		_ = store.Close()
	}()
	cfg, err := store.MirrorConfig()
	if err != nil {
		return Result{}, err
	}
	published, err := store.Published()
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Result{}, err
	}
	root, err := s.publishRoot(cfg.Path)
	if err != nil {
		return Result{}, err
	}
	if err := removePublishedRoot(root); err != nil {
		return Result{}, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		if err := store.SetPublished(state.PublishedRecord{
			SnapshotName: published.SnapshotName,
			Path:         cfg.Path,
			Suite:        published.Suite,
			Component:    published.Component,
			PublishedAt:  s.now(),
			Hidden:       true,
		}); err != nil {
			return Result{}, err
		}
	}
	return Result{MirrorName: cfg.Name, Path: root, Hidden: true}, nil
}

func (s *Service) publishSnapshots(store *state.Store, cfg config.Mirror, snapshots []state.SnapshotRecord) (Result, error) {
	root, err := s.publishRoot(cfg.Path)
	if err != nil {
		return Result{}, err
	}
	if err := os.RemoveAll(filepath.Join(root, "dists")); err != nil {
		return Result{}, err
	}
	if err := os.RemoveAll(filepath.Join(root, "pool")); err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		return Result{}, err
	}

	packagesByIndex := map[indexKey][]state.PackageRecord{}
	packagesByKey := map[string]state.PackageRecord{}
	for _, snapshot := range snapshots {
		packages, err := store.SnapshotPackages(snapshot.Name)
		if err != nil {
			return Result{}, err
		}
		for _, pkg := range packages {
			packagesByKey[pkg.Key] = pkg
			key := indexKey{Component: componentForPackage(pkg), Arch: pkg.Architecture}
			packagesByIndex[key] = append(packagesByIndex[key], pkg)
		}
	}

	for _, pkg := range packagesByKey {
		if err := s.publishPackage(root, pkg); err != nil {
			return Result{}, err
		}
	}

	suite := firstSuite(cfg)
	var indexFiles []releaseFile
	for _, component := range cfg.Components {
		for _, arch := range cfg.Arch {
			key := indexKey{Component: component, Arch: arch}
			content := renderPackages(packagesByIndex[key])
			base := filepath.Join("dists", suite, component, "binary-"+arch)
			packagesRel := filepath.Join(base, "Packages")
			gzRel := filepath.Join(base, "Packages.gz")
			if err := writeFile(filepath.Join(root, packagesRel), content); err != nil {
				return Result{}, err
			}
			gzContent, err := gzipBytes(content)
			if err != nil {
				return Result{}, err
			}
			if err := writeFile(filepath.Join(root, gzRel), gzContent); err != nil {
				return Result{}, err
			}
			indexFiles = append(indexFiles, releaseFile{Path: filepath.ToSlash(filepath.Join(component, "binary-"+arch, "Packages")), Data: content})
			indexFiles = append(indexFiles, releaseFile{Path: filepath.ToSlash(filepath.Join(component, "binary-"+arch, "Packages.gz")), Data: gzContent})
		}
	}

	release, err := s.renderRelease(store, cfg, suite, indexFiles)
	if err != nil {
		return Result{}, err
	}
	if err := writeFile(filepath.Join(root, "dists", suite, "Release"), release); err != nil {
		return Result{}, err
	}

	var names []string
	for _, snapshot := range snapshots {
		names = append(names, snapshot.Name)
	}
	sort.Strings(names)
	return Result{
		MirrorName: cfg.Name,
		Path:       root,
		Suite:      suite,
		Snapshots:  names,
		Packages:   len(packagesByKey),
		Indexes:    len(indexFiles),
	}, nil
}

func (s *Service) resolveSnapshotGroup(store *state.Store, cfg config.Mirror, selected string) ([]state.SnapshotRecord, error) {
	date, err := dateFromSnapshotName(selected)
	if err != nil {
		record, err := store.Snapshot(selected)
		if err != nil {
			return nil, err
		}
		return []state.SnapshotRecord{record}, nil
	}
	var snapshots []state.SnapshotRecord
	for _, target := range snapshotTargets(cfg) {
		name := mirror.SnapshotName(target, date)
		if strings.HasPrefix(selected, "MERGED-") || cfg.Merge.Enabled {
			name = mirror.MergedSnapshotName(target, date)
		}
		record, err := store.Snapshot(name)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return nil, err
		}
		snapshots = append(snapshots, record)
	}
	if len(snapshots) == 0 {
		record, err := store.Snapshot(selected)
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, record)
	}
	return snapshots, nil
}

func (s *Service) publishPackage(root string, pkg state.PackageRecord) error {
	filename := firstNonEmpty(pkg.Fields["Filename"], pkg.Filename)
	if filename == "" {
		return fmt.Errorf("package %s has no filename", pkg.Key)
	}
	clean := filepath.Clean(filename)
	if filepath.IsAbs(clean) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("invalid package filename %q", filename)
	}
	source := filepath.Join(s.packageDir, filepath.FromSlash(pkg.PoolPath))
	destination := filepath.Join(root, filepath.FromSlash(clean))
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return err
	}
	if err := os.Link(source, destination); err == nil {
		return nil
	} else if !os.IsExist(err) {
		if copyErr := copyFile(destination, source); copyErr == nil {
			return nil
		} else if !os.IsExist(copyErr) {
			return fmt.Errorf("publish package %s: link failed: %v; copy failed: %w", filename, err, copyErr)
		}
	}
	info, err := os.Stat(destination)
	if err != nil {
		return err
	}
	if info.Size() == pkg.Size {
		return nil
	}
	if err := os.Remove(destination); err != nil {
		return err
	}
	if err := os.Link(source, destination); err == nil {
		return nil
	}
	return copyFile(destination, source)
}

func (s *Service) renderRelease(store *state.Store, cfg config.Mirror, suite string, files []releaseFile) ([]byte, error) {
	origin := cfg.Origin
	label := cfg.Label
	upstream, err := store.UpstreamRelease(suite)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if origin == "default" {
		origin = upstream.Origin
	}
	if label == "default" {
		label = upstream.Label
	}
	if origin == "" {
		origin = cfg.Name
	}
	if label == "" {
		label = cfg.Name
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].Path < files[j].Path
	})
	var buf bytes.Buffer
	writeField(&buf, "Origin", origin)
	writeField(&buf, "Label", label)
	writeField(&buf, "Suite", suite)
	writeField(&buf, "Codename", suite)
	writeField(&buf, "Date", s.now().UTC().Format(time.RFC1123))
	writeField(&buf, "Architectures", strings.Join(cfg.Arch, " "))
	writeField(&buf, "Components", strings.Join(cfg.Components, " "))
	writeChecksumBlock(&buf, "MD5Sum", files, md5Hex)
	writeChecksumBlock(&buf, "SHA1", files, sha1Hex)
	writeChecksumBlock(&buf, "SHA256", files, sha256Hex)
	writeChecksumBlock(&buf, "SHA512", files, sha512Hex)
	return buf.Bytes(), nil
}

func (s *Service) publishRoot(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("publish path is required")
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}
	return filepath.Join(s.mirrorsRoot, filepath.Clean(path)), nil
}

type indexKey struct {
	Component string
	Arch      string
}

type releaseFile struct {
	Path string
	Data []byte
}

func renderPackages(packages []state.PackageRecord) []byte {
	sort.Slice(packages, func(i, j int) bool {
		left := []string{packages[i].Name, packages[i].Version, packages[i].Architecture, packages[i].Filename}
		right := []string{packages[j].Name, packages[j].Version, packages[j].Architecture, packages[j].Filename}
		return strings.Join(left, "\x00") < strings.Join(right, "\x00")
	})
	var buf bytes.Buffer
	for _, pkg := range packages {
		fields := packageFields(pkg)
		for _, key := range orderedFieldNames(fields) {
			writeField(&buf, key, fields[key])
		}
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}

func packageFields(pkg state.PackageRecord) map[string]string {
	fields := map[string]string{}
	for key, value := range pkg.Fields {
		fields[key] = value
	}
	defaults := map[string]string{
		"Package":      pkg.Name,
		"Version":      pkg.Version,
		"Architecture": pkg.Architecture,
		"Filename":     pkg.Filename,
		"Size":         fmt.Sprintf("%d", pkg.Size),
		"MD5sum":       pkg.MD5,
		"SHA1":         pkg.SHA1,
		"SHA256":       pkg.SHA256,
		"SHA512":       pkg.SHA512,
	}
	if pkg.Source != "" {
		defaults["Source"] = pkg.Source
	}
	for key, value := range defaults {
		if strings.TrimSpace(fields[key]) == "" {
			fields[key] = value
		}
	}
	return fields
}

func orderedFieldNames(fields map[string]string) []string {
	preferred := []string{"Package", "Source", "Version", "Architecture", "Filename", "Size", "MD5sum", "SHA1", "SHA256", "SHA512", "Description"}
	seen := map[string]bool{}
	var names []string
	for _, name := range preferred {
		if _, ok := fields[name]; ok {
			names = append(names, name)
			seen[name] = true
		}
	}
	var rest []string
	for name := range fields {
		if !seen[name] {
			rest = append(rest, name)
		}
	}
	sort.Strings(rest)
	return append(names, rest...)
}

func writeField(buf *bytes.Buffer, key, value string) {
	lines := strings.Split(strings.TrimRight(value, "\n"), "\n")
	if len(lines) == 0 {
		fmt.Fprintf(buf, "%s:\n", key)
		return
	}
	fmt.Fprintf(buf, "%s: %s\n", key, lines[0])
	for _, line := range lines[1:] {
		if line == "" || line[0] == ' ' || line[0] == '\t' {
			fmt.Fprintf(buf, "%s\n", line)
		} else {
			fmt.Fprintf(buf, " %s\n", line)
		}
	}
}

func writeChecksumBlock(buf *bytes.Buffer, name string, files []releaseFile, sum func([]byte) string) {
	fmt.Fprintf(buf, "%s:\n", name)
	for _, file := range files {
		fmt.Fprintf(buf, " %s %d %s\n", sum(file.Data), len(file.Data), file.Path)
	}
}

func gzipBytes(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	writer := gzip.NewWriter(&buf)
	if _, err := writer.Write(data); err != nil {
		_ = writer.Close()
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func copyFile(destination, source string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func removePublishedRoot(root string) error {
	clean := filepath.Clean(root)
	if clean == "." || clean == string(filepath.Separator) {
		return fmt.Errorf("refusing to remove unsafe publish path %q", root)
	}
	return os.RemoveAll(clean)
}

func snapshotTargets(cfg config.Mirror) []string {
	var targets []string
	for _, dist := range cfg.Dists {
		for _, release := range cfg.Releases {
			for _, component := range cfg.Components {
				targets = append(targets, mirror.ComponentMirrorName(cfg.Name, dist, release, component))
			}
		}
	}
	return targets
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

func componentForPackage(pkg state.PackageRecord) string {
	if pkg.Component != "" {
		return pkg.Component
	}
	filename := firstNonEmpty(pkg.Fields["Filename"], pkg.Filename)
	parts := strings.Split(filepath.ToSlash(filename), "/")
	for i := 0; i+1 < len(parts); i++ {
		if parts[i] == "pool" {
			return parts[i+1]
		}
	}
	return ""
}

func dateFromSnapshotName(name string) (string, error) {
	index := strings.LastIndex(name, "_")
	if index < 0 || index == len(name)-1 {
		return "", fmt.Errorf("snapshot %q does not contain a date suffix", name)
	}
	date := name[index+1:]
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return "", err
	}
	return date, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func md5Hex(data []byte) string {
	sum := md5.Sum(data)
	return fmt.Sprintf("%x", sum)
}

func sha1Hex(data []byte) string {
	sum := sha1.Sum(data)
	return fmt.Sprintf("%x", sum)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum)
}

func sha512Hex(data []byte) string {
	sum := sha512.Sum512(data)
	return fmt.Sprintf("%x", sum)
}

func (s *Service) dbPath(name string) string {
	return filepath.Join(s.dbDir, name+".sqlite")
}
