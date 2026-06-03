package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNopLoggerDoesNotCreateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "mirrors.log")
	logger := Nop()
	logger.Infof("hidden")
	if err := logger.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("nop logger should not create file, stat err=%v", err)
	}
}

func TestFileLoggerFiltersByLevel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mirrors.log")
	logger, err := OpenFile(path, Warn)
	if err != nil {
		t.Fatalf("OpenFile returned error: %v", err)
	}
	logger.Debugf("debug hidden")
	logger.Infof("info hidden")
	logger.Warnf("warn visible")
	logger.Errorf("error visible")
	if err := logger.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	text := string(data)
	for _, hidden := range []string{"debug hidden", "info hidden"} {
		if strings.Contains(text, hidden) {
			t.Fatalf("log contains filtered message %q:\n%s", hidden, text)
		}
	}
	for _, visible := range []string{"warn visible", "error visible"} {
		if !strings.Contains(text, visible) {
			t.Fatalf("log missing %q:\n%s", visible, text)
		}
	}
}

func TestOpenFileCreatesParentDirectories(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "logs", "mirrors.log")
	logger, err := OpenFile(path, Debug)
	if err != nil {
		t.Fatalf("OpenFile returned error: %v", err)
	}
	logger.Debugf("created")
	if err := logger.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected log file: %v", err)
	}
}

func TestParseLevelRejectsInvalidValue(t *testing.T) {
	if _, err := ParseLevel("verbose"); err == nil {
		t.Fatal("expected invalid level error")
	}
}
