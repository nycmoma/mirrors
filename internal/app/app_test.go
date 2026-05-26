package app

import (
	"strings"
	"testing"

	"mirrors/internal/cli"
)

func TestRunMirrorCommandRejectsAmbiguousIdentity(t *testing.T) {
	err := runMirrorCommand(cli.Command{
		Name:       "info",
		ConfigPath: "mirror.conf",
		NameRef:    "ubuntu-xenial",
	})
	if err == nil {
		t.Fatal("expected ambiguous identity error")
	}
	if !strings.Contains(err.Error(), "either --config or --name") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunMirrorCommandRequiresIdentity(t *testing.T) {
	err := runMirrorCommand(cli.Command{Name: "info"})
	if err == nil {
		t.Fatal("expected missing identity error")
	}
	if !strings.Contains(err.Error(), "missing mirror identity") {
		t.Fatalf("unexpected error: %v", err)
	}
}
