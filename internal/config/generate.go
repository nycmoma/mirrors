package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"

	"mirrors/internal/debmeta"
	"mirrors/internal/download"
)

var generatedNameChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// UpstreamRelease describes Release/InRelease values observed during validation.
type UpstreamRelease struct {
	Suite  string
	Origin string
	Label  string
}

// Generate returns a mirror config derived from a concrete Release/InRelease URL.
func Generate(rawURL string) (Mirror, error) {
	return GenerateWithDownloader(context.Background(), rawURL, download.NewClient())
}

// GenerateWithDownloader fetches a concrete Release/InRelease URL and returns
// one normalized config derived from that metadata.
func GenerateWithDownloader(ctx context.Context, rawURL string, downloader download.Downloader) (Mirror, error) {
	if downloader == nil {
		return Mirror{}, fmt.Errorf("downloader is required")
	}
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return Mirror{}, fmt.Errorf("invalid mirror URL %q: expected http or https URL", rawURL)
	}

	baseURL, suite, metadataName, err := releaseURLParts(parsed)
	if err != nil {
		return Mirror{}, err
	}
	baseParsed, err := url.Parse(baseURL)
	if err != nil {
		return Mirror{}, err
	}
	releaseMeta, err := fetchReleaseURL(ctx, downloader, parsed.String(), metadataName)
	if err != nil {
		return Mirror{}, err
	}
	dist, release := splitSuite(firstNonEmpty(releaseMeta.Suite, releaseMeta.Codename, suite))
	name := generatedName(baseParsed, suite)

	cfg := FromValues(Values{
		Name:       name,
		URL:        baseURL,
		Dist:       dist,
		Release:    release,
		Origin:     firstNonEmpty(releaseMeta.Origin, "default"),
		Label:      firstNonEmpty(releaseMeta.Label, "default"),
		Arch:       strings.Join(releaseMeta.Architectures, ", "),
		Components: strings.Join(releaseMeta.Components, ", "),
		Path:       name,
		Merge:      Merge{},
		Signing:    Signing{},
	})
	if err := Validate(cfg); err != nil {
		return Mirror{}, err
	}
	return cfg, nil
}

// ValidateUpstream verifies that a config's requested suites, components, and
// architectures exist in upstream Release metadata.
func ValidateUpstream(ctx context.Context, cfg Mirror, downloader download.Downloader) error {
	_, err := ValidateUpstreamDetails(ctx, cfg, downloader)
	return err
}

// ValidateUpstreamDetails verifies upstream metadata and returns the actual
// origin/label values found for each requested suite.
func ValidateUpstreamDetails(ctx context.Context, cfg Mirror, downloader download.Downloader) ([]UpstreamRelease, error) {
	if err := Validate(cfg); err != nil {
		return nil, err
	}
	if downloader == nil {
		return nil, fmt.Errorf("downloader is required")
	}
	var details []UpstreamRelease
	for _, dist := range cfg.Dists {
		for _, release := range cfg.Releases {
			suite := SuiteName(dist, release)
			releaseMeta, err := FetchRelease(ctx, downloader, cfg.URL, suite)
			if err != nil {
				return nil, fmt.Errorf("suite %q metadata: %w", suite, err)
			}
			details = append(details, UpstreamRelease{
				Suite:  suite,
				Origin: releaseMeta.Origin,
				Label:  releaseMeta.Label,
			})
			if cfg.Origin != "default" && releaseMeta.Origin != "" && releaseMeta.Origin != cfg.Origin {
				return nil, fmt.Errorf("suite %q origin mismatch: got %q, want %q", suite, releaseMeta.Origin, cfg.Origin)
			}
			if cfg.Label != "default" && releaseMeta.Label != "" && releaseMeta.Label != cfg.Label {
				return nil, fmt.Errorf("suite %q label mismatch: got %q, want %q", suite, releaseMeta.Label, cfg.Label)
			}
			for _, component := range cfg.Components {
				if !contains(releaseMeta.Components, component) {
					return nil, fmt.Errorf("suite %q does not contain component %q", suite, component)
				}
			}
			for _, arch := range cfg.Arch {
				if !contains(releaseMeta.Architectures, arch) {
					return nil, fmt.Errorf("suite %q does not contain architecture %q", suite, arch)
				}
			}
		}
	}
	return details, nil
}

// FetchRelease fetches InRelease or Release metadata for one suite.
func FetchRelease(ctx context.Context, downloader download.Downloader, baseURL, suite string) (*debmeta.Release, error) {
	inReleaseURL, err := joinURL(baseURL, path.Join("dists", suite, "InRelease"))
	if err != nil {
		return nil, err
	}
	data, err := downloader.FetchMetadata(ctx, inReleaseURL, nil)
	if err == nil {
		release, _, err := debmeta.ParseInRelease(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", inReleaseURL, err)
		}
		return release, nil
	}
	if !isHTTPStatus(err, 404) {
		return nil, err
	}

	releaseURL, err := joinURL(baseURL, path.Join("dists", suite, "Release"))
	if err != nil {
		return nil, err
	}
	data, err = downloader.FetchMetadata(ctx, releaseURL, nil)
	if err != nil {
		return nil, err
	}
	release, err := debmeta.ParseRelease(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", releaseURL, err)
	}
	return release, nil
}

