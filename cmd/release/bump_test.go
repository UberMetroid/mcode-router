package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sampleCatalog mimics the fleet catalog version constant.
const sampleCatalog = "package fleet\n\nconst (\n\tVersion            = \"1.0.5\"\n" +
	"\tQuotaSchemaVersion = 9\n)\n"

// sampleManifest mimics the plugin manifest version field.
const sampleManifest = "{\n  \"schemaVersion\": 1,\n  \"name\": \"mcode-router\",\n" +
	"  \"version\": \"1.0.5\",\n  \"category\": \"Code\"\n}\n"

// sampleChangelog mimics the release history and its link references.
const sampleChangelog = "# Changelog\n\n## [1.0.5] — 2026-11-17\n\n- Entry.\n\n" +
	"[1.0.5]: https://github.com/UberMetroid/mcode-router/releases/tag/v1.0.5\n" +
	"[1.0.4]: https://github.com/UberMetroid/mcode-router/releases/tag/v1.0.4\n"

// writeRepo stages a minimal repository tree for the release helpers.
func writeRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range map[string]string{
		catalogPath:   sampleCatalog,
		manifestPath:  sampleManifest,
		changelogPath: sampleChangelog,
	} {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	return root
}

func TestBumpCatalog(t *testing.T) {
	out, err := bumpCatalog(sampleCatalog, "1.0.6")
	if err != nil {
		t.Fatalf("bump: %v", err)
	}
	if !strings.Contains(out, `Version            = "1.0.6"`) {
		t.Fatalf("version constant not rewritten:\n%s", out)
	}
	if !strings.Contains(out, "QuotaSchemaVersion = 9") {
		t.Fatalf("unrelated constant was disturbed:\n%s", out)
	}
	if !strings.HasPrefix(out, "package fleet\n") {
		t.Fatalf("file prefix was lost:\n%s", out)
	}
}

func TestBumpManifest(t *testing.T) {
	out, err := bumpManifest(sampleManifest, "1.0.6")
	if err != nil {
		t.Fatalf("bump: %v", err)
	}
	if !strings.Contains(out, `"version": "1.0.6"`) {
		t.Fatalf("manifest version not rewritten:\n%s", out)
	}
	if !strings.Contains(out, `"schemaVersion": 1`) {
		t.Fatalf("schemaVersion was disturbed:\n%s", out)
	}
	if !strings.HasPrefix(out, "{\n  \"schemaVersion\": 1,") {
		t.Fatalf("file prefix was lost:\n%s", out)
	}
}

func TestBumpChangelog(t *testing.T) {
	out, err := bumpChangelog(sampleChangelog, "1.0.6", "2026-11-18")
	if err != nil {
		t.Fatalf("bump: %v", err)
	}
	if !strings.Contains(out, "## [1.0.6] — 2026-11-18") {
		t.Fatalf("release heading missing:\n%s", out)
	}
	link := "[1.0.6]: https://github.com/UberMetroid/mcode-router/releases/tag/v1.0.6"
	if !strings.Contains(out, link) {
		t.Fatalf("release link missing:\n%s", out)
	}
	if strings.Index(out, link) > strings.Index(out, "[1.0.5]:") {
		t.Fatalf("release link is out of order:\n%s", out)
	}
	again, err := bumpChangelog(out, "1.0.6", "2026-11-18")
	if err != nil {
		t.Fatalf("second bump: %v", err)
	}
	if again != out {
		t.Fatalf("second bump was not idempotent:\n%s", again)
	}
}

func TestParseVersion(t *testing.T) {
	if _, err := parseVersion("1.0.6"); err != nil {
		t.Fatalf("valid version rejected: %v", err)
	}
	for _, bad := range []string{"1.0", "v1.0.6", "1.0.6-rc1", "one.two.three", ""} {
		if _, err := parseVersion(bad); err == nil {
			t.Fatalf("invalid version accepted: %q", bad)
		}
	}
}

func TestRepoRoot(t *testing.T) {
	root := writeRepo(t)
	found, err := repoRoot(filepath.Join(root, "internal", "fleet"))
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	if found != root {
		t.Fatalf("repo root = %q, want %q", found, root)
	}
}

func TestApplyVersion(t *testing.T) {
	root := writeRepo(t)
	if err := applyVersion(root, "1.0.6", "2026-11-18"); err != nil {
		t.Fatalf("apply: %v", err)
	}
	versions, err := currentVersions(root)
	if err != nil {
		t.Fatalf("versions: %v", err)
	}
	if versions.catalog != "1.0.6" || versions.manifest != "1.0.6" || versions.link != "1.0.6" {
		t.Fatalf("release files not aligned: %+v", versions)
	}
}
