package verify

import (
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/UberMetroid/mcode-router/internal/fleet"
	"github.com/UberMetroid/mcode-router/internal/worktree"
)

const snapMaxTotalBytes, snapMaxFileBytes, maxAnchorFiles = int64(2 << 30), int64(512 << 20), 512

var maxSnapshotFiles = 8192

func fileFingerprint(full string, rem *int64) string {
	if st, err := os.Lstat(full); err != nil {
		return "missing"
	} else if st.Mode()&os.ModeSymlink != 0 {
		dest, _ := os.Readlink(full)
		return "link:" + dest
	} else if !st.Mode().IsRegular() {
		return fmt.Sprintf("mode:%o", st.Mode())
	} else if fh, err := os.Open(full); err != nil {
		return "unreadable"
	} else {
		defer fh.Close()
		capB, h := max(int64(0), min(*rem, snapMaxFileBytes)), sha256.New()
		if capB > 0 {
			n, cerr := io.Copy(h, io.LimitReader(fh, capB))
			*rem -= n
			if cerr != nil {
				return "unreadable"
			}
		}
		if st.Size() > capB {
			return fmt.Sprintf("trunc:%d:%o:%d:%x", st.Size(), st.Mode().Perm(), st.ModTime().UnixNano(), h.Sum(nil))
		}
		return fmt.Sprintf("%d:%o:%x", st.Size(), st.Mode().Perm(), h.Sum(nil))
	}
}

func runtimeFilter(extra ...string) func(string) bool {
	qp := fleet.QuotaFilePath("")
	cands := []string{fleet.ScratchDirPath(), fleet.SlotsRootPath(), qp, strings.TrimSuffix(qp, filepath.Ext(qp)) + ".lock"}
	for _, p := range extra {
		if strings.TrimSpace(p) != "" {
			cands = append(cands, p, strings.TrimSuffix(p, filepath.Ext(p))+".lock")
		}
	}
	var ex []string
	for _, p := range cands {
		if a, err := filepath.Abs(p); err == nil && a != "" {
			ex = append(ex, a)
		}
	}
	return func(ap string) bool {
		for _, r := range ex {
			if ap == r || strings.HasPrefix(ap, r+string(filepath.Separator)) {
				return true
			}
		}
		return false
	}
}

func snapshotGit(root string, rem *int64, skipRt func(string) bool) map[string]string {
	head := HeadRef(root)
	raw, ok := gitOutOK(root, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if !ok || head == "" {
		return map[string]string{"@ERR": "git-snapshot-failed"}
	}
	selfRef, _ := gitOutOK(root, "symbolic-ref", "-q", "HEAD")
	refs, _ := gitOutOK(root, "show-ref", "--head")
	if selfRef = strings.TrimSpace(selfRef); strings.HasPrefix(selfRef, "refs/heads/ooda/") {
		wtOut, _ := gitOutOK(root, "worktree", "list", "--porcelain")
		wtBranches, keep := map[string]bool{}, []string(nil)
		for _, ln := range strings.Split(wtOut, "\n") {
			if br, ok := strings.CutPrefix(strings.TrimSpace(ln), "branch "); ok && br != selfRef {
				wtBranches[br] = true
			}
		}
		for _, ln := range strings.Split(refs, "\n") {
			if _, br, ok := strings.Cut(ln, " "); ok && strings.HasPrefix(br, "refs/heads/ooda/") && wtBranches[br] {
				continue
			}
			keep = append(keep, ln)
		}
		refs = strings.Join(keep, "\n")
	}
	rs := sha256.Sum256([]byte(refs))
	snap, count := map[string]string{"@HEAD": head, "@REFS": fmt.Sprintf("%x", rs[:16])}, 0
	parts := strings.Split(raw, "\x00")
	for i := 0; i < len(parts); i++ {
		rec := parts[i]
		if len(rec) < 4 {
			continue
		}
		xy, paths := rec[:2], [][2]string{{"", rec[3:]}}
		if (xy[0] == 'R' || xy[0] == 'C' || xy[1] == 'R' || xy[1] == 'C') && i+1 < len(parts) {
			i++
			paths = append(paths, [2]string{"orig:", parts[i]})
		}
		for _, pk := range paths {
			rel, err := worktree.NormalizeRelPath(strings.TrimSuffix(pk[1], "/"))
			if full := filepath.Join(root, filepath.FromSlash(rel)); err == nil && rel != "" &&
				!worktree.IsIgnoredArtifact(rel) && !skipRt(full) {
				if pk[0] == "" {
					count++
				}
				if count > maxSnapshotFiles {
					return map[string]string{"@ERR": "too-many-files"}
				}
				snap[pk[0]+rel] = xy + "|" + fileFingerprint(full, rem)
			}
		}
	}
	return snap
}

func snapshotDir(dir string, rem *int64, skipRt func(string) bool) map[string]string {
	if a, err := filepath.Abs(dir); err == nil {
		dir = a
	}
	snap, count := map[string]string{"@MODE": "dir"}, 0
	if err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		rel, rerr := filepath.Rel(dir, p)
		if rerr != nil {
			return rerr
		}
		if rel = filepath.ToSlash(rel); rel == "." {
			return nil
		}
		if d.Name() == ".git" || worktree.IsIgnoredArtifact(rel) || skipRt(p) {
			return map[bool]error{true: filepath.SkipDir}[d.IsDir()]
		}
		if d.IsDir() {
			return nil
		}
		if count++; count > maxSnapshotFiles {
			return fmt.Errorf("too many files")
		}
		snap[rel] = fileFingerprint(p, rem)
		return nil
	}); err != nil {
		return map[string]string{"@ERR": "walk-failed"}
	}
	return snap
}

