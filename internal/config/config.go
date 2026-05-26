package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Mirror is the normalized [mirror] configuration.
type Mirror struct {
	Name       string
	URL        string
	Dists      []string
	Releases   []string
	Origin     string
	Label      string
	Arch       []string
	Components []string
	Path       string
	Merge      Merge
	Server     string
}

// Merge describes snapshot merge behavior.
type Merge struct {
	Enabled bool
	Depth   int
}

const (
	stateDirName   = ".mirrors"
	dbDirName      = "db"
	packageDirName = "packages"
)

var validMirrorName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// DBPath returns the default per-mirror SQLite database path.
func DBPath(mirrorName string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = "~"
	}
	return DBPathForHome(home, mirrorName)
}

// DBPathForHome returns the per-mirror SQLite database path for a specific home directory.
func DBPathForHome(home, mirrorName string) string {
	return filepath.Join(home, stateDirName, dbDirName, mirrorName+".sqlite")
}

// DBDirForHome returns the directory that contains per-mirror SQLite databases.
func DBDirForHome(home string) string {
	return filepath.Join(home, stateDirName, dbDirName)
}

// PackageDirForHome returns the package pool directory for a specific home directory.
func PackageDirForHome(home string) string {
	return filepath.Join(home, stateDirName, packageDirName)
}

// String renders the config in INI form.
func (m Mirror) String() string {
	return fmt.Sprintf(`[mirror]
name = %s
url = %s
dist = %s
release = %s
origin = %s
label = %s
arch = %s
components = %s
path = %s
merge = %s
server = %s
`,
		m.Name,
		m.URL,
		strings.Join(m.Dists, ", "),
		strings.Join(m.Releases, ", "),
		m.Origin,
		m.Label,
		strings.Join(m.Arch, ", "),
		strings.Join(m.Components, ", "),
		m.Path,
		m.Merge.String(),
		m.Server,
	)
}

// String renders Merge as the config value.
func (m Merge) String() string {
	if !m.Enabled {
		return "no"
	}
	if m.Depth > 0 {
		return fmt.Sprintf("%d", m.Depth)
	}
	return "yes"
}
