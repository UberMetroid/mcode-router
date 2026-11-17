package worktree

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/UberMetroid/mcode-router/internal/fleet"
)

func removeRepoWorktrees(root, wtDir string, outW io.Writer) []string {
	var ids []string
	if raw, err := os.ReadFile(filepath.Join(wtDir, "results.json")); err == nil {
		var rows []struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(raw, &rows) == nil {
			for _, r := range rows {
				if fleet.SafeIDRe.MatchString(r.ID) {
					ids = append(ids, r.ID)
				}
			}
		}
	}
	st, err := os.Stat(wtDir)
	if err != nil || !st.IsDir() {
		return ids
	}
	lst, _ := fleet.RunCmd(fleet.CmdSpec{
		Argv: []string{"git", "worktree", "list", "--porcelain"}, Cwd: root, TimeoutSec: 60,
	})
	for _, ln := range strings.Split(lst.Stdout, "\n") {
		if !strings.HasPrefix(ln, "worktree ") {
			continue
		}
		wt := strings.TrimSpace(ln[len("worktree "):])
		if rel, rerr := filepath.Rel(wtDir, wt); rerr == nil && !strings.HasPrefix(rel, "..") {
			_, _ = fleet.RunCmd(fleet.CmdSpec{
				Argv: []string{"git", "worktree", "remove", "--force", wt}, Cwd: root, TimeoutSec: 60,
			})
			fmt.Fprintf(outW, "removed worktree %s\n", wt)
		}
	}
	if des, derr := os.ReadDir(wtDir); derr == nil {
		for _, de := range des {
			if de.IsDir() && fleet.SafeIDRe.MatchString(de.Name()) {
				ids = append(ids, de.Name())
			}
		}
	}
	return ids
}

// Cleanup removes .worktrees directories and temporary ooda/* branches under root.
func Cleanup(root string, outW io.Writer) {
	wtDir := filepath.Join(root, ".worktrees")
	ids := removeRepoWorktrees(root, wtDir, outW)
	_, _ = fleet.RunCmd(fleet.CmdSpec{Argv: []string{"git", "worktree", "prune"}, Cwd: root, TimeoutSec: 60})
	if st, err := os.Stat(wtDir); err == nil && st.IsDir() {
		_ = os.RemoveAll(wtDir)
		fmt.Fprintf(outW, "removed %s\n", wtDir)
	}
	seen := make(map[string]bool)
	for _, tid := range ids {
		if seen[tid] {
			continue
		}
		seen[tid] = true
		r, _ := fleet.RunCmd(fleet.CmdSpec{
			Argv: []string{"git", "branch", "-D", "ooda/" + tid}, Cwd: root, TimeoutSec: 60,
		})
		if r.ExitCode == 0 {
			fmt.Fprintf(outW, "deleted branch ooda/%s\n", tid)
		}
	}
	fmt.Fprintf(outW, "cleanup done (slots in %s kept for warm caches)\n", fleet.SlotsRootPath())
}
