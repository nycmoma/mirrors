package signing

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"mirrors/internal/config"
)

func TestSignDisabledRemovesStaleSignatures(t *testing.T) {
	root := writeRepo(t)
	inRelease := filepath.Join(root, "dists", "focal", "InRelease")
	releaseGPG := filepath.Join(root, "dists", "focal", "Release.gpg")
	writeFile(t, inRelease, "old")
	writeFile(t, releaseGPG, "old")

	runner := &fakeRunner{}
	service := NewService(WithRunner(runner))
	result, err := service.Sign(context.Background(), config.Mirror{
		Signing: config.Signing{Disabled: true},
	}, Repository{Path: root, Suite: "focal"})
	if err != nil {
		t.Fatalf("Sign returned error: %v", err)
	}
	if result.Enabled {
		t.Fatalf("expected signing disabled result: %#v", result)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("disabled signing should not run gpg: %#v", runner.calls)
	}
	if _, err := os.Stat(inRelease); !os.IsNotExist(err) {
		t.Fatalf("InRelease should be removed, stat error: %v", err)
	}
	if _, err := os.Stat(releaseGPG); !os.IsNotExist(err) {
		t.Fatalf("Release.gpg should be removed, stat error: %v", err)
	}
}

func TestSignCreatesInReleaseAndReleaseGPGWithConfigValues(t *testing.T) {
	root := writeRepo(t)
	passphrase := filepath.Join(t.TempDir(), "passphrase")
	writeFile(t, passphrase, "from-file\n")
	runner := &fakeRunner{}
	service := NewService(WithRunner(runner), WithGetenv(func(name string) string {
		switch name {
		case "GPG_KEY":
			return "env-key"
		case "GPG_PASSPHRASE":
			return "env-pass"
		default:
			return ""
		}
	}))

	result, err := service.Sign(context.Background(), config.Mirror{
		Signing: config.Signing{
			GPGHome:           "/tmp/gnupg",
			GPGKey:            "config-key",
			GPGPassphraseFile: passphrase,
		},
	}, Repository{Path: root, Suite: "focal"})
	if err != nil {
		t.Fatalf("Sign returned error: %v", err)
	}
	if !result.Enabled || result.InRelease == "" || result.ReleaseGPG == "" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("expected two gpg calls, got %#v", runner.calls)
	}
	for _, call := range runner.calls {
		joined := strings.Join(call.args, " ")
		for _, want := range []string{"--homedir /tmp/gnupg", "--local-user config-key", "--pinentry-mode loopback", "--passphrase-fd 0"} {
			if !strings.Contains(joined, want) {
				t.Fatalf("gpg call missing %q: %s", want, joined)
			}
		}
		if string(call.stdin) != "from-file" {
			t.Fatalf("expected file passphrase, got %q", call.stdin)
		}
	}
}

func TestSignUsesEnvironmentWhenConfigMissing(t *testing.T) {
	root := writeRepo(t)
	runner := &fakeRunner{}
	service := NewService(WithRunner(runner), WithGetenv(func(name string) string {
		switch name {
		case "GPG_KEY":
			return "env-key"
		case "GPG_PASSPHRASE":
			return "env-pass"
		default:
			return ""
		}
	}))
	if _, err := service.Sign(context.Background(), config.Mirror{}, Repository{Path: root, Suite: "focal"}); err != nil {
		t.Fatalf("Sign returned error: %v", err)
	}
	joined := strings.Join(runner.calls[0].args, " ")
	if !strings.Contains(joined, "--local-user env-key") {
		t.Fatalf("expected env key in gpg args: %s", joined)
	}
	if string(runner.calls[0].stdin) != "env-pass" {
		t.Fatalf("expected env passphrase, got %q", runner.calls[0].stdin)
	}
}

func TestSigningFailureIncludesInstructionsAndRemovesStale(t *testing.T) {
	root := writeRepo(t)
	inRelease := filepath.Join(root, "dists", "focal", "InRelease")
	writeFile(t, inRelease, "old")
	runner := &fakeRunner{err: errors.New("forced")}
	service := NewService(WithRunner(runner))

	_, err := service.Sign(context.Background(), config.Mirror{}, Repository{Path: root, Suite: "focal"})
	if err == nil {
		t.Fatal("expected signing error")
	}
	if !strings.Contains(err.Error(), "gpg_key") || !strings.Contains(err.Error(), "sign = no") {
		t.Fatalf("error missing setup instructions: %v", err)
	}
	if _, statErr := os.Stat(inRelease); !os.IsNotExist(statErr) {
		t.Fatalf("stale InRelease should be removed before signing, stat error: %v", statErr)
	}
}

func TestRealGPGIfAvailable(t *testing.T) {
	if _, err := exec.LookPath("gpg"); err != nil {
		t.Skip("gpg unavailable; install gpg and create a signing key to run integration signing test")
	}
	if output, err := exec.Command("gpg", "--batch", "--list-secret-keys", "--with-colons").CombinedOutput(); err != nil || !strings.Contains(string(output), "sec:") {
		t.Skip("no usable default gpg secret key; create one with: gpg --quick-generate-key 'Mirror Signing <mirror@example.test>'")
	}

	root := writeRepo(t)
	service := NewService()
	result, err := service.Sign(context.Background(), config.Mirror{}, Repository{Path: root, Suite: "focal"})
	if err != nil {
		t.Fatalf("real gpg Sign returned error: %v", err)
	}
	if _, err := os.Stat(result.InRelease); err != nil {
		t.Fatalf("InRelease missing: %v", err)
	}
	if _, err := os.Stat(result.ReleaseGPG); err != nil {
		t.Fatalf("Release.gpg missing: %v", err)
	}
}

type fakeRunner struct {
	calls []fakeCall
	err   error
}

type fakeCall struct {
	command string
	args    []string
	stdin   []byte
}

func (r *fakeRunner) LookPath(file string) (string, error) {
	return "/usr/bin/" + file, nil
}

func (r *fakeRunner) Run(_ context.Context, command string, args []string, stdin []byte) error {
	r.calls = append(r.calls, fakeCall{
		command: command,
		args:    append([]string(nil), args...),
		stdin:   append([]byte(nil), stdin...),
	})
	if r.err != nil {
		return r.err
	}
	output := ""
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--output" {
			output = args[i+1]
			break
		}
	}
	if output != "" {
		return os.WriteFile(output, []byte("signature"), 0644)
	}
	return nil
}

func writeRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "dists", "focal", "Release"), "Origin: Test\n")
	return root
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
}
