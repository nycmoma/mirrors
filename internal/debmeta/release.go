package debmeta

import (
	"bytes"
	"fmt"
	"io"
	"strings"
)

// Release contains the top-level fields needed from Release/InRelease files.
type Release struct {
	Origin        string
	Label         string
	Suite         string
	Codename      string
	Version       string
	Date          string
	Description   string
	Architectures []string
	Components    []string
	Checksums     []Checksum
	Fields        Stanza
}

// ParseRelease parses a Debian Release payload.
func ParseRelease(r io.Reader) (*Release, error) {
	reader := newReleaseStanzaReader(r)
	stanza, err := reader.ReadStanza()
	if err != nil {
		return nil, err
	}
	if stanza == nil {
		return nil, fmt.Errorf("empty Release metadata")
	}

	release := &Release{
		Origin:        stanza["Origin"],
		Label:         stanza["Label"],
		Suite:         stanza["Suite"],
		Codename:      stanza["Codename"],
		Version:       stanza["Version"],
		Date:          stanza["Date"],
		Description:   strings.TrimSpace(stanza["Description"]),
		Architectures: strings.Fields(stanza["Architectures"]),
		Components:    strings.Fields(stanza["Components"]),
		Fields:        stanza,
	}

	for _, algorithm := range []string{ChecksumMD5, ChecksumSHA1, ChecksumSHA256, ChecksumSHA512} {
		parsed, err := ParseChecksumList(algorithm, stanza[algorithm])
		if err != nil {
			return nil, err
		}
		release.Checksums = append(release.Checksums, parsed...)
	}

	return release, nil
}

// ParseInRelease extracts and parses the clear-signed Release payload.
func ParseInRelease(r io.Reader) (*Release, []byte, error) {
	payload, err := ExtractInReleasePayload(r)
	if err != nil {
		return nil, nil, err
	}

	release, err := ParseRelease(bytes.NewReader(payload))
	if err != nil {
		return nil, nil, err
	}
	return release, payload, nil
}

// ExtractInReleasePayload extracts the cleartext section from an InRelease file.
func ExtractInReleasePayload(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}

	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "-----BEGIN PGP SIGNED MESSAGE-----" {
		return nil, fmt.Errorf("InRelease missing PGP signed message header")
	}

	i := 1
	for i < len(lines) && lines[i] != "" {
		i++
	}
	if i == len(lines) {
		return nil, fmt.Errorf("InRelease missing cleartext payload")
	}
	i++

	var payload bytes.Buffer
	for ; i < len(lines); i++ {
		line := lines[i]
		if line == "-----BEGIN PGP SIGNATURE-----" {
			return payload.Bytes(), nil
		}
		if strings.HasPrefix(line, "- ") {
			line = line[2:]
		}
		payload.WriteString(line)
		payload.WriteByte('\n')
	}

	return nil, fmt.Errorf("InRelease missing PGP signature block")
}
