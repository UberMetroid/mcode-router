package main

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"
)

// releaseEdit rewrites one release file.
type releaseEdit struct {
	rel   string
	apply func(string) (string, error)
}

// applyVersion rewrites every release file to carry the given version.
func applyVersion(root, version, date string) error {
	edits := []releaseEdit{
		{catalogPath, func(c string) (string, error) { return bumpCatalog(c, version) }},
		{manifestPath, func(c string) (string, error) { return bumpManifest(c, version) }},
		{changelogPath, func(c string) (string, error) { return bumpChangelog(c, version, date) }},
	}
	for _, edit := range edits {
		if err := applyEdit(root, edit); err != nil {
			return err
		}
	}
	return nil
}

// applyEdit rewrites a single release file in place.
func applyEdit(root string, edit releaseEdit) error {
	path := filepath.Join(root, edit.rel)
	content, err := readText(path)
	if err != nil {
		return err
	}
	updated, err := edit.apply(content)
	if err != nil {
		return fmt.Errorf("%s: %w", edit.rel, err)
	}
	if updated == content {
		return nil
	}
	if err := writeText(path, updated); err != nil {
		return fmt.Errorf("%s: %w", edit.rel, err)
	}
	return nil
}

// runBump writes the requested version into every release file.
func runBump(args []string, outW, errW io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(errW, "error: bump takes exactly one version argument")
		return 2
	}
	version := strings.TrimSpace(args[0])
	if _, err := parseVersion(version); err != nil {
		fmt.Fprintf(errW, "error: %v\n", err)
		return 2
	}
	root, err := repoRoot(".")
	if err != nil {
		fmt.Fprintf(errW, "error: %v\n", err)
		return 1
	}
	before, err := currentVersions(root)
	if err != nil {
		fmt.Fprintf(errW, "error: %v\n", err)
		return 1
	}
	date := time.Now().Format("2006-01-02")
	if err := applyVersion(root, version, date); err != nil {
		fmt.Fprintf(errW, "error: %v\n", err)
		return 1
	}
	after, err := currentVersions(root)
	if err != nil {
		fmt.Fprintf(errW, "error: %v\n", err)
		return 1
	}
	if after.catalog != version || after.manifest != version || after.link != version {
		fmt.Fprintf(errW, "error: version mismatch after bump: %+v\n", after)
		return 1
	}
	changelog, err := readText(filepath.Join(root, changelogPath))
	if err != nil {
		fmt.Fprintf(errW, "error: %v\n", err)
		return 1
	}
	fmt.Fprintf(outW, "bumped %s -> %s\n", before.catalog, version)
	fmt.Fprintf(outW, "  %s  %s -> %s\n", catalogPath, before.catalog, after.catalog)
	fmt.Fprintf(outW, "  %s  %s -> %s\n", manifestPath, before.manifest, after.manifest)
	fmt.Fprintf(outW, "  %s  release link %s\n", changelogPath, after.link)
	if changelogBodyEmpty(changelog, version) {
		fmt.Fprintf(outW, "  note: write the %s changelog prose before tagging\n", version)
	}
	return 0
}

// runCheck verifies that every release file agrees on the version.
func runCheck(outW, errW io.Writer) int {
	root, err := repoRoot(".")
	if err != nil {
		fmt.Fprintf(errW, "error: %v\n", err)
		return 1
	}
	versions, err := currentVersions(root)
	if err != nil {
		fmt.Fprintf(errW, "error: %v\n", err)
		return 1
	}
	fmt.Fprintf(outW, "CLI version      : %s\n", versions.catalog)
	fmt.Fprintf(outW, "plugin manifest : %s\n", versions.manifest)
	fmt.Fprintf(outW, "changelog link  : %s\n", versions.link)
	if versions.catalog != versions.manifest {
		fmt.Fprintf(errW, "error: CLI and plugin manifest disagree: %s vs %s\n",
			versions.catalog, versions.manifest)
		return 1
	}
	if versions.link != versions.catalog {
		fmt.Fprintf(errW, "error: changelog link %s does not match %s\n",
			versions.link, versions.catalog)
		return 1
	}
	changelog, err := readText(filepath.Join(root, changelogPath))
	if err != nil {
		fmt.Fprintf(errW, "error: %v\n", err)
		return 1
	}
	if changelogBodyEmpty(changelog, versions.catalog) {
		fmt.Fprintf(outW, "note: %s has no changelog prose yet\n", versions.catalog)
	}
	fmt.Fprintln(outW, "release files agree")
	return 0
}
