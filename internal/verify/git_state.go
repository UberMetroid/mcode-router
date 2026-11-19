package verify

import (
	"github.com/UberMetroid/mcode-router/internal/fleet"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func gitOutOK(cwd string, args ...string) (string, bool) {
	argv := append([]string{"git"}, args...)
	res, err := fleet.RunCmd(fleet.CmdSpec{
		Argv:       argv,
		Cwd:        cwd,
		TimeoutSec: gitTimeout,
	})
	if err != nil || res.ExitCode != 0 {
		return "", false
	}
	return res.Stdout, true
}

// gitDirs resolves the shared .git directory that holds config and hooks, plus
// the per-worktree .git directory. A linked worktree's own directory holds
// neither, so reading config and hooks from it alone would miss everything a
// worker planted in the real repository.
var gitDirs = func(root string) (common, work string) {
	if raw, ok := gitOutOK(root, "rev-parse", "--absolute-git-dir"); ok {
		work = strings.TrimSpace(raw)
	}
	if raw, ok := gitOutOK(root, "rev-parse", "--path-format=absolute", "--git-common-dir"); ok {
		common = strings.TrimSpace(raw)
	} else if raw, ok := gitOutOK(root, "rev-parse", "--git-common-dir"); ok {
		common = strings.TrimSpace(raw)
		if common != "" && !filepath.IsAbs(common) {
			common = filepath.Join(root, common)
		}
	}
	if common == "" {
		common = work
	}
	if work == "" {
		work = common
	}
	return common, work
}

// gitHookState fingerprints the hook files git would run for this repository:
// the repository's own hooks directory and, when configured, the
// core.hooksPath directory. Both are walked because a hook planted in either
// one runs on the user's next commit. The walk is bounded in depth, in file
// count and in how far down the tree it may reach, because core.hooksPath is
// worker-controlled and could otherwise point at an arbitrary tree.
var gitHookState = func(root, common string, rem *int64, out map[string]string) {
	dirs := [][2]string{{filepath.Join(common, "hooks"), "@HOOK:"}}
	if hp, hok := gitOutOK(root, "config", "--get", "core.hooksPath"); hok {
		if hp = strings.TrimSpace(hp); hp != "" {
			if !filepath.IsAbs(hp) {
				hp = filepath.Join(root, hp)
			}
			if hp != dirs[0][0] {
				dirs = append(dirs, [2]string{hp, "@HOOKPATH:"})
			}
		}
	}
	for _, d := range dirs {
		hdir, prefix := d[0], d[1]
		if len(strings.Split(filepath.Clean(hdir), string(filepath.Separator))) < 3 {
			continue
		}
		out[prefix+"DIR"] = fileFingerprint(hdir, rem)
		n, base := 0, strings.Count(hdir, string(filepath.Separator))
		_ = filepath.WalkDir(hdir, func(p string, e fs.DirEntry, werr error) error {
			if werr != nil || e.IsDir() {
				return nil
			}
			if n >= maxAnchorFiles || strings.Count(p, string(filepath.Separator)) > base+4 {
				return fs.SkipAll
			}
			n++
			if rel, rerr := filepath.Rel(hdir, p); rerr == nil {
				out[prefix+filepath.ToSlash(rel)] = fileFingerprint(p, rem)
			}
			return nil
		})
	}
}

func readBounded(path string, limit int64) ([]byte, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	return io.ReadAll(io.LimitReader(fh, limit+1))
}