func recordGuardAnchors(targetDir string, snap map[string]string, rem *int64, skipRt func(string) bool) {
	canon := func(p string) string {
		a, _ := filepath.Abs(p)
		if r, err := filepath.EvalSymlinks(a); err == nil {
			return r
		}
		return a
	}
	tAbs := canon(targetDir)
	inTarget := func(p string) bool {
		rel, err := filepath.Rel(tAbs, canon(p))
		return skipRt(p) || (err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
	}
	cnt := 0
	home, _ := os.UserHomeDir()
	addFile := func(key, p string, statOnly bool) {
		st, err := os.Lstat(p)
		if inTarget(p) || cnt >= maxAnchorFiles || err != nil || st.IsDir() {
			return
		}
		cnt++
		if statOnly {
			snap["@GUARD:"+key] = fmt.Sprintf("stat:%d:%o:%d", st.Size(), st.Mode().Perm(), st.ModTime().UnixNano())
			return
		}
		snap["@GUARD:"+key] = fileFingerprint(p, rem)
	}
	dirs := [][2]string{{"workspaces", filepath.Join(filepath.Dir(fleet.QuotaFilePath("")), "workspaces")},
		{"plugin", fleet.PluginRootPath()}}
	if home != "" {
		for _, f := range []string{".bashrc", ".bash_profile", ".profile", ".zshrc", ".zprofile", ".gitconfig"} {
			addFile("home/"+f, filepath.Join(home, f), false)
		}
		for _, f := range []string{".netrc", ".git-credentials"} {
			addFile("home/"+f, filepath.Join(home, filepath.FromSlash(f)), true)
		}
		dirs = append(dirs, [2]string{"agents", filepath.Join(home, ".agents")})
	}
	for _, d := range dirs {
		if !inTarget(d[1]) {
			_ = filepath.WalkDir(d[1], func(p string, ent fs.DirEntry, werr error) error {
				if cnt >= maxAnchorFiles {
					return fs.SkipAll
				}
				if werr == nil && !ent.IsDir() && !skipRt(p) {
					rel, _ := filepath.Rel(d[1], p)
					addFile(d[0]+"/"+filepath.ToSlash(rel), p, false)
				}
				return nil
			})
		}
	}
}

// SnapshotRepo captures repo-wide git HEAD/refs/porcelain state or bounded non-git directory state.
func SnapshotRepo(dir string, extra ...string) map[string]string {
	if strings.TrimSpace(dir) == "" {
		return nil
	}
	rem, skipRt, snap := snapMaxTotalBytes, runtimeFilter(extra...), map[string]string(nil)
	if root := fleet.TryRepoRoot(dir); root != "" {
		snap = snapshotGit(root, &rem, skipRt)
	} else {
		snap = snapshotDir(dir, &rem, skipRt)
	}
	if snap != nil && snap["@ERR"] == "" {
		recordGuardAnchors(dir, snap, &rem, skipRt)
	}
	return snap
}

// SnapshotDrift fails closed on snapshot errors and returns sorted modified/mutated paths.
func SnapshotDrift(before, after map[string]string) []string {
	if before == nil && after == nil {
		return nil
	}
	if len(before) == 0 || len(after) == 0 || before["@ERR"] != "" || after["@ERR"] != "" {
		return []string{"@SNAPSHOT_FAILED (unable to verify read-only workspace)"}
	}
	seen, drift := make(map[string]bool, len(before)+len(after)), []string(nil)
	for _, m := range []map[string]string{before, after} {
		for k := range m {
			if !seen[k] && before[k] != after[k] {
				msg := map[string]string{
					"@HEAD": "@HEAD (unexpected git commit)", "@REFS": "@REFS (unexpected git ref/branch/stash change)",
				}[k]
				drift = append(drift, map[bool]string{true: msg, false: k}[msg != ""])
			}
			seen[k] = true
		}
	}
	sort.Strings(drift)
	return drift
}
