package debmeta

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const releaseFixture = `Origin: Ubuntu
Label: Ubuntu
Suite: jammy
Codename: jammy
Date: Thu, 21 Apr 2022 17:16:08 UTC
Architectures: amd64 i386
Components: main restricted
Description: Ubuntu Jammy
MD5Sum:
 abc123 10 main/binary-amd64/Packages.gz
SHA1:
 def456 10 main/binary-amd64/Packages.gz
SHA256:
 789abc 10 main/binary-amd64/Packages.gz
SHA512:
 fedcba 10 main/binary-amd64/Packages.gz
`

func TestParseRelease(t *testing.T) {
	release, err := ParseRelease(strings.NewReader(releaseFixture))
	if err != nil {
		t.Fatalf("ParseRelease returned error: %v", err)
	}
	if release.Origin != "Ubuntu" || release.Label != "Ubuntu" || release.Suite != "jammy" {
		t.Fatalf("unexpected release fields: %#v", release)
	}
	if len(release.Architectures) != 2 || release.Architectures[0] != "amd64" {
		t.Fatalf("unexpected architectures: %#v", release.Architectures)
	}
	if len(release.Components) != 2 || release.Components[1] != "restricted" {
		t.Fatalf("unexpected components: %#v", release.Components)
	}
	if len(release.Checksums) != 4 {
		t.Fatalf("expected 4 checksums, got %d", len(release.Checksums))
	}
}

func TestParseInReleaseExtractsPayload(t *testing.T) {
	input := `-----BEGIN PGP SIGNED MESSAGE-----
Hash: SHA512

Origin: Example
Suite: stable
- Label: dash escaped
Architectures: amd64
Components: main
SHA256:
 abc 1 main/binary-amd64/Packages.xz
-----BEGIN PGP SIGNATURE-----

signature
-----END PGP SIGNATURE-----
`

	release, payload, err := ParseInRelease(strings.NewReader(input))
	if err != nil {
		t.Fatalf("ParseInRelease returned error: %v", err)
	}
	if release.Label != "dash escaped" {
		t.Fatalf("dash escaping was not removed: %#v", release)
	}
	if !strings.Contains(string(payload), "Label: dash escaped") {
		t.Fatalf("payload does not contain extracted label:\n%s", payload)
	}
}

func TestParseLocalUbuntuReleaseWhenAvailable(t *testing.T) {
	path := filepath.Join("..", "..", "tests", "metadata", "ubuntu", "dists", "jammy", "Release")
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		t.Skipf("local Ubuntu fixture not available: %s", path)
	}
	if err != nil {
		t.Fatalf("open local Ubuntu Release: %v", err)
	}
	defer file.Close()

	release, err := ParseRelease(file)
	if err != nil {
		t.Fatalf("ParseRelease local Ubuntu fixture: %v", err)
	}
	if release.Origin == "" || release.Label == "" || len(release.Components) == 0 || len(release.Checksums) == 0 {
		t.Fatalf("local Ubuntu Release parsed without expected fields: %#v", release)
	}
}
