package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// Load parses a [mirror] config file.
func Load(path string) (Mirror, error) {
	raw, err := readMirrorSection(path)
	if err != nil {
		return Mirror{}, err
	}

	return Mirror{
		Name:       raw["name"],
		URL:        raw["url"],
		Dists:      splitList(raw["dist"]),
		Releases:   splitList(defaultString(raw["release"], "default")),
		Origin:     defaultString(raw["origin"], "default"),
		Label:      defaultString(raw["label"], "default"),
		Arch:       splitList(raw["arch"]),
		Components: splitList(raw["components"]),
		Path:       raw["path"],
		Merge:      parseMerge(raw["merge"]),
		Server:     raw["server"],
	}, nil
}

func readMirrorSection(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = file.Close()
	}()

	values := map[string]string{}
	inMirror := false
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			inMirror = strings.EqualFold(strings.Trim(line, "[]"), "mirror")
			continue
		}
		if !inMirror {
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
	if len(values) == 0 {
		return nil, fmt.Errorf("missing [mirror] section in %s", path)
	}
	return values, nil
}

func splitList(value string) []string {
	var result []string
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			result = append(result, item)
		}
	}
	return result
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func parseMerge(value string) Merge {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "", "no", "false", "0":
		return Merge{}
	case "yes", "true":
		return Merge{Enabled: true}
	}

	depth := 0
	for _, char := range value {
		if char < '0' || char > '9' {
			return Merge{Enabled: true}
		}
		depth = depth*10 + int(char-'0')
	}
	return Merge{Enabled: depth > 0, Depth: depth}
}
