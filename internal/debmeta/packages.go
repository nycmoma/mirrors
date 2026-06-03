package debmeta

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ulikunitz/xz"
)

// Package contains the package index fields the mirror workflow needs.
type Package struct {
	Name         string
	Version      string
	Architecture string
	Filename     string
	Size         int64
	Checksums    FileChecksums
	Component    string
	Source       string
	Fields       Stanza
}

// OpenMaybeCompressed opens plain, gzip, or xz-compressed metadata by path.
func OpenMaybeCompressed(path string) (io.ReadCloser, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	switch filepath.Ext(path) {
	case ".gz":
		reader, err := gzip.NewReader(file)
		if err != nil {
			file.Close()
			return nil, err
		}
		return closeBoth{Reader: reader, first: reader, second: file}, nil
	case ".xz":
		reader, err := xz.NewReader(file)
		if err != nil {
			file.Close()
			return nil, err
		}
		return closeBoth{Reader: reader, second: file}, nil
	default:
		return file, nil
	}
}

// ParsePackagesFile parses a Packages, Packages.gz, or Packages.xz file.
func ParsePackagesFile(path string) ([]Package, error) {
	reader, err := OpenMaybeCompressed(path)
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	return ParsePackages(reader)
}

// ParsePackages parses a Debian Packages index.
func ParsePackages(r io.Reader) ([]Package, error) {
	stanzas, err := ParseStanzas(r)
	if err != nil {
		return nil, err
	}

	packages := make([]Package, 0, len(stanzas))
	for i, stanza := range stanzas {
		pkg, err := PackageFromStanza(stanza)
		if err != nil {
			return nil, fmt.Errorf("package stanza %d: %w", i+1, err)
		}
		packages = append(packages, pkg)
	}
	return packages, nil
}

// PackageFromStanza converts one Packages stanza into a typed package record.
func PackageFromStanza(stanza Stanza) (Package, error) {
	var missing []string
	for _, field := range []string{"Package", "Version", "Architecture", "Filename"} {
		if strings.TrimSpace(stanza[field]) == "" {
			missing = append(missing, field)
		}
	}
	if len(missing) > 0 {
		return Package{}, fmt.Errorf("missing required field(s): %s", strings.Join(missing, ", "))
	}

	size := int64(-1)
	if strings.TrimSpace(stanza["Size"]) != "" {
		var err error
		size, err = strconv.ParseInt(stanza["Size"], 10, 64)
		if err != nil {
			return Package{}, fmt.Errorf("invalid Size %q: %w", stanza["Size"], err)
		}
	}

	filename := stanza["Filename"]
	return Package{
		Name:         stanza["Package"],
		Version:      stanza["Version"],
		Architecture: stanza["Architecture"],
		Filename:     filename,
		Size:         size,
		Checksums: FileChecksums{
			MD5:    firstNonEmpty(stanza["MD5sum"], stanza["MD5Sum"]),
			SHA1:   stanza["SHA1"],
			SHA256: stanza["SHA256"],
			SHA512: stanza["SHA512"],
		},
		Component: deriveComponent(filename),
		Source:    stanza["Source"],
		Fields:    stanza,
	}, nil
}

type closeBoth struct {
	io.Reader
	first  io.Closer
	second io.Closer
}

func (c closeBoth) Close() error {
	var err error
	if c.first != nil {
		err = c.first.Close()
	}
	if c.second != nil {
		if secondErr := c.second.Close(); err == nil {
			err = secondErr
		}
	}
	return err
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func deriveComponent(filename string) string {
	parts := strings.Split(filename, "/")
	for i := 0; i+1 < len(parts); i++ {
		if parts[i] == "pool" {
			return parts[i+1]
		}
	}
	return ""
}
