package debmeta

import (
	"errors"
	"strings"
	"testing"
)

func TestParseStanzasHandlesMultilineFields(t *testing.T) {
	input := `Package: demo
Version: 1.0
Architecture: amd64
Description: short text
 long text
 .
 final line
Filename: pool/main/d/demo/demo_1.0_amd64.deb
Size: 42

Package: second
Version: 2.0
Architecture: all
Filename: pool/main/s/second/second_2.0_all.deb
Size: 11
`

	stanzas, err := ParseStanzas(strings.NewReader(input))
	if err != nil {
		t.Fatalf("ParseStanzas returned error: %v", err)
	}
	if len(stanzas) != 2 {
		t.Fatalf("expected 2 stanzas, got %d", len(stanzas))
	}
	if got := stanzas[0]["Description"]; got != "short text\n long text\n .\n final line\n" {
		t.Fatalf("unexpected description:\n%q", got)
	}
	if got := stanzas[1]["Package"]; got != "second" {
		t.Fatalf("unexpected second package: %q", got)
	}
}

func TestParseStanzasRejectsMalformedInput(t *testing.T) {
	_, err := ParseStanzas(strings.NewReader("not a field\n"))
	if !errors.Is(err, ErrMalformedStanza) {
		t.Fatalf("expected ErrMalformedStanza, got %v", err)
	}
}
