package debmeta

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	ChecksumMD5    = "MD5Sum"
	ChecksumSHA1   = "SHA1"
	ChecksumSHA256 = "SHA256"
	ChecksumSHA512 = "SHA512"
)

// Checksum identifies a file listed by Release metadata.
type Checksum struct {
	Algorithm string
	Value     string
	Size      int64
	Path      string
}

// FileChecksums stores hashes attached directly to a package file stanza.
type FileChecksums struct {
	MD5    string
	SHA1   string
	SHA256 string
	SHA512 string
}

// ParseChecksumList parses a Release checksum field such as SHA256.
func ParseChecksumList(algorithm, value string) ([]Checksum, error) {
	var checksums []Checksum
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.Fields(line)
		if len(parts) != 3 {
			return nil, fmt.Errorf("malformed %s checksum line: %q", algorithm, line)
		}

		size, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid %s checksum size %q: %w", algorithm, parts[1], err)
		}

		checksums = append(checksums, Checksum{
			Algorithm: algorithm,
			Value:     parts[0],
			Size:      size,
			Path:      parts[2],
		})
	}
	return checksums, nil
}
