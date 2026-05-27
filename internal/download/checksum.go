package download

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"fmt"
	"hash"
	"io"
	"strings"
)

// Checksum contains expected or actual file size and hashes.
type Checksum struct {
	Size   int64
	MD5    string
	SHA1   string
	SHA256 string
	SHA512 string
}

type checksumWriter struct {
	sum    Checksum
	hashes []hash.Hash
}

func newChecksumWriter() *checksumWriter {
	return &checksumWriter{
		hashes: []hash.Hash{md5.New(), sha1.New(), sha256.New(), sha512.New()},
	}
}

func (w *checksumWriter) Write(p []byte) (int, error) {
	w.sum.Size += int64(len(p))
	for _, h := range w.hashes {
		_, _ = h.Write(p)
	}
	return len(p), nil
}

func (w *checksumWriter) Sum() Checksum {
	w.sum.MD5 = fmt.Sprintf("%x", w.hashes[0].Sum(nil))
	w.sum.SHA1 = fmt.Sprintf("%x", w.hashes[1].Sum(nil))
	w.sum.SHA256 = fmt.Sprintf("%x", w.hashes[2].Sum(nil))
	w.sum.SHA512 = fmt.Sprintf("%x", w.hashes[3].Sum(nil))
	return w.sum
}

func verifyChecksum(url string, actual Checksum, expected *Checksum) error {
	if expected == nil {
		return nil
	}

	if actual.Size != expected.Size {
		return fmt.Errorf("%s: size mismatch %d != %d", url, actual.Size, expected.Size)
	}
	if expected.MD5 != "" && !sameHash(actual.MD5, expected.MD5) {
		return fmt.Errorf("%s: md5 mismatch %q != %q", url, actual.MD5, expected.MD5)
	}
	if expected.SHA1 != "" && !sameHash(actual.SHA1, expected.SHA1) {
		return fmt.Errorf("%s: sha1 mismatch %q != %q", url, actual.SHA1, expected.SHA1)
	}
	if expected.SHA256 != "" && !sameHash(actual.SHA256, expected.SHA256) {
		return fmt.Errorf("%s: sha256 mismatch %q != %q", url, actual.SHA256, expected.SHA256)
	}
	if expected.SHA512 != "" && !sameHash(actual.SHA512, expected.SHA512) {
		return fmt.Errorf("%s: sha512 mismatch %q != %q", url, actual.SHA512, expected.SHA512)
	}

	return nil
}

func sameHash(left, right string) bool {
	return strings.EqualFold(strings.TrimSpace(left), strings.TrimSpace(right))
}

var _ io.Writer = (*checksumWriter)(nil)
