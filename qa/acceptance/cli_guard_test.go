package acceptance

import (
	"strings"
	"testing"
)

// TestReadOnlyWithholdsOnDrift checks that a read-only worker that mutates
// the workspace has its output withheld rather than disclosed first.
func TestReadOnlyWithholdsOnDrift(t *testing.T) {
	run := cliEnv(t, "printf 'CANARY-OUTPUT\\n'; echo drift > drifted.txt")
	rc, out, errS := run("--recon", "probe", "--lane", "devin-fast")
	if strings.Contains(out, "CANARY-OUTPUT") {
		t.Fatalf("worker output disclosed despite drift: out=%q err=%q", out, errS)
	}
	if rc == 0 {
		t.Fatalf("read-only drift not failed closed: out=%q err=%q", out, errS)
	}
}

// TestNonGitCwdFailsClosed checks that a task against a target that is not a
// git repository is refused instead of being reported as a verified success,
// while an explicit opt-out still allows the unverified run.
func TestNonGitCwdFailsClosed(t *testing.T) {
	run := cliEnv(t, "exit 0")
	plain := t.TempDir()
	rc, out, errS := run("--task", "do work", "--cwd", plain, "--lane", "devin-fast")
	if rc == 0 {
		t.Fatalf("non-git task reported success with no verification: out=%q err=%q", out, errS)
	}
	if !strings.Contains(strings.ToLower(out+errS), "git") {
		t.Fatalf("non-git refusal not explained: out=%q err=%q", out, errS)
	}
	rc2, out2, err2 := run("--task", "do work", "--cwd", plain, "--lane", "devin-fast", "--no-review")
	if rc2 != 0 {
		t.Fatalf("opt-out should still allow an unverified task: rc=%d out=%q err=%q", rc2, out2, err2)
	}
}
