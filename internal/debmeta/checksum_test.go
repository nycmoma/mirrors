package debmeta

import "testing"

func TestParseChecksumListPreservesAlgorithmAndValue(t *testing.T) {
	checksums, err := ParseChecksumList(ChecksumSHA256, `
abcdef 123 main/binary-amd64/Packages.xz
012345 456 main/binary-amd64/Packages.gz
`)
	if err != nil {
		t.Fatalf("ParseChecksumList returned error: %v", err)
	}
	if len(checksums) != 2 {
		t.Fatalf("expected 2 checksums, got %d", len(checksums))
	}
	if checksums[0].Algorithm != ChecksumSHA256 || checksums[0].Value != "abcdef" || checksums[0].Size != 123 {
		t.Fatalf("unexpected first checksum: %#v", checksums[0])
	}
}

func TestParseChecksumListRejectsMalformedLine(t *testing.T) {
	if _, err := ParseChecksumList(ChecksumSHA1, "abc only-two-fields"); err == nil {
		t.Fatalf("expected malformed checksum error")
	}
}
