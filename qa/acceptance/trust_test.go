package acceptance

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/UberMetroid/mcode-router/internal/verify"
)

// TestDiffHygieneCredentialShapes checks that credential shapes beyond the
// three enumerated P0 rules are flagged before a diff reaches a reviewer.
func TestDiffHygieneCredentialShapes(t *testing.T) {
	shapes := []string{
		"postgres" + "://admin:hunter2@db.internal:5432/app",
		"pass" + "word = \"hunter2\"",
		"glpat" + "-ABCDEFGHIJKLMNOPQRST",
		"npm" + "_ABCDEFGHIJKLMNOPQRSTUVWXYZ012345",
		"aws_secret_access" + "_key = k39D2nQ8sT1vR7wL0zX5yB4cF6hJ3mP9aE2uAbCd",
		"eyJhbGciOiJIUzI1NiJ9" + ".eyJzdWIiOiIxIn0.c2lnabcd",
	}
	for _, shape := range shapes {
		dc := verify.DiffContext{
			Diff: diffBody("app.conf", shape), ChangedFiles: []string{"app.conf"},
		}
		if len(verify.CheckDiffHygiene(dc)) == 0 {
			t.Fatalf("credential shape not flagged: %q", shape)
		}
	}
}

// TestDiffHygieneContextLine checks that a credential on an unchanged context
// line of a diff is flagged too, not only on added lines.
func TestDiffHygieneContextLine(t *testing.T) {
	cred := "pass" + "word = \"hunter2\""
	diff := "diff --git a/app.conf b/app.conf\n--- a/app.conf\n+++ b/app.conf\n" +
		"@@ -1,2 +1,2 @@\n " + cred + "\n+changed\n"
	dc := verify.DiffContext{Diff: diff, ChangedFiles: []string{"app.conf"}}
	if len(verify.CheckDiffHygiene(dc)) == 0 {
		t.Fatal("credential on a context line not flagged")
	}
}

// TestSnapshotGitHookDrift checks that a read-only worker planting a git hook
// or rewriting git config is detected as drift.
func TestSnapshotGitHookDrift(t *testing.T) {
	repo := initRepo(t)
	before := verify.SnapshotRepo(repo)
	hook := filepath.Join(repo, ".git", "hooks", "post-commit")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "config", "core.hooksPath", filepath.Join(repo, ".git", "hooks"))
	after := verify.SnapshotRepo(repo)
	if len(verify.SnapshotDrift(before, after)) == 0 {
		t.Fatal("planted git hook and config not detected as drift")
	}
}

// TestSnapshotIgnoredFileDrift checks that a file hidden by .gitignore is
// still seen as drift, whether it was created or rewritten.
func TestSnapshotIgnoredFileDrift(t *testing.T) {
	repo := initRepo(t)
	ignore := filepath.Join(repo, ".gitignore")
	if err := os.WriteFile(ignore, []byte("hidden.env\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", ".gitignore")
	runGit(t, repo, "commit", "-q", "-m", "ignore")
	hidden := filepath.Join(repo, "hidden.env")
	if err := os.WriteFile(hidden, []byte("MODE=600\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := verify.SnapshotRepo(repo)
	st, serr := os.Stat(hidden)
	if serr != nil {
		t.Fatal(serr)
	}
	// Same length, restored timestamp: only content hashing sees this rewrite.
	if err := os.WriteFile(hidden, []byte("MODE=601\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(hidden, st.ModTime(), st.ModTime()); err != nil {
		t.Fatal(err)
	}
	after := verify.SnapshotRepo(repo)
	if len(verify.SnapshotDrift(before, after)) == 0 {
		t.Fatal("rewritten gitignored file not detected as drift")
	}
}

// TestDiffHygieneRemovedLine checks that a credential on a removed diff line
// is flagged: the deleted line still travels to the reviewer inside the patch,
// so it crosses the same boundary as an added one.
func TestDiffHygieneRemovedLine(t *testing.T) {
	cred := "pass" + "word = \"hunter2\""
	diff := "diff --git a/app.conf b/app.conf\n--- a/app.conf\n+++ b/app.conf\n" +
		"@@ -1,2 +1,1 @@\n-" + cred + "\n+safe\n"
	dc := verify.DiffContext{Diff: diff, ChangedFiles: []string{"app.conf"}}
	if len(verify.CheckDiffHygiene(dc)) == 0 {
		t.Fatal("credential on a removed line not flagged")
	}
}

// TestSnapshotWorktreeHookDrift checks that a linked worktree still sees the
// real repository's git config and hooks, which live in the common git dir
// rather than in the worktree's own .git directory.
func TestSnapshotWorktreeHookDrift(t *testing.T) {
	repo := initRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")
	runGit(t, repo, "worktree", "add", "-b", "wtbranch", wt)
	before := verify.SnapshotRepo(wt)
	if before["@ERR"] != "" {
		t.Fatalf("snapshot of linked worktree failed: %s", before["@ERR"])
	}
	hook := filepath.Join(repo, ".git", "hooks", "post-commit")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	after := verify.SnapshotRepo(wt)
	drift := verify.SnapshotDrift(before, after)
	for _, d := range drift {
		if d == "@HOOK:post-commit" {
			return
		}
	}
	t.Fatalf("hook in the common git dir not detected from a linked worktree: %v", drift)
}

// TestSnapshotQuotedIgnoredName checks that a gitignored file whose name git
// would quote is still fingerprinted, so rewriting it counts as drift.
func TestSnapshotQuotedIgnoredName(t *testing.T) {
	repo := initRepo(t)
	ignore := filepath.Join(repo, ".gitignore")
	if err := os.WriteFile(ignore, []byte("*.env\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", ".gitignore")
	runGit(t, repo, "commit", "-q", "-m", "ignore")
	hidden := filepath.Join(repo, "we\"ird.env")
	if err := os.WriteFile(hidden, []byte("MODE=600\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := verify.SnapshotRepo(repo)
	if err := os.WriteFile(hidden, []byte("MODE=600\nrewritten\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	after := verify.SnapshotRepo(repo)
	drift := verify.SnapshotDrift(before, after)
	for _, d := range drift {
		if d == "@IGNORED" {
			return
		}
	}
	t.Fatalf("rewritten gitignored file with a quoted name not detected: %v", drift)
}

