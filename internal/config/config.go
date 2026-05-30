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
	ConfigPath string
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
	Signing    Signing
}

// Merge describes snapshot merge behavior.
type Merge struct {
	Enabled bool
	Depth   int
}

// Signing describes optional repository signing settings.
type Signing struct {
	Disabled          bool
	GPGHome           string
	GPGKey            string
	GPGPassphrase     string
	GPGPassphraseFile string
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
sign = %s
gpg_home = %s
gpg_key = %s
gpg_passphrase_file = %s
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
		signString(!m.Signing.Disabled),
		m.Signing.GPGHome,
		m.Signing.GPGKey,
		m.Signing.GPGPassphraseFile,
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

func signString(enabled bool) string {
	if enabled {
		return "yes"
	}
	return "no"
}
