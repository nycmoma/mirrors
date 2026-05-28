package signing

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"mirrors/internal/config"
)

const setupInstructions = "configure signing with [mirror] gpg_key, gpg_home, gpg_passphrase, gpg_passphrase_file, environment variables GPG_KEY/GPG_PASSPHRASE, or create a usable default gpg key"

// Repository identifies one published suite to sign.
type Repository struct {
	Path  string
	Suite string
}

// Result summarizes signing output.
type Result struct {
	Enabled    bool
	InRelease  string
	ReleaseGPG string
}

// Runner runs a signing command.
type Runner interface {
	Run(ctx context.Context, command string, args []string, stdin []byte) error
	LookPath(file string) (string, error)
}

// Service signs published Release metadata.
type Service struct {
	runner Runner
	getenv func(string) string
}

// Option configures a Service.
type Option func(*Service)

// WithRunner sets the command runner.
func WithRunner(runner Runner) Option {
	return func(service *Service) {
		service.runner = runner
	}
}

// WithGetenv sets the environment lookup function.
func WithGetenv(getenv func(string) string) Option {
	return func(service *Service) {
		service.getenv = getenv
	}
}

// NewService creates a signing service.
func NewService(options ...Option) *Service {
	service := &Service{
		runner: commandRunner{},
		getenv: os.Getenv,
	}
	for _, option := range options {
		option(service)
	}
	return service
}

// Sign signs the Release file for repo unless signing is disabled.
func (s *Service) Sign(ctx context.Context, cfg config.Mirror, repo Repository) (Result, error) {
	resolved, err := s.resolve(cfg)
	if err != nil {
		return Result{}, err
	}
	inRelease := filepath.Join(repo.Path, "dists", repo.Suite, "InRelease")
	releaseGPG := filepath.Join(repo.Path, "dists", repo.Suite, "Release.gpg")
	if !resolved.Enabled {
		if err := removeIfExists(inRelease); err != nil {
			return Result{}, err
		}
		if err := removeIfExists(releaseGPG); err != nil {
			return Result{}, err
		}
		return Result{Enabled: false}, nil
	}

	release := filepath.Join(repo.Path, "dists", repo.Suite, "Release")
	if _, err := os.Stat(release); err != nil {
		return Result{}, wrapSigningError(fmt.Errorf("Release file is required before signing: %w", err))
	}
	if err := removeIfExists(inRelease); err != nil {
		return Result{}, err
	}
	if err := removeIfExists(releaseGPG); err != nil {
		return Result{}, err
	}
	if _, err := s.runner.LookPath("gpg"); err != nil {
		return Result{}, wrapSigningError(fmt.Errorf("gpg executable not found: %w", err))
	}

	if err := s.runGPG(ctx, resolved, []string{
		"--clearsign",
		"--digest-algo", "SHA256",
		"--output", inRelease,
		release,
	}); err != nil {
		return Result{}, wrapSigningError(fmt.Errorf("create InRelease: %w", err))
	}
	if err := s.runGPG(ctx, resolved, []string{
		"--detach-sign",
		"--armor",
		"--digest-algo", "SHA256",
		"--output", releaseGPG,
		release,
	}); err != nil {
		return Result{}, wrapSigningError(fmt.Errorf("create Release.gpg: %w", err))
	}

	return Result{Enabled: true, InRelease: inRelease, ReleaseGPG: releaseGPG}, nil
}

func (s *Service) runGPG(ctx context.Context, resolved resolvedConfig, args []string) error {
	base := []string{"--batch", "--yes"}
	if resolved.GPGHome != "" {
		base = append(base, "--homedir", resolved.GPGHome)
	}
	if resolved.GPGKey != "" {
		base = append(base, "--local-user", resolved.GPGKey)
	}
	var stdin []byte
	if resolved.Passphrase != "" {
		base = append(base, "--pinentry-mode", "loopback", "--passphrase-fd", "0")
		stdin = []byte(resolved.Passphrase)
	}
	return s.runner.Run(ctx, "gpg", append(base, args...), stdin)
}

type resolvedConfig struct {
	Enabled    bool
	GPGHome    string
	GPGKey     string
	Passphrase string
}

func (s *Service) resolve(cfg config.Mirror) (resolvedConfig, error) {
	if cfg.Signing.Disabled {
		return resolvedConfig{}, nil
	}
	result := resolvedConfig{
		Enabled: true,
		GPGHome: strings.TrimSpace(cfg.Signing.GPGHome),
		GPGKey:  strings.TrimSpace(cfg.Signing.GPGKey),
	}
	if result.GPGKey == "" {
		result.GPGKey = strings.TrimSpace(s.getenv("GPG_KEY"))
	}
	passphrase, err := s.resolvePassphrase(cfg)
	if err != nil {
		return resolvedConfig{}, err
	}
	result.Passphrase = passphrase
	return result, nil
}

func (s *Service) resolvePassphrase(cfg config.Mirror) (string, error) {
	if strings.TrimSpace(cfg.Signing.GPGPassphrase) != "" {
		return cfg.Signing.GPGPassphrase, nil
	}
	if strings.TrimSpace(cfg.Signing.GPGPassphraseFile) != "" {
		data, err := os.ReadFile(cfg.Signing.GPGPassphraseFile)
		if err != nil {
			return "", wrapSigningError(fmt.Errorf("read gpg_passphrase_file: %w", err))
		}
		return strings.TrimRight(string(data), "\r\n"), nil
	}
	return strings.TrimSpace(s.getenv("GPG_PASSPHRASE")), nil
}

// Error is returned when signing setup or gpg execution fails.
type Error struct {
	Err error
}

func (e Error) Error() string {
	return fmt.Sprintf("%v. Signing is enabled by default; %s, or set sign = no to publish unsigned output.", e.Err, setupInstructions)
}

func (e Error) Unwrap() error {
	return e.Err
}

func wrapSigningError(err error) error {
	if err == nil {
		return nil
	}
	var signingErr Error
	if errors.As(err, &signingErr) {
		return err
	}
	return Error{Err: err}
}

func removeIfExists(path string) error {
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

type commandRunner struct{}

func (commandRunner) LookPath(file string) (string, error) {
	return exec.LookPath(file)
}

func (commandRunner) Run(ctx context.Context, command string, args []string, stdin []byte) error {
	cmd := exec.CommandContext(ctx, command, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message != "" {
			return fmt.Errorf("%w: %s", err, message)
		}
		return err
	}
	return nil
}
