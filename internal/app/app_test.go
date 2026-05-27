package app

import (
	"bytes"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mirrors/internal/cli"
	"mirrors/internal/config"

	_ "github.com/mattn/go-sqlite3"
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

func TestNotImplementedReportsPlannedPhase(t *testing.T) {
	err := notImplemented("create")
	if err == nil {
		t.Fatal("expected not implemented error")
	}

	want := `action "create" will be implemented in Phase 7: Mirror Service.`
	if err.Error() != want {
		t.Fatalf("expected %q, got %q", want, err.Error())
	}
}

func TestRunConfigShowRejectsAmbiguousIdentity(t *testing.T) {
	err := runConfig(cli.Command{
		Name:       "config",
		Subcommand: "show",
		ConfigPath: writeTempConfig(t),
		NameRef:    "ubuntu",
	})
	if err == nil {
		t.Fatal("expected ambiguous identity error")
	}
	if !strings.Contains(err.Error(), "either --config or --name") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunConfigShowByPath(t *testing.T) {
	output, err := captureStdout(func() error {
		return runConfig(cli.Command{
			Name:       "config",
			Subcommand: "show",
			ConfigPath: writeTempConfig(t),
		})
	})
	if err != nil {
		t.Fatalf("runConfig returned error: %v", err)
	}
	for _, want := range []string{
		"name = ubuntu",
		"url = http://us.archive.ubuntu.com/ubuntu/",
		"dist = trusty, xenial, bionic, focal, jammy",
		"release = default, security, updates",
		"merge = no",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
}

func TestRunConfigShowByName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	createMirrorDB(t, config.DBPathForHome(home, "ubuntu"))

	output, err := captureStdout(func() error {
		return runConfig(cli.Command{
			Name:       "config",
			Subcommand: "show",
			NameRef:    "ubuntu",
		})
	})
	if err != nil {
		t.Fatalf("runConfig returned error: %v", err)
	}
	for _, want := range []string{
		"name = ubuntu",
		"dist = focal, jammy",
		"release = default, updates",
		"merge = 2",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
}

func writeTempConfig(t *testing.T) string {
	t.Helper()
	path := t.TempDir() + "/mirror.conf"
	content := `[mirror]
name = ubuntu
url = http://us.archive.ubuntu.com/ubuntu/
dist = trusty, xenial, bionic, focal, jammy
release = default, security, updates
origin = aptly_default
label = aptly_default
arch = amd64
components = main, restricted, multiverse, universe
path = preprod
server = http://dlt-ubmirror.datto.com
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	return path
}

func createMirrorDB(t *testing.T, dbPath string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer func() {
		_ = db.Close()
	}()

	_, err = db.Exec(`
CREATE TABLE mirror (
	name TEXT NOT NULL,
	url TEXT NOT NULL,
	dist TEXT NOT NULL,
	release TEXT NOT NULL,
	origin TEXT NOT NULL,
	label TEXT NOT NULL,
	arch TEXT NOT NULL,
	components TEXT NOT NULL,
	path TEXT NOT NULL,
	merge TEXT NOT NULL,
	server TEXT NOT NULL
);
INSERT INTO mirror (
	name, url, dist, release, origin, label, arch, components, path, merge, server
) VALUES (
	'ubuntu',
	'http://us.archive.ubuntu.com/ubuntu/',
	'focal, jammy',
	'default, updates',
	'Ubuntu',
	'Ubuntu',
	'amd64',
	'main, restricted',
	'preprod',
	'2',
	'http://mirror.example.test'
);
`)
	if err != nil {
		t.Fatalf("Exec returned error: %v", err)
	}
}

func captureStdout(fn func() error) (string, error) {
	old := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		return "", err
	}
	os.Stdout = writer

	runErr := fn()

	_ = writer.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, copyErr := io.Copy(&buf, reader)
	_ = reader.Close()
	if runErr != nil {
		return buf.String(), runErr
	}
	return buf.String(), copyErr
}
