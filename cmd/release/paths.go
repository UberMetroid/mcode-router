package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// catalogPath holds the CLI version constant.
	catalogPath = "internal/fleet/catalog.go"
	// manifestPath holds the MiniMax Code plugin manifest.
	manifestPath = "plugins/mcode-router/.minimax-plugin/plugin.json"
	// changelogPath holds the release history and its link references.
	changelogPath = "CHANGELOG.md"
)

// releaseFiles are the repository files that carry the release version.
var releaseFiles = []string{catalogPath, manifestPath, changelogPath}

// repoRoot walks up from startDir until it finds the release files.
func repoRoot(startDir string) (string, error) {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return "", err
	}
	for {
		if hasReleaseFiles(dir) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no mcode-router repository found above %s", startDir)
		}
		dir = parent
	}
}

// hasReleaseFiles reports whether dir holds every file a release edits.
func hasReleaseFiles(dir string) bool {
	for _, rel := range releaseFiles {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			return false
		}
	}
	return true
}

// readText reads a UTF-8 text file.
func readText(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// writeText replaces a file's contents, keeping its permission bits.
func writeText(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

// appendBlock adds a block at the end of the content after one blank line.
func appendBlock(content, block string) string {
	return strings.TrimRight(content, "\n") + "\n\n" + block
}
