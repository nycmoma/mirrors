package debmeta

import (
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ulikunitz/xz"
)

const packagesFixture = `Package: demo
Version: 1:1.2.3-1ubuntu1
Architecture: amd64
Source: demo-src (1.2.3-1)
Filename: pool/main/d/demo/demo_1.2.3-1ubuntu1_amd64.deb
Size: 12345
MD5sum: md5value
SHA1: sha1value
SHA256: sha256value
SHA512: sha512value
Description: short text
 long text

Package: optional
Version: 0.1
Architecture: all
Filename: pool/universe/o/optional/optional_0.1_all.deb
Size: 99
`

func TestParsePackagesExtractsRequiredFields(t *testing.T) {
	packages, err := ParsePackages(strings.NewReader(packagesFixture))
	if err != nil {
		t.Fatalf("ParsePackages returned error: %v", err)
	}
	if len(packages) != 2 {
		t.Fatalf("expected 2 packages, got %d", len(packages))
	}

	first := packages[0]
	if first.Name != "demo" || first.Version != "1:1.2.3-1ubuntu1" || first.Architecture != "amd64" {
		t.Fatalf("unexpected package identity: %#v", first)
	}
	if first.Filename != "pool/main/d/demo/demo_1.2.3-1ubuntu1_amd64.deb" || first.Size != 12345 {
		t.Fatalf("unexpected file metadata: %#v", first)
	}
	if first.Component != "main" || first.Source != "demo-src (1.2.3-1)" {
		t.Fatalf("unexpected component/source: %#v", first)
	}
	if first.Checksums.MD5 != "md5value" || first.Checksums.SHA256 != "sha256value" {
		t.Fatalf("unexpected checksums: %#v", first.Checksums)
	}

	second := packages[1]
	if second.Checksums != (FileChecksums{}) {
		t.Fatalf("optional checksums should be empty when absent: %#v", second.Checksums)
	}
}

func TestParsePackagesRejectsMissingRequiredFields(t *testing.T) {
	_, err := ParsePackages(strings.NewReader(`Package: broken
Version: 1.0
Architecture: amd64
Size: 12
`))
	if err == nil {
		t.Fatalf("expected missing required field error")
	}
}

func TestParsePackagesAllowsMissingSize(t *testing.T) {
	packages, err := ParsePackages(strings.NewReader(`Package: demo
Version: 1.0
Architecture: amd64
Filename: pool/main/d/demo/demo_1.0_amd64.deb
SHA256: abc

`))
	if err != nil {
		t.Fatalf("ParsePackages returned error: %v", err)
	}
	if len(packages) != 1 || packages[0].Size != -1 {
		t.Fatalf("expected one package with unknown size sentinel, got %#v", packages)
	}
}

func TestParsePackagesFileSupportsGzipAndXZ(t *testing.T) {
	dir := t.TempDir()

	gzipPath := filepath.Join(dir, "Packages.gz")
	writeGzipFixture(t, gzipPath, packagesFixture)
	gzipPackages, err := ParsePackagesFile(gzipPath)
	if err != nil {
		t.Fatalf("ParsePackagesFile gzip returned error: %v", err)
	}
	if len(gzipPackages) != 2 {
		t.Fatalf("expected 2 gzip packages, got %d", len(gzipPackages))
	}

	xzPath := filepath.Join(dir, "Packages.xz")
	writeXZFixture(t, xzPath, packagesFixture)
	xzPackages, err := ParsePackagesFile(xzPath)
	if err != nil {
		t.Fatalf("ParsePackagesFile xz returned error: %v", err)
	}
	if len(xzPackages) != 2 {
		t.Fatalf("expected 2 xz packages, got %d", len(xzPackages))
	}
}

func TestParseLocalUbuntuPackagesWhenAvailable(t *testing.T) {
	path := filepath.Join("..", "..", "tests", "metadata", "ubuntu", "dists", "jammy", "main", "binary-amd64", "Packages.xz")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Skipf("local Ubuntu fixture not available: %s", path)
	} else if err != nil {
		t.Fatalf("stat local Ubuntu Packages.xz: %v", err)
	}

	packages, err := ParsePackagesFile(path)
	if err != nil {
		t.Fatalf("ParsePackagesFile local Ubuntu fixture: %v", err)
	}
	if len(packages) == 0 {
		t.Fatalf("local Ubuntu Packages.xz parsed no packages")
	}
	first := packages[0]
	if first.Name == "" || first.Version == "" || first.Architecture == "" || first.Filename == "" || first.Size == 0 {
		t.Fatalf("first local Ubuntu package missing expected fields: %#v", first)
	}
}

func writeGzipFixture(t *testing.T, path, payload string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create gzip fixture: %v", err)
	}
	writer := gzip.NewWriter(file)
	if _, err := writer.Write([]byte(payload)); err != nil {
		t.Fatalf("write gzip fixture: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close gzip fixture: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close gzip file: %v", err)
	}
}

func writeXZFixture(t *testing.T, path, payload string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create xz fixture: %v", err)
	}
	writer, err := xz.NewWriter(file)
	if err != nil {
		t.Fatalf("create xz writer: %v", err)
	}
	if _, err := writer.Write([]byte(payload)); err != nil {
		t.Fatalf("write xz fixture: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close xz fixture: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close xz file: %v", err)
	}
}
