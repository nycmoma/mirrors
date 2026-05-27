package pool

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPathForMatchesAptlyStyleLayout(t *testing.T) {
	checksum := Checksum{
		SHA256: "476e0cdac6bc757dd2b78bacc1325323b09c45ecb41d4562deec2a1c7c148405",
	}

	path, err := PathFor("my-package_1.2.3_all.deb", checksum)
	if err != nil {
		t.Fatalf("PathFor returned error: %v", err)
	}

	want := filepath.Join("47", "6e", "0cdac6bc757dd2b78bacc1325323_my-package_1.2.3_all.deb")
	if path != want {
		t.Fatalf("expected %q, got %q", want, path)
	}
}

func TestLegacyPathFor(t *testing.T) {
	path, err := LegacyPathFor("a/b/package.deb", Checksum{MD5: "0035abcdef"})
	if err != nil {
		t.Fatalf("LegacyPathFor returned error: %v", err)
	}

	want := filepath.Join("00", "35", "package.deb")
	if path != want {
		t.Fatalf("expected %q, got %q", want, path)
	}
}

func TestImportNewPackage(t *testing.T) {
	pool := newTestPool(t)
	source := writeTempPackage(t, "package content")
	expected := checksumForString(t, "package content")

	result, err := pool.Import(source, "demo_1.0_amd64.deb", expected)
	if err != nil {
		t.Fatalf("Import returned error: %v", err)
	}
	if result.Duplicate {
		t.Fatalf("first import should not be duplicate")
	}
	if result.Checksum.SHA256 != expected.SHA256 {
		t.Fatalf("expected checksum to be preserved: %#v", result.Checksum)
	}
	if _, err := os.Stat(pool.FullPath(result.Path)); err != nil {
		t.Fatalf("imported file does not exist: %v", err)
	}

	list, err := pool.List()
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(list) != 1 || list[0] != filepath.ToSlash(result.Path) {
		t.Fatalf("unexpected file list: %#v", list)
	}
}

func TestImportDuplicatePackageIsIdempotent(t *testing.T) {
	pool := newTestPool(t)
	source := writeTempPackage(t, "same content")
	expected := checksumForString(t, "same content")

	first, err := pool.Import(source, "demo_1.0_amd64.deb", expected)
	if err != nil {
		t.Fatalf("first Import returned error: %v", err)
	}
	second, err := pool.Import(source, "demo_1.0_amd64.deb", expected)
	if err != nil {
		t.Fatalf("second Import returned error: %v", err)
	}

	if !second.Duplicate {
		t.Fatalf("second import should be duplicate")
	}
	if first.Path != second.Path {
		t.Fatalf("duplicate import path changed: %q != %q", first.Path, second.Path)
	}

	list, err := pool.List()
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected one pool file, got %#v", list)
	}
}

func TestImportRejectsChecksumMismatch(t *testing.T) {
	pool := newTestPool(t)
	source := writeTempPackage(t, "actual content")
	expected := checksumForString(t, "expected content")

	_, err := pool.Import(source, "demo_1.0_amd64.deb", expected)
	if err == nil {
		t.Fatalf("expected checksum mismatch")
	}
	if !strings.Contains(err.Error(), "size mismatch") && !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("unexpected mismatch error: %v", err)
	}
}

func TestVerifyExistingPackage(t *testing.T) {
	pool := newTestPool(t)
	source := writeTempPackage(t, "verify content")
	expected := checksumForString(t, "verify content")

	result, err := pool.Import(source, "demo_1.0_amd64.deb", expected)
	if err != nil {
		t.Fatalf("Import returned error: %v", err)
	}

	ok, err := pool.Verify(result.Path, expected)
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	if !ok {
		t.Fatalf("expected Verify to find imported package")
	}

	wrong := expected
	wrong.SHA256 = strings.Repeat("0", 64)
	ok, err = pool.Verify(result.Path, wrong)
	if err == nil {
		t.Fatalf("expected Verify checksum mismatch")
	}
	if ok {
		t.Fatalf("Verify should not succeed with wrong checksum")
	}
}

func TestDiskUsageAndRemove(t *testing.T) {
	pool := newTestPool(t)
	source := writeTempPackage(t, "12345")
	expected := checksumForString(t, "12345")

	result, err := pool.Import(source, "demo_1.0_amd64.deb", expected)
	if err != nil {
		t.Fatalf("Import returned error: %v", err)
	}

	usage, err := pool.DiskUsage()
	if err != nil {
		t.Fatalf("DiskUsage returned error: %v", err)
	}
	if usage != 5 {
		t.Fatalf("expected disk usage 5, got %d", usage)
	}

	removed, err := pool.Remove(result.Path)
	if err != nil {
		t.Fatalf("Remove returned error: %v", err)
	}
	if removed != 5 {
		t.Fatalf("expected removed size 5, got %d", removed)
	}
}

func TestRemoveIfUnreferencedRefusesReferencedPackage(t *testing.T) {
	pool := newTestPool(t)
	source := writeTempPackage(t, "referenced")
	expected := checksumForString(t, "referenced")

	result, err := pool.Import(source, "demo_1.0_amd64.deb", expected)
	if err != nil {
		t.Fatalf("Import returned error: %v", err)
	}

	_, err = pool.RemoveIfUnreferenced(result.Path, referenceMap{result.Path: true})
	if err == nil {
		t.Fatalf("expected referenced package removal to fail")
	}

	ok, err := pool.Verify(result.Path, expected)
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	if !ok {
		t.Fatalf("referenced package should still exist")
	}
}

func TestRemoveIfUnreferencedRemovesUnreferencedPackage(t *testing.T) {
	pool := newTestPool(t)
	source := writeTempPackage(t, "unreferenced")
	expected := checksumForString(t, "unreferenced")

	result, err := pool.Import(source, "demo_1.0_amd64.deb", expected)
	if err != nil {
		t.Fatalf("Import returned error: %v", err)
	}

	removed, err := pool.RemoveIfUnreferenced(result.Path, referenceMap{})
	if err != nil {
		t.Fatalf("RemoveIfUnreferenced returned error: %v", err)
	}
	if removed != int64(len("unreferenced")) {
		t.Fatalf("expected removed size %d, got %d", len("unreferenced"), removed)
	}

	ok, err := pool.Verify(result.Path, expected)
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	if ok {
		t.Fatalf("unreferenced package should have been removed")
	}
}

func TestRemoveRejectsRootPath(t *testing.T) {
	pool := newTestPool(t)
	if _, err := pool.Remove(""); err == nil {
		t.Fatalf("expected empty pool path to be rejected")
	}
	if _, err := pool.Remove("."); err == nil {
		t.Fatalf("expected root pool path to be rejected")
	}
	if _, err := os.Stat(pool.Root()); err != nil {
		t.Fatalf("pool root should still exist or be addressable: %v", err)
	}
}

func newTestPool(t *testing.T) *Pool {
	t.Helper()
	pool, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if err := os.MkdirAll(pool.Root(), 0755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	return pool
}

func writeTempPackage(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "package.deb")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	return path
}

func checksumForString(t *testing.T, content string) Checksum {
	t.Helper()
	path := writeTempPackage(t, content)
	checksum, err := checksumsForFile(path)
	if err != nil {
		t.Fatalf("checksumsForFile returned error: %v", err)
	}
	return checksum
}

type referenceMap map[string]bool

func (m referenceMap) IsReferenced(path string) (bool, error) {
	return m[path], nil
}
