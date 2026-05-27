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

	merge, err := ParseMerge(raw["merge"])
	if err != nil {
		return Mirror{}, err
	}

	return FromValues(Values{
		Name:       strings.TrimSpace(raw["name"]),
		URL:        strings.TrimSpace(raw["url"]),
		Dist:       raw["dist"],
		Release:    raw["release"],
		Origin:     strings.TrimSpace(defaultString(raw["origin"], "default")),
		Label:      strings.TrimSpace(defaultString(raw["label"], "default")),
		Arch:       raw["arch"],
		Components: raw["components"],
		Path:       strings.TrimSpace(raw["path"]),
		Merge:      merge,
		Server:     strings.TrimSpace(raw["server"]),
	}), nil
}

// Values contains raw scalar config values before list normalization.
type Values struct {
	Name       string
	URL        string
	Dist       string
	Release    string
	Origin     string
	Label      string
	Arch       string
	Components string
	Path       string
	Merge      Merge
	Server     string
}

// FromValues builds a normalized Mirror from raw config values.
func FromValues(values Values) Mirror {
	return Mirror{
		Name:       strings.TrimSpace(values.Name),
		URL:        strings.TrimSpace(values.URL),
		Dists:      splitList(values.Dist),
		Releases:   splitList(defaultString(values.Release, "default")),
		Origin:     strings.TrimSpace(defaultString(values.Origin, "default")),
		Label:      strings.TrimSpace(defaultString(values.Label, "default")),
		Arch:       splitList(values.Arch),
		Components: splitList(values.Components),
		Path:       strings.TrimSpace(values.Path),
		Merge:      values.Merge,
		Server:     strings.TrimSpace(values.Server),
	}
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

func ParseMerge(value string) (Merge, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "", "no", "0":
		return Merge{}, nil
	case "yes":
		return Merge{Enabled: true}, nil
	}

	depth := 0
	for _, char := range value {
		if char < '0' || char > '9' {
			return Merge{}, fmt.Errorf("invalid merge value %q: use no, yes, 0, or a positive number", value)
		}
		depth = depth*10 + int(char-'0')
	}
	return Merge{Enabled: depth > 0, Depth: depth}, nil
}
