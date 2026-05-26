package config

import (
	"fmt"
	"path/filepath"
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

// DBPath returns the default per-mirror SQLite database path.
func DBPath(mirrorName string) string {
	return filepath.Join("~", ".mirrors", "db", mirrorName+".sqlite")
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
