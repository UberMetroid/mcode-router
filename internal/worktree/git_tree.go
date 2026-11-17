package worktree

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/UberMetroid/mcode-router/internal/fleet"
)

const gitTimeoutSec = 120

// EnsureSlot creates a private runtime scratch directory for prompts/exports.
func EnsureSlot(slotKey string) (string, error) {
	slot := filepath.Join(fleet.SlotsRootPath(), "slot-"+fleet.SafeID(slotKey))
	if err := os.MkdirAll(slot, 0o700); err != nil {
		return "", err
	}
	_ = os.Chmod(slot, 0o700)
	return slot, nil
}

// EnsureWorktree creates .worktrees/<taskID> on a clean branch ooda/<taskID> at HEAD.
func EnsureWorktree(repoRoot, taskID string) (string, string, error) {
	if !fleet.SafeIDRe.MatchString(taskID) {
		return "", "", fleet.UsageErr("error: invalid task id %q", taskID)
	}
	rootAbs, err := filepath.Abs(repoRoot)
	if err != nil {
		return "", "", err
	}
	wtRoot := filepath.Join(rootAbs, ".worktrees")
	if err := os.MkdirAll(wtRoot, 0o755); err != nil {
		return "", "", err
	}
	wtPath := filepath.Join(wtRoot, taskID)
	branch := "ooda/" + taskID
	if _, err := os.Stat(wtPath); err == nil {
		if VerifyWorktreeBinding(rootAbs, wtPath) != nil || len(ChangedFilesIn(wtPath, nil)) > 0 {
			return "", "", fleet.UsageErr("error: existing worktree %s has uncommitted/tampered state", wtPath)
		}
		_, _ = fleet.RunCmd(fleet.CmdSpec{
			Argv: []string{"git", "worktree", "remove", "--force", wtPath},
			Cwd:  rootAbs, TimeoutSec: gitTimeoutSec,
		})
		_ = os.RemoveAll(wtPath)
	}
	argv := []string{"git", "worktree", "add", "-B", branch, wtPath, "HEAD"}
	res, runErr := fleet.RunCmd(fleet.CmdSpec{Argv: argv, Cwd: rootAbs, TimeoutSec: gitTimeoutSec})
	if runErr != nil || res.ExitCode != 0 {
		return "", "", fleet.UsageErr("error: worktree add failed for %s:\n%s", taskID, res.Combined)
	}
	return wtPath, branch, nil
}

// DirtyState fingerprints modified/staged/untracked files over a dirty baseline.
func DirtyState(wt string) map[string]string {
	snap := make(map[string]string)
	for _, rel := range ChangedFilesIn(wt, nil) {
		target := filepath.Join(wt, filepath.FromSlash(rel))
		st, err := os.Lstat(target)
		if err != nil {
			snap[rel] = "missing"
			continue
		}
		if st.Mode()&os.ModeSymlink != 0 {
			dest, _ := os.Readlink(target)
			snap[rel] = "link:" + dest
			continue
		}
		if !st.Mode().IsRegular() {
			snap[rel] = "missing"
			continue
		}
		f, rerr := os.Open(target)
		if rerr != nil {
			snap[rel] = "missing"
			continue
		}
		h := sha256.New()
		_, cerr := io.Copy(h, io.LimitReader(f, 8<<20))
		_ = f.Close()
		if cerr != nil {
			snap[rel] = "missing"
			continue
		}
		fmt.Fprintf(h, ":%d", st.Size())
		snap[rel] = hex.EncodeToString(h.Sum(nil))
	}
	return snap
}

