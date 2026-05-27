package pool

import (
	"fmt"
	"path/filepath"
	"strings"
)

// PathFor returns the package pool path relative to the pool root.
func PathFor(filename string, checksum Checksum) (string, error) {
	base := filepath.Base(filename)
	if base == "." || base == string(filepath.Separator) || base == "" {
		return "", fmt.Errorf("invalid package filename %q", filename)
	}

	hash := strings.TrimSpace(checksum.SHA256)
	if len(hash) < 32 {
		return "", fmt.Errorf("unable to compute pool location for %q: SHA256 is missing", base)
	}

	return filepath.Join(hash[0:2], hash[2:4], hash[4:32]+"_"+base), nil
}

// LegacyPathFor returns the old MD5-based pool path. New imports use PathFor.
func LegacyPathFor(filename string, checksum Checksum) (string, error) {
	base := filepath.Base(filename)
	if base == "." || base == string(filepath.Separator) || base == "" {
		return "", fmt.Errorf("invalid package filename %q", filename)
	}

	hash := strings.TrimSpace(checksum.MD5)
	if len(hash) < 4 {
		return "", fmt.Errorf("unable to compute legacy pool location for %q: MD5 is missing", base)
	}

	return filepath.Join(hash[0:2], hash[2:4], base), nil
}
