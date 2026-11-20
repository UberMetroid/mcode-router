package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cycleChangelog mimics a changelog mid-cycle: an open heading with prose.
const cycleChangelog = "# Changelog\n\n## [Unreleased]\n\n### Changed\n\n- New prose.\n\n" +
	"## [1.0.5] — 2026-11-17\n\n- Entry.\n\n" +
	"[1.0.5]: https://github.com/UberMetroid/mcode-router/releases/tag/v1.0.5\n"

// strandedChangelog mimics the shape a bump leaves behind when it renames the
// first open heading and leaves the second one below the release.
const strandedChangelog = "# Changelog\n\n## [1.0.5] — 2026-11-17\n\n- Entry.\n\n" +
	"## [Unreleased]\n\n### Changed\n\n- New prose.\n\n" +
	"[1.0.5]: https://github.com/UberMetroid/mcode-router/releases/tag/v1.0.5\n"

// duplicateChangelog mimics two open headings written in the same cycle.
const duplicateChangelog = "# Changelog\n\n## [Unreleased]\n\n- First.\n\n" +
	"## [Unreleased]\n\n- Second.\n\n"

func TestBumpRenamesOpenHeading(t *testing.T) {
	out, err := bumpChangelog(cycleChangelog, "1.0.6", "2026-11-18")
	if err != nil {
		t.Fatalf("bump: %v", err)
	}
	if strings.Contains(out, "[Unreleased]") {
		t.Fatalf("open heading survived the bump:\n%s", out)
	}
	release := strings.Index(out, "## [1.0.6]")
	prose := strings.Index(out, "- New prose.")
	older := strings.Index(out, "## [1.0.5]")
	if release < 0 || prose < release || prose > older {
		t.Fatalf("prose is not under the new release heading:\n%s", out)
	}
	if changelogBodyEmpty(out, "1.0.6") {
		t.Fatalf("release reads as empty:\n%s", out)
	}
}

func TestCheckAcceptsCycle(t *testing.T) {
	if err := checkUnreleased(sampleChangelog); err != nil {
		t.Fatalf("released changelog rejected: %v", err)
	}
	if err := checkUnreleased(cycleChangelog); err != nil {
		t.Fatalf("open cycle rejected: %v", err)
	}
}

func TestCheckRejectsStranded(t *testing.T) {
	err := checkUnreleased(strandedChangelog)
	if err == nil {
		t.Fatal("stranded open heading accepted")
	}
	if !strings.Contains(err.Error(), "stranded") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCheckRejectsDuplicate(t *testing.T) {
	err := checkUnreleased(duplicateChangelog)
	if err == nil {
		t.Fatal("duplicate open headings accepted")
	}
	if !strings.Contains(err.Error(), "merge them into one") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestApplyRefusesStranded(t *testing.T) {
	root := writeRepo(t)
	path := filepath.Join(root, changelogPath)
	if err := os.WriteFile(path, []byte(strandedChangelog), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := applyVersion(root, "1.0.6", "2026-11-18"); err == nil {
		t.Fatal("apply accepted a stranded changelog")
	}
	versions, err := currentVersions(root)
	if err != nil {
		t.Fatalf("versions: %v", err)
	}
	if versions.catalog != "1.0.5" {
		t.Fatalf("catalog was written before the check failed: %+v", versions)
	}
}

func TestApplyVersionBumpsOpenCycle(t *testing.T) {
	root := writeRepo(t)
	path := filepath.Join(root, changelogPath)
	if err := os.WriteFile(path, []byte(cycleChangelog), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := applyVersion(root, "1.0.6", "2026-11-18"); err != nil {
		t.Fatalf("apply: %v", err)
	}
	out, err := readText(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(out, "[Unreleased]") {
		t.Fatalf("open heading survived the bump:\n%s", out)
	}
	versions, err := currentVersions(root)
	if err != nil {
		t.Fatalf("versions: %v", err)
	}
	if versions.catalog != "1.0.6" || versions.link != "1.0.6" {
		t.Fatalf("release files not aligned: %+v", versions)
	}
}
