package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Level controls which diagnostic messages are written.
type Level int

const (
	Debug Level = iota
	Info
	Warn
	Error
)

// ParseLevel parses a configured log level.
func ParseLevel(value string) (Level, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "debug":
		return Debug, nil
	case "", "info":
		return Info, nil
	case "warn", "warning":
		return Warn, nil
	case "error":
		return Error, nil
	default:
		return Info, fmt.Errorf("log_level must be one of error, warn, info, or debug")
	}
}

func (level Level) String() string {
	switch level {
	case Debug:
		return "DEBUG"
	case Info:
		return "INFO"
	case Warn:
		return "WARN"
	case Error:
		return "ERROR"
	default:
		return "INFO"
	}
}

// Logger is the narrow diagnostic logging interface used by internal packages.
type Logger interface {
	Debugf(format string, args ...interface{})
	Infof(format string, args ...interface{})
	Warnf(format string, args ...interface{})
	Errorf(format string, args ...interface{})
	Close() error
}

// Nop returns a logger that discards all messages.
func Nop() Logger {
	return nopLogger{}
}

// OpenFile creates a file-backed logger. Parent directories are created.
func OpenFile(path string, level Level) (Logger, error) {
	if strings.TrimSpace(path) == "" {
		return Nop(), nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}
	return &fileLogger{
		file:  file,
		level: level,
		now:   time.Now,
	}, nil
}

type nopLogger struct{}

func (nopLogger) Debugf(string, ...interface{}) {}
func (nopLogger) Infof(string, ...interface{})  {}
func (nopLogger) Warnf(string, ...interface{})  {}
func (nopLogger) Errorf(string, ...interface{}) {}
func (nopLogger) Close() error                  { return nil }

type fileLogger struct {
	mu    sync.Mutex
	file  *os.File
	level Level
	now   func() time.Time
}

func (logger *fileLogger) Debugf(format string, args ...interface{}) {
	logger.logf(Debug, format, args...)
}

func (logger *fileLogger) Infof(format string, args ...interface{}) {
	logger.logf(Info, format, args...)
}

func (logger *fileLogger) Warnf(format string, args ...interface{}) {
	logger.logf(Warn, format, args...)
}

func (logger *fileLogger) Errorf(format string, args ...interface{}) {
	logger.logf(Error, format, args...)
}

func (logger *fileLogger) Close() error {
	logger.mu.Lock()
	defer logger.mu.Unlock()
	if logger.file == nil {
		return nil
	}
	err := logger.file.Close()
	logger.file = nil
	return err
}

func (logger *fileLogger) logf(level Level, format string, args ...interface{}) {
	if logger == nil || logger.file == nil || level < logger.level {
		return
	}
	message := fmt.Sprintf(format, args...)
	message = strings.ReplaceAll(message, "\n", " ")
	line := fmt.Sprintf("%s %-5s %s\n", logger.now().UTC().Format(time.RFC3339), level.String(), message)
	logger.mu.Lock()
	defer logger.mu.Unlock()
	if logger.file != nil {
		_, _ = logger.file.WriteString(line)
	}
}
