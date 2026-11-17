package main

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var (
	// semverRe matches a plain major.minor.patch version.
	semverRe = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)$`)
	// catalogVersionRe matches the CLI version constant in the fleet catalog.
	catalogVersionRe = regexp.MustCompile(`(?m)^([ \t]*Version[ \t]*=[ \t]*)"([^"]*)"`)
	// manifestVersionRe matches the version field of the plugin manifest.
	manifestVersionRe = regexp.MustCompile(`("version"[ \t]*:[ \t]*)"([^"]*)"`)
)

// releaseVersions holds the version found in each release file.
type releaseVersions struct {
	catalog  string
	manifest string
	link     string
}

// parseVersion splits a version into its numeric components.
func parseVersion(v string) ([3]int, error) {
	match := semverRe.FindStringSubmatch(strings.TrimSpace(v))
	if match == nil {
		return [3]int{}, fmt.Errorf("invalid version %q, expected major.minor.patch", v)
	}
	var out [3]int
	for i := 0; i < 3; i++ {
		n, err := strconv.Atoi(match[i+1])
		if err != nil {
			return [3]int{}, fmt.Errorf("invalid version %q", v)
		}
		out[i] = n
	}
	return out, nil
}

// versionLess reports whether version a sorts before version b.
func versionLess(a, b [3]int) bool {
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// readCatalogVersion extracts the CLI version constant from the fleet catalog.
func readCatalogVersion(path string) (string, error) {
	content, err := readText(path)
	if err != nil {
		return "", err
	}
	match := catalogVersionRe.FindStringSubmatch(content)
	if match == nil {
		return "", fmt.Errorf("no Version constant in %s", path)
	}
	return match[2], nil
}

// readManifestVersion extracts the version field from the plugin manifest.
func readManifestVersion(path string) (string, error) {
	content, err := readText(path)
	if err != nil {
		return "", err
	}
	match := manifestVersionRe.FindStringSubmatch(content)
	if match == nil {
		return "", fmt.Errorf("no version field in %s", path)
	}
	return match[2], nil
}

// currentVersions reads the version from every release file.
func currentVersions(root string) (releaseVersions, error) {
	out := releaseVersions{}
	var err error
	out.catalog, err = readCatalogVersion(filepath.Join(root, catalogPath))
	if err != nil {
		return out, err
	}
	out.manifest, err = readManifestVersion(filepath.Join(root, manifestPath))
	if err != nil {
		return out, err
	}
	out.link, err = readChangelogVersion(filepath.Join(root, changelogPath))
	if err != nil {
		return out, err
	}
	return out, nil
}