// SuiteName maps config dist/release values to Debian suite names.
func SuiteName(dist, release string) string {
	if release == "" || release == "default" {
		return dist
	}
	return dist + "-" + release
}

func releaseURLParts(parsed *url.URL) (string, string, string, error) {
	parts := pathParts(parsed.Path)
	for index, part := range parts {
		if part != "dists" || index+1 >= len(parts) {
			continue
		}
		if index+2 >= len(parts) {
			return "", "", "", fmt.Errorf("config generate requires a Release or InRelease URL under /dists/<suite>/")
		}
		suite := parts[index+1]
		metadataName := parts[index+2]
		if metadataName != "Release" && metadataName != "InRelease" {
			return "", "", "", fmt.Errorf("config generate requires a Release or InRelease URL, got %q", metadataName)
		}
		if index+3 != len(parts) {
			return "", "", "", fmt.Errorf("config generate requires a direct /dists/<suite>/Release or /dists/<suite>/InRelease URL")
		}
		base := *parsed
		base.Path = "/" + path.Join(parts[:index]...)
		if len(parts[:index]) == 0 {
			base.Path = "/"
		}
		base.RawQuery = ""
		base.Fragment = ""
		return strings.TrimRight(base.String(), "/") + "/", suite, metadataName, nil
	}
	return "", "", "", fmt.Errorf("config generate requires a Release or InRelease URL under /dists/<suite>/")
}

func fetchReleaseURL(ctx context.Context, downloader download.Downloader, rawURL, metadataName string) (*debmeta.Release, error) {
	data, err := downloader.FetchMetadata(ctx, rawURL, nil)
	if err != nil {
		return nil, err
	}
	switch metadataName {
	case "InRelease":
		release, _, err := debmeta.ParseInRelease(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", rawURL, err)
		}
		return release, nil
	case "Release":
		release, err := debmeta.ParseRelease(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", rawURL, err)
		}
		return release, nil
	default:
		return nil, fmt.Errorf("unsupported release metadata %q", metadataName)
	}
}

func splitSuite(suite string) (string, string) {
	suite = strings.TrimSpace(suite)
	if suite == "" {
		return "stable", "default"
	}
	for _, suffix := range []string{"security", "updates", "backports", "proposed"} {
		marker := "-" + suffix
		if strings.HasSuffix(suite, marker) && len(suite) > len(marker) {
			return strings.TrimSuffix(suite, marker), suffix
		}
	}
	return suite, "default"
}

func generatedName(parsed *url.URL, urlSuite string) string {
	host := strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")
	host = strings.TrimSuffix(host, ".")
	parts := []string{host}
	parts = append(parts, repoIdentityParts(pathParts(parsed.Path))...)
	if strings.TrimSpace(urlSuite) != "" {
		parts = append(parts, urlSuite)
	}
	name := generatedNameChars.ReplaceAllString(strings.Join(parts, "-"), "-")
	name = strings.Trim(name, ".-_")
	if name == "" {
		return "mirror"
	}
	return name
}

func repoIdentityParts(parts []string) []string {
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" && part != "." {
			cleaned = append(cleaned, part)
		}
	}
	if len(cleaned) >= 2 && strings.EqualFold(cleaned[0], "linux") && isDistroLayoutSegment(cleaned[1]) {
		return append([]string{cleaned[1]}, cleaned[2:]...)
	}
	for len(cleaned) > 0 && isGenericTrailingRepoSegment(cleaned[len(cleaned)-1]) {
		cleaned = cleaned[:len(cleaned)-1]
	}
	return cleaned
}

func isGenericTrailingRepoSegment(value string) bool {
	switch strings.ToLower(value) {
	case "ubuntu", "debian", "apt", "repo", "repository":
		return true
	default:
		return false
	}
}

func isDistroLayoutSegment(value string) bool {
	switch strings.ToLower(value) {
	case "ubuntu", "debian", "centos", "fedora", "rhel", "rocky", "almalinux":
		return true
	default:
		return false
	}
}

func pathParts(value string) []string {
	var parts []string
	for _, part := range strings.Split(path.Clean(value), "/") {
		if part != "" && part != "." {
			parts = append(parts, part)
		}
	}
	return parts
}

func joinURL(baseURL, relativePath string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return "", err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid base URL %q", baseURL)
	}
	base := strings.TrimRight(parsed.String(), "/")
	rel := strings.TrimLeft(relativePath, "/")
	return base + "/" + rel, nil
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func isHTTPStatus(err error, status int) bool {
	var httpErr *download.HTTPError
	return errors.As(err, &httpErr) && httpErr.StatusCode == status
}
