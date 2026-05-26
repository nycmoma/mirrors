package config

import (
	"fmt"
	"net/url"
)

// Validate checks whether a mirror config has enough data for Phase 1.
func Validate(cfg Mirror) error {
	if cfg.Name == "" {
		return fmt.Errorf("missing required config field: name")
	}
	if cfg.URL == "" {
		return fmt.Errorf("missing required config field: url")
	}
	parsed, err := url.Parse(cfg.URL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("invalid mirror URL %q", cfg.URL)
	}
	if len(cfg.Dists) == 0 {
		return fmt.Errorf("missing required config field: dist")
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
	return nil
}
