package debmeta

import (
	"bufio"
	"errors"
	"io"
	"strings"
	"unicode"
)

// Stanza is one Debian control-file paragraph.
type Stanza map[string]string

const maxFieldSize = 2 * 1024 * 1024

var ErrMalformedStanza = errors.New("malformed Debian stanza")

// StanzaReader reads Debian control-file stanzas one at a time.
type StanzaReader struct {
	scanner   *bufio.Scanner
	isRelease bool
}

// NewStanzaReader creates a stanza reader for Packages-like metadata.
func NewStanzaReader(r io.Reader) *StanzaReader {
	return newStanzaReader(r, false)
}

func newReleaseStanzaReader(r io.Reader) *StanzaReader {
	return newStanzaReader(r, true)
}

func newStanzaReader(r io.Reader, isRelease bool) *StanzaReader {
	scanner := bufio.NewScanner(bufio.NewReaderSize(r, 32768))
	scanner.Buffer(nil, maxFieldSize)

	return &StanzaReader{
		scanner:   scanner,
		isRelease: isRelease,
	}
}

// ParseStanzas reads all stanzas from r.
func ParseStanzas(r io.Reader) ([]Stanza, error) {
	reader := NewStanzaReader(r)
	var stanzas []Stanza

	for {
		stanza, err := reader.ReadStanza()
		if err != nil {
			return nil, err
		}
		if stanza == nil {
			return stanzas, nil
		}
		stanzas = append(stanzas, stanza)
	}
}

// ReadStanza returns the next stanza, or nil when the stream is exhausted.
func (r *StanzaReader) ReadStanza() (Stanza, error) {
	stanza := make(Stanza, 32)
	lastField := ""
	lastMultiline := false

	for r.scanner.Scan() {
		line := r.scanner.Text()

		if line == "" {
			if len(stanza) > 0 {
				return stanza, nil
			}
			continue
		}

		if line[0] == ' ' || line[0] == '\t' {
			if lastField == "" {
				return nil, ErrMalformedStanza
			}
			if lastMultiline {
				stanza[lastField] += line + "\n"
			} else {
				stanza[lastField] += " " + strings.TrimSpace(line)
			}
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 || parts[0] == "" {
			return nil, ErrMalformedStanza
		}

		lastField = canonicalField(parts[0])
		lastMultiline = isMultilineField(lastField, r.isRelease)
		if lastMultiline {
			stanza[lastField] = strings.TrimPrefix(parts[1], " ")
			if stanza[lastField] != "" {
				stanza[lastField] += "\n"
			}
		} else {
			stanza[lastField] = strings.TrimSpace(parts[1])
		}
	}

	if err := r.scanner.Err(); err != nil {
		return nil, err
	}
	if len(stanza) > 0 {
		return stanza, nil
	}
	return nil, nil
}

func isMultilineField(field string, isRelease bool) bool {
	switch field {
	case "Description", "Files", "Changes", "Checksums-Sha1", "Checksums-Sha256", "Checksums-Sha512", "Package-List":
		return true
	case "MD5Sum", "SHA1", "SHA256", "SHA512":
		return isRelease
	}
	return false
}

func canonicalField(field string) string {
	upper := strings.ToUpper(field)
	switch upper {
	case "SHA1", "SHA256", "SHA512":
		return upper
	case "MD5SUM":
		return "MD5Sum"
	case "NOTAUTOMATIC":
		return "NotAutomatic"
	case "BUTAUTOMATICUPGRADES":
		return "ButAutomaticUpgrades"
	}

	startOfWord := true
	return strings.Map(func(r rune) rune {
		if startOfWord {
			startOfWord = false
			return unicode.ToUpper(r)
		}
		if r == '-' {
			startOfWord = true
		}
		return unicode.ToLower(r)
	}, field)
}
