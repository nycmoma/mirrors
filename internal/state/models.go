package state

import "time"

// PackageRecord is one known package file and its package index identity.
type PackageRecord struct {
	Key          string
	Name         string
	Version      string
	Architecture string
	Filename     string
	Component    string
	Source       string
	Size         int64
	MD5          string
	SHA1         string
	SHA256       string
	SHA512       string
	PoolPath     string
	Fields       map[string]string
}

// SnapshotRecord describes a dated package membership set.
type SnapshotRecord struct {
	Name      string
	Kind      string
	CreatedAt time.Time
}

// PublishedRecord describes the currently published snapshot for a mirror.
type PublishedRecord struct {
	SnapshotName string
	Path         string
	Suite        string
	Component    string
	PublishedAt  time.Time
	Hidden       bool
}

// UpdateRecord stores one workflow attempt.
type UpdateRecord struct {
	ID         int64
	Action     string
	Status     string
	Message    string
	StartedAt  time.Time
	FinishedAt time.Time
}

// UpstreamIndexRecord stores downloaded upstream metadata references.
type UpstreamIndexRecord struct {
	Path      string
	Size      int64
	MD5       string
	SHA1      string
	SHA256    string
	SHA512    string
	FetchedAt time.Time
}

// UpstreamReleaseRecord stores upstream Release metadata needed for publishing.
type UpstreamReleaseRecord struct {
	Suite     string
	Origin    string
	Label     string
	FetchedAt time.Time
}

// CleanupRef records an explicit non-snapshot reference to a package pool path.
type CleanupRef struct {
	PoolPath string
	RefType  string
	RefName  string
}

// Stats summarizes mirror state for list/info output.
type Stats struct {
	KnownPackageCount     int
	MirrorPackageCount    int
	KnownPackageSizeBytes int64
	MirrorSizeBytes       int64
	SnapshotCount         int
	Published             *PublishedRecord
	LastUpdate            *UpdateRecord
}
