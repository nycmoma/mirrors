package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"mirrors/internal/mirror"
)

type terminalProgressReporter struct {
	interactive  bool
	started      bool
	total        int
	completed    int
	failed       int
	active       int
	knownBytes   int64
	totalBytes   int64
	unknown      int
	current      string
	lineLen      int
	packageBytes map[string]int64
}

func newTerminalProgressReporter() *terminalProgressReporter {
	info, err := os.Stdout.Stat()
	interactive := err == nil && info.Mode()&os.ModeCharDevice != 0
	return &terminalProgressReporter{interactive: interactive}
}

func (reporter *terminalProgressReporter) Start(event mirror.DownloadProgressStart) {
	reporter.started = true
	reporter.total = event.TotalPackages
	reporter.totalBytes = event.TotalKnownBytes
	reporter.unknown = event.UnknownSizePackages
	reporter.packageBytes = map[string]int64{}
	if !reporter.interactive {
		fmt.Printf("Downloading packages: %d package(s), %s known", event.TotalPackages, humanSize(event.TotalKnownBytes))
		if event.UnknownSizePackages > 0 {
			fmt.Printf(", %d unknown-size package(s)", event.UnknownSizePackages)
		}
		fmt.Println()
		return
	}
	reporter.render()
}

func (reporter *terminalProgressReporter) PackageStart(event mirror.DownloadProgressPackageStart) {
	if !reporter.started {
		return
	}
	reporter.active++
	reporter.current = filepath.Base(event.Filename)
	if reporter.interactive {
		reporter.render()
	}
}

func (reporter *terminalProgressReporter) Bytes(event mirror.DownloadProgressBytes) {
	if !reporter.started {
		return
	}
	if event.TotalBytes >= 0 && event.CurrentBytes > event.TotalBytes {
		event.CurrentBytes = event.TotalBytes
	}
	if event.CurrentBytes < 0 {
		event.CurrentBytes = 0
	}
	reporter.packageBytes[event.Filename] = event.CurrentBytes
	reporter.knownBytes = reporter.currentKnownBytes()
	if reporter.interactive {
		reporter.render()
	}
}

func (reporter *terminalProgressReporter) PackageComplete(event mirror.DownloadProgressPackageComplete) {
	if !reporter.started {
		return
	}
	if reporter.active > 0 {
		reporter.active--
	}
	reporter.completed++
	reporter.current = filepath.Base(event.Filename)
	if event.Size >= 0 {
		reporter.packageBytes[event.Filename] = event.Size
		reporter.knownBytes = reporter.currentKnownBytes()
	}
	if !reporter.interactive {
		fmt.Printf("Downloaded %d/%d: %s\n", reporter.completed, reporter.total, filepath.Base(event.Filename))
		return
	}
	reporter.render()
}

func (reporter *terminalProgressReporter) Error(event mirror.DownloadProgressError) {
	if !reporter.started {
		return
	}
	if reporter.active > 0 {
		reporter.active--
	}
	reporter.failed++
	delete(reporter.packageBytes, event.Filename)
	reporter.knownBytes = reporter.currentKnownBytes()
	if reporter.interactive {
		reporter.clearLine()
	}
	fmt.Printf("Failed: %s: %v\n", event.Filename, event.Err)
}

func (reporter *terminalProgressReporter) Finish(event mirror.DownloadProgressFinish) {
	if !reporter.started {
		return
	}
	reporter.completed = event.DownloadedPackages
	reporter.failed = event.FailedPackages
	reporter.knownBytes = event.DownloadedBytes
	reporter.active = 0
	if reporter.interactive {
		reporter.render()
		fmt.Println()
	}
	fmt.Printf("Download complete: downloaded %d, reused %d, failed %d\n", event.DownloadedPackages, event.ReusedPackages, event.FailedPackages)
	reporter.started = false
}

func (reporter *terminalProgressReporter) currentKnownBytes() int64 {
	var total int64
	for _, current := range reporter.packageBytes {
		total += current
	}
	if reporter.totalBytes >= 0 && total > reporter.totalBytes {
		return reporter.totalBytes
	}
	return total
}

func (reporter *terminalProgressReporter) render() {
	line := reporter.line()
	padding := ""
	if reporter.lineLen > len(line) {
		padding = strings.Repeat(" ", reporter.lineLen-len(line))
	}
	fmt.Printf("\r%s%s", line, padding)
	reporter.lineLen = len(line)
}

func (reporter *terminalProgressReporter) clearLine() {
	if reporter.lineLen == 0 {
		return
	}
	fmt.Printf("\r%s\r", strings.Repeat(" ", reporter.lineLen))
	reporter.lineLen = 0
}

func (reporter *terminalProgressReporter) line() string {
	width := 24
	filled := 0
	if reporter.total > 0 {
		filled = reporter.completed * width / reporter.total
	}
	bar := strings.Repeat("#", filled) + strings.Repeat("-", width-filled)
	byteText := fmt.Sprintf("%s/%s", humanSize(reporter.knownBytes), humanSize(reporter.totalBytes))
	if reporter.unknown > 0 {
		byteText += fmt.Sprintf(" known, %d unknown", reporter.unknown)
	}
	current := reporter.current
	if len(current) > 36 {
		current = current[:33] + "..."
	}
	return fmt.Sprintf("Downloading packages [%s] %d/%d %s %d active %d failed %s",
		bar,
		reporter.completed,
		reporter.total,
		byteText,
		reporter.active,
		reporter.failed,
		current,
	)
}
