package appconfig

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"mirrors/internal/download"
)

const (
	defaultHTTPTimeout     = 30 * time.Second
	defaultHTTPRetries     = 3
	defaultHTTPRetryDelay  = time.Second
	defaultDownloadThreads = 1
)

// Config contains application-wide defaults from ~/.config/mirrors.conf.
type Config struct {
	DataRoot        string
	MirrorsRoot     string
	LogsRoot        string
	HTTPTimeout     time.Duration
	HTTPRetries     int
	HTTPRetryDelay  time.Duration
	DownloadThreads int
	Path            string
}

// Load returns global application configuration, using defaults when no file exists.
func Load() (Config, error) {
	cfg := Default()
	path, found, err := configPath()
	if err != nil {
		return Config{}, err
	}
	if !found {
		cfg.Path = path
		if err := writeDefaultConfig(path, cfg); err != nil {
			fmt.Fprintf(os.Stderr, "WARNING: could not create default app config %q: %v\n", path, err)
			if defaultsErr := ensureConfiguredDirs(&cfg); defaultsErr != nil {
				return Config{}, fmt.Errorf("create default app config %q: %v; default paths are not usable: %w", path, err, defaultsErr)
			}
			return cfg, nil
		}
		if err := ensureConfiguredDirs(&cfg); err != nil {
			return Config{}, fmt.Errorf("%s: %w", path, err)
		}
		return cfg, nil
	}
	values, err := readAppConfig(path)
	if err != nil {
		return Config{}, err
	}
	cfg.Path = path
	if err := applyValues(&cfg, values); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := ensureConfiguredDirs(&cfg); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// Default returns the built-in application defaults.
func Default() Config {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		home = "~"
	}
	return Config{
		DataRoot:        filepath.Join(home, ".mirrors", ".data"),
		MirrorsRoot:     filepath.Join(home, ".mirrors", "mirrors"),
		LogsRoot:        filepath.Join(home, ".mirrors", ".logs"),
		HTTPTimeout:     defaultHTTPTimeout,
		HTTPRetries:     defaultHTTPRetries,
		HTTPRetryDelay:  defaultHTTPRetryDelay,
		DownloadThreads: defaultDownloadThreads,
	}
}

// NewDownloader returns a downloader client configured with global HTTP defaults.
func (cfg Config) NewDownloader() download.Downloader {
	return download.NewClient(
		download.WithTimeout(cfg.HTTPTimeout),
		download.WithRetries(cfg.HTTPRetries),
		download.WithRetryDelay(cfg.HTTPRetryDelay),
	)
}

// DBDir returns the directory containing per-mirror SQLite DB files.
func (cfg Config) DBDir() string {
	return filepath.Join(cfg.DataRoot, "db")
}

// DBPath returns the per-mirror SQLite database path.
func (cfg Config) DBPath(name string) string {
	return filepath.Join(cfg.DBDir(), name+".sqlite")
}

// PackageDir returns the package pool directory.
func (cfg Config) PackageDir() string {
	return filepath.Join(cfg.DataRoot, "packages")
}

func configPath() (string, bool, error) {
	if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdg != "" {
		path := filepath.Join(xdg, "mirrors.conf")
		found, err := fileExists(path)
		return path, found, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false, err
	}
	path := filepath.Join(home, ".config", "mirrors.conf")
	found, err := fileExists(path)
	return path, found, err
}

func fileExists(path string) (bool, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) || errors.Is(err, syscall.ENOTDIR) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func readAppConfig(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = file.Close()
	}()

	values := map[string]string{}
	inApp := true
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section := strings.ToLower(strings.TrimSpace(strings.Trim(line, "[]")))
			inApp = section == "app" || section == "mirrors"
			continue
		}
		if !inApp {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid config line %q", line)
		}
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		value := strings.Trim(strings.TrimSpace(parts[1]), `"'`)
		values[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return values, nil
}

