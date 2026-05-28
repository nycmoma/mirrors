package mirror

import (
	"fmt"
	"net/url"
	"path"
	"strings"
)

// ReleaseURL returns the upstream Release metadata URL for a suite.
func ReleaseURL(baseURL, suite string) (string, error) {
	return joinURL(baseURL, path.Join("dists", suite, "Release"))
}

// InReleaseURL returns the upstream InRelease metadata URL for a suite.
func InReleaseURL(baseURL, suite string) (string, error) {
	return joinURL(baseURL, path.Join("dists", suite, "InRelease"))
}

// PackagesIndexPath returns the Release-relative Packages index path.
func PackagesIndexPath(component, arch, compression string) string {
	name := "Packages"
	if compression != "" {
		name += "." + strings.TrimPrefix(compression, ".")
	}
	return path.Join(component, "binary-"+arch, name)
}

// PackagesIndexURL returns the upstream Packages index URL for a suite.
func PackagesIndexURL(baseURL, suite, component, arch, compression string) (string, error) {
	return joinURL(baseURL, path.Join("dists", suite, PackagesIndexPath(component, arch, compression)))
}

// PackageURL returns the upstream package file URL.
func PackageURL(baseURL, filename string) (string, error) {
	return joinURL(baseURL, filename)
}

func joinURL(baseURL, relativePath string) (string, error) {
	if strings.TrimSpace(baseURL) == "" {
		return "", fmt.Errorf("base URL is required")
	}
	parsed, err := url.Parse(baseURL)
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