// ChangedFilesIn returns sorted relative paths changed relative to base (or HEAD).
func ChangedFilesIn(wt string, base map[string]string) []string {
	if base != nil {
		now := DirtyState(wt)
		var diff []string
		for rel, sig := range now {
			if old, ok := base[rel]; !ok || old != sig {
				diff = append(diff, rel)
			}
		}
		for rel, old := range base {
			if _, ok := now[rel]; !ok && old != "missing" {
				diff = append(diff, rel)
			}
		}
		sort.Strings(diff)
		return diff
	}
	queries := [][]string{
		{"git", "diff", "-z", "--name-only", "HEAD", "--", "."},
		{"git", "diff", "-z", "--cached", "--name-only", "--", "."},
		{"git", "ls-files", "-z", "--others", "--exclude-standard", "--", "."},
	}
	seen := make(map[string]bool)
	for _, argv := range queries {
		res, err := fleet.RunCmd(fleet.CmdSpec{Argv: argv, Cwd: wt, TimeoutSec: gitTimeoutSec})
		if err != nil || res.ExitCode != 0 {
			continue
		}
		for _, entry := range strings.Split(res.Stdout, "\x00") {
			if entry == "" {
				continue
			}
			rel, rerr := NormalizeRelPath(entry)
			if rerr == nil && !IsIgnoredArtifact(rel) {
				seen[rel] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// FindScopeViolations returns changed paths outside declared scope entries.
func FindScopeViolations(changed, declared []string) []string {
	if len(declared) == 0 {
		return nil
	}
	var allowed []string
	for _, d := range declared {
		if norm, err := NormalizeRelPath(d); err == nil && norm != "" {
			allowed = append(allowed, strings.TrimRight(norm, "/"))
		}
	}
	var viols []string
	for _, cf := range changed {
		ok := false
		for _, df := range allowed {
			if cf == df || strings.HasPrefix(cf, df+"/") {
				ok = true
				break
			}
		}
		if !ok {
			viols = append(viols, cf)
		}
	}
	return viols
}

// DiffStat returns a compact git diff --stat and short status summary.
func DiffStat(wt string) string {
	p, err := fleet.RunCmd(fleet.CmdSpec{
		Argv: []string{"git", "diff", "--stat", "HEAD", "--", "."}, Cwd: wt, TimeoutSec: gitTimeoutSec,
	})
	stat := "<diff failed>"
	if err == nil && p.ExitCode == 0 {
		stat = strings.TrimSpace(p.Stdout)
	}
	s, serr := fleet.RunCmd(fleet.CmdSpec{
		Argv: []string{"git", "status", "--short", "--", "."}, Cwd: wt, TimeoutSec: gitTimeoutSec,
	})
	if serr == nil && s.ExitCode == 0 && strings.TrimSpace(s.Stdout) != "" {
		if stat != "" {
			stat += "\n"
		}
		stat += "-- untracked/modified --\n" + strings.TrimSpace(s.Stdout)
	}
	if strings.TrimSpace(stat) == "" {
		return "(clean)"
	}
	return stat
}

// SafeWorktreeFiles resolves existing declared files contained within worktree.
func SafeWorktreeFiles(files []string, wt string) []string {
	wtReal, err := filepath.EvalSymlinks(wt)
	if err != nil {
		wtReal, _ = filepath.Abs(wt)
	}
	var out []string
	for _, f := range files {
		cand := filepath.Join(wt, filepath.FromSlash(f))
		candReal, rerr := filepath.EvalSymlinks(cand)
		if rerr != nil {
			continue
		}
		rel, relErr := filepath.Rel(wtReal, candReal)
		if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			continue
		}
		if st, serr := os.Stat(cand); serr == nil && st.Mode().IsRegular() {
			out = append(out, cand)
		}
	}
	return out
}

// VerifyWorktreeBinding verifies wt resolves to its worktree root and repo common gitdir.
func VerifyWorktreeBinding(root, wt string) error {
	st, err := os.Lstat(filepath.Join(wt, ".git"))
	if err != nil || !st.Mode().IsRegular() {
		return fmt.Errorf("worktree .git pointer missing or non-regular")
	}
	res, err := fleet.RunCmd(fleet.CmdSpec{
		Argv: []string{"git", "rev-parse", "--show-toplevel", "--git-common-dir"}, Cwd: wt, TimeoutSec: 15,
	})
	if err != nil || res.ExitCode != 0 {
		return fmt.Errorf("worktree git binding check failed")
	}
	lines := strings.Split(strings.TrimSpace(res.Stdout), "\n")
	if len(lines) < 2 {
		return fmt.Errorf("incomplete git rev-parse output")
	}
	normReal := func(base, p string) string {
		if !filepath.IsAbs(p) {
			p = filepath.Join(base, p)
		}
		if r, rerr := filepath.EvalSymlinks(p); rerr == nil {
			return r
		}
		abs, _ := filepath.Abs(p)
		return abs
	}
	gotCommon := normReal(wt, strings.TrimSpace(lines[1]))
	expRes, eerr := fleet.RunCmd(fleet.CmdSpec{
		Argv: []string{"git", "rev-parse", "--git-common-dir"}, Cwd: root, TimeoutSec: 15,
	})
	if eerr != nil || expRes.ExitCode != 0 || normReal(wt, strings.TrimSpace(lines[0])) != normReal(wt, ".") ||
		gotCommon != normReal(root, strings.TrimSpace(expRes.Stdout)) {
		return fmt.Errorf("worktree %s escaped repository git binding", wt)
	}
	return nil
}
