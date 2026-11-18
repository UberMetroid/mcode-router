// Package acceptance holds black-box refusal tests for the router's trust
// boundaries.
//
// Every test drives exported API only, so a guard cannot be bypassed by
// reaching into package internals. No test touches the network: fake agent
// CLIs on PATH stand in for the real ones.
package acceptance

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/UberMetroid/mcode-router/internal/cli"
	"github.com/UberMetroid/mcode-router/internal/fleet"
)

// runGit runs git in dir with a fixed identity so commits never need config.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{"-c", "user.name=t", "-c", "user.email=t@invalid",
		"-c", "commit.gpgsign=false"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// initRepo creates a git repo with one committed file and returns its path.
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "a.go")
	runGit(t, dir, "commit", "-q", "-m", "seed")
	return dir
}

// diffBody wraps added lines in a unified diff for a single file.
func diffBody(path string, lines ...string) string {
	var b strings.Builder
	b.WriteString("diff --git a/" + path + " b/" + path + "\n")
	b.WriteString("--- a/" + path + "\n+++ b/" + path + "\n")
	b.WriteString("@@ -0,0 +1," + strconv.Itoa(len(lines)) + " @@\n")
	for _, ln := range lines {
		b.WriteString("+" + ln + "\n")
	}
	return b.String()
}

// cliEnv builds an isolated home, git repo, quota file and bin directory that
// holds a fake agent CLI, then returns a RunCLI caller.
func cliEnv(t *testing.T, fake string) func(...string) (int, string, string) {
	t.Helper()
	tmp := t.TempDir()
	home, repo, binDir := filepath.Join(tmp, "home"), filepath.Join(tmp, "repo"), filepath.Join(tmp, "bin")
	for _, d := range []string{home, repo, binDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	runGit(t, repo, "init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "a.go")
	runGit(t, repo, "commit", "-q", "-m", "seed")
	qPath := filepath.Join(tmp, "quota.json")
	if err := fleet.AtomicWriteJSON(qPath, fleet.DefaultQuotaData(), false, true); err != nil {
		t.Fatal(err)
	}
	fakeCLI := filepath.Join(binDir, "devin")
	if err := os.WriteFile(fakeCLI, []byte("#!/bin/sh\n"+fake+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{
		"HOME": home, "AGENT_QUOTA_FILE": qPath, "OODA_NO_AUTO_PROBE": "1", "NO_COLOR": "1",
		"OODA_DISCOVERY_FILE": filepath.Join(tmp, "discovery.json"),
		"ROUTER_SCRATCH_DIR":  filepath.Join(tmp, "hud"),
		"ROUTER_SLOTS_DIR":    filepath.Join(tmp, "slots"),
		"MCODE_ROUTER_DEPTH":  "0", "ROUTER_DEPTH": "0",
	} {
		t.Setenv(k, v)
	}
	gitBin, _ := exec.LookPath("git")
	t.Setenv("PATH", binDir+":"+filepath.Dir(gitBin)+":/bin:/usr/bin")
	oldWd, _ := os.Getwd()
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	return func(argv ...string) (int, string, string) {
		var out, errB bytes.Buffer
		rc := cli.RunCLI("mcode-router", argv, &out, &errB)
		return rc, out.String(), errB.String()
	}
}
