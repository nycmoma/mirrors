package pool

import "os"

// Verify checks whether path exists in the pool and matches expected checksums.
func (p *Pool) Verify(path string, expected Checksum) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	fullPath, err := p.fullPath(path)
	if err != nil {
		return false, err
	}
	info, err := os.Stat(fullPath)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, nil
	}

	if err := p.verifyFullPath(fullPath, normalizeExpected(expected)); err != nil {
		return false, err
	}
	return true, nil
}

func (p *Pool) verifyFullPath(fullPath string, expected Checksum) error {
	actual, err := checksumsForFile(fullPath)
	if err != nil {
		return err
	}
	return verifyExpected(fullPath, actual, expected)
}
