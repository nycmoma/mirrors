package mirror

import (
	"strings"

	"mirrors/internal/config"
)

// SuiteName maps a dist/release pair to an apt suite.
func SuiteName(dist, release string) string {
	return config.SuiteName(dist, release)
}

// ComponentMirrorName returns the full internal mirror name.
func ComponentMirrorName(prefix, dist, release, component string) string {
	return strings.Join([]string{prefix, SuiteName(dist, release), component}, "-")
}

// ComponentMirrorNames returns all internal mirror names for the config matrix.
func ComponentMirrorNames(prefix string, dists, releases, components []string) []string {
	var names []string
	for _, dist := range dists {
		for _, release := range releases {
			for _, component := range components {
				names = append(names, ComponentMirrorName(prefix, dist, release, component))
			}
		}
	}
	return names
}

// SnapshotName returns the dated snapshot name for a component mirror.
func SnapshotName(componentMirrorName, date string) string {
	return componentMirrorName + "_" + date
}

// MergedSnapshotName returns the dated merged snapshot name for a component mirror.
func MergedSnapshotName(componentMirrorName, date string) string {
	return "MERGED-" + SnapshotName(componentMirrorName, date)
}
