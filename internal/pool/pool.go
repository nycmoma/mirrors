package pool

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Pool stores package files under an aptly-style checksum layout.
type Pool struct {
	mu   sync.Mutex
	root string
}

// ImportResult describes a package import.
type ImportResult struct {
	Path      string
	Checksum  Checksum
	Duplicate bool
}

// New creates a package pool rooted at root.
func New(root string) (*Pool, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("package pool root is required")
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}

	return &Pool{root: absRoot}, nil
}

// Root returns the absolute pool root.
func (p *Pool) Root() string {
	return p.root
}

// FullPath returns an absolute path for a pool-relative path.
func (p *Pool) FullPath(path string) string {
	return filepath.Join(p.root, filepath.Clean(path))
}

func (p *Pool) fullPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("pool path is required")
	}
	clean := filepath.Clean(path)
	if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid pool path %q", path)
	}
	return filepath.Join(p.root, clean), nil
}

// Import verifies and imports srcPath into the package pool.
func (p *Pool) Import(srcPath, filename string, expected Checksum) (ImportResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	actual, err := checksumsForFile(srcPath)
	if err != nil {
		return ImportResult{}, err
	}
	if err := verifyExpected(srcPath, actual, normalizeExpected(expected)); err != nil {
		return ImportResult{}, err
	}

	poolPath, err := PathFor(filename, actual)
	if err != nil {
		return ImportResult{}, err
	}
	targetPath, err := p.fullPath(poolPath)
	if err != nil {
		return ImportResult{}, err
	}

	if info, err := os.Stat(targetPath); err == nil {
		if info.Size() != actual.Size {
			return ImportResult{}, fmt.Errorf("pool target already exists with different size: %s", targetPath)
		}
		if err := p.verifyFullPath(targetPath, actual); err != nil {
			return ImportResult{}, err
		}
		return ImportResult{Path: poolPath, Checksum: actual, Duplicate: true}, nil
	} else if !os.IsNotExist(err) {
		return ImportResult{}, err
	}

	if err := os.MkdirAll(filepath.Dir(targetPath), 0777); err != nil {
		return ImportResult{}, err
	}
	if err := copyFile(targetPath, srcPath); err != nil {
		_ = os.Remove(targetPath)
		return ImportResult{}, err
	}

	return ImportResult{Path: poolPath, Checksum: actual}, nil
}

// List returns all pool-relative file paths.
func (p *Pool) List() ([]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	var result []string
	err := filepath.WalkDir(p.root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(p.root, path)
		if err != nil {
			return err
		}
		result = append(result, filepath.ToSlash(rel))
		return nil
	})
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	sort.Strings(result)
	return result, nil
}

// DiskUsage returns total file size in bytes under the pool root.
func (p *Pool) DiskUsage() (int64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	var total int64
	err := filepath.WalkDir(p.root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	if os.IsNotExist(err) {
		return 0, nil
	}
	return total, err
}

func copyFile(destination, source string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0666)
	if err != nil {
		return err
	}

	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func normalizeExpected(expected Checksum) Checksum {
	if expected.Size == 0 && expected.MD5 == "" && expected.SHA1 == "" && expected.SHA256 == "" && expected.SHA512 == "" {
		expected.Size = -1
	}
	return expected
}
