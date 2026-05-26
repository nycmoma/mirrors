package config

import (
	"fmt"
	"net/url"
	"strings"
)

// Validate checks whether a mirror config has enough normalized data to use.
func Validate(cfg Mirror) error {
	if cfg.Name == "" {
		return fmt.Errorf("missing required config field: name")
	}
	if !validMirrorName.MatchString(cfg.Name) {
		return fmt.Errorf("invalid mirror name %q: use letters, digits, dots, underscores, and hyphens", cfg.Name)
	}
	if cfg.URL == "" {
		return fmt.Errorf("missing required config field: url")
	}
	parsed, err := url.Parse(cfg.URL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("invalid mirror URL %q: expected http or https URL", cfg.URL)
	}
	if len(cfg.Dists) == 0 {
		return fmt.Errorf("missing required config field: dist")
	}
	if len(cfg.Releases) == 0 {
		return fmt.Errorf("missing required config field: release")
	}
	if len(cfg.Arch) == 0 {
		return fmt.Errorf("missing required config field: arch")
	}
	if len(cfg.Components) == 0 {
		return fmt.Errorf("missing required config field: components")
	}
	if cfg.Path == "" {
		return fmt.Errorf("missing required config field: path")
	}
	if strings.Contains(cfg.Path, "..") {
		return fmt.Errorf("invalid mirror path %q: path must not contain '..'", cfg.Path)
	}
	return nil
}