func applyValues(cfg *Config, values map[string]string) error {
	if value := strings.TrimSpace(values["data_root"]); value != "" {
		cfg.DataRoot = expandHome(value)
	}
	if value := strings.TrimSpace(values["mirrors_root"]); value != "" {
		cfg.MirrorsRoot = expandHome(value)
	}
	if value := strings.TrimSpace(values["logs_root"]); value != "" {
		cfg.LogsRoot = expandHome(value)
	}
	if strings.TrimSpace(cfg.DataRoot) == "" {
		return fmt.Errorf("data_root is required")
	}
	if strings.TrimSpace(cfg.MirrorsRoot) == "" {
		return fmt.Errorf("mirrors_root is required")
	}
	if strings.TrimSpace(cfg.LogsRoot) == "" {
		return fmt.Errorf("logs_root is required")
	}

	if err := applyDuration(values, "http_timeout", &cfg.HTTPTimeout); err != nil {
		return err
	}
	if err := applyInt(values, "http_retries", &cfg.HTTPRetries); err != nil {
		return err
	}
	if err := applyDuration(values, "http_retry_delay", &cfg.HTTPRetryDelay); err != nil {
		return err
	}
	if err := applyInt(values, "download_threads", &cfg.DownloadThreads); err != nil {
		return err
	}
	if cfg.HTTPTimeout <= 0 {
		return fmt.Errorf("http_timeout must be positive")
	}
	if cfg.HTTPRetries < 0 {
		return fmt.Errorf("http_retries must be zero or positive")
	}
	if cfg.HTTPRetryDelay < 0 {
		return fmt.Errorf("http_retry_delay must be zero or positive")
	}
	if cfg.DownloadThreads < 1 {
		return fmt.Errorf("download_threads must be at least 1")
	}
	return nil
}

func writeDefaultConfig(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	content := fmt.Sprintf(`[app]
data_root = %s
mirrors_root = %s
logs_root = %s
http_timeout = %s
http_retries = %d
http_retry_delay = %s
download_threads = %d
`,
		cfg.DataRoot,
		cfg.MirrorsRoot,
		cfg.LogsRoot,
		cfg.HTTPTimeout,
		cfg.HTTPRetries,
		cfg.HTTPRetryDelay,
		cfg.DownloadThreads,
	)
	return os.WriteFile(path, []byte(content), 0644)
}

func ensureConfiguredDirs(cfg *Config) error {
	for _, item := range []struct {
		field string
		path  string
	}{
		{field: "data_root", path: cfg.DataRoot},
		{field: "mirrors_root", path: cfg.MirrorsRoot},
		{field: "logs_root", path: cfg.LogsRoot},
	} {
		if err := ensureDir(item.field, item.path); err != nil {
			return err
		}
	}
	return nil
}

func ensureDir(field, path string) error {
	if err := os.MkdirAll(path, 0755); err != nil {
		return fmt.Errorf("%s %q is not usable: %w", field, path, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("%s %q is not usable: %w", field, path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s %q is not a directory", field, path)
	}
	if err := writable(path); err != nil {
		return fmt.Errorf("%s %q is not writable: %w", field, path, err)
	}
	return nil
}

func writable(path string) error {
	file, err := os.CreateTemp(path, ".mirrors-write-test-*")
	if err != nil {
		return err
	}
	name := file.Name()
	closeErr := file.Close()
	removeErr := os.Remove(name)
	if closeErr != nil {
		return closeErr
	}
	return removeErr
}

func applyDuration(values map[string]string, key string, target *time.Duration) error {
	value := strings.TrimSpace(values[key])
	if value == "" {
		return nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fmt.Errorf("invalid %s %q: %w", key, value, err)
	}
	*target = parsed
	return nil
}

func applyInt(values map[string]string, key string, target *int) error {
	value := strings.TrimSpace(values[key])
	if value == "" {
		return nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("invalid %s %q: %w", key, value, err)
	}
	*target = parsed
	return nil
}

func expandHome(path string) string {
	path = strings.TrimSpace(path)
	if path == "~" {
		home, err := os.UserHomeDir()
		if err == nil && home != "" {
			return home
		}
		return path
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil && home != "" {
			return filepath.Join(home, path[2:])
		}
	}
	return filepath.Clean(path)
}
