package pool

import (
	"fmt"
	"os"
)

// ReferenceChecker reports whether a pool-relative path is still referenced.
type ReferenceChecker interface {
	IsReferenced(poolPath string) (bool, error)
}

// Remove deletes a pool file and returns the removed size.
func (p *Pool) Remove(path string) (int64, error) {
	return p.RemoveIfUnreferenced(path, nil)
}

// RemoveIfUnreferenced deletes path only when refs does not report it as used.
func (p *Pool) RemoveIfUnreferenced(path string, refs ReferenceChecker) (int64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if refs != nil {
		referenced, err := refs.IsReferenced(path)
		if err != nil {
			return 0, err
		}
		if referenced {
			return 0, fmt.Errorf("refusing to remove referenced package file: %s", path)
		}
	}

	fullPath, err := p.fullPath(path)
	if err != nil {
		return 0, err
	}
	info, err := os.Stat(fullPath)
	if err != nil {
		return 0, err
	}
	if err := os.Remove(fullPath); err != nil {
		return 0, err
	}

	return info.Size(), nil
}
