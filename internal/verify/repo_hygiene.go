package verify

import (
	"bytes"
	"fmt"
	"github.com/UberMetroid/mcode-router/internal/worktree"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/UberMetroid/mcode-router/internal/fleet"
)

func listHygieneFiles(root string, files []string) ([]string, error) {
	var cands, kept []string
	if fleet.TryRepoRoot(root) != "" {
		raw, ok := gitOutOK(root, "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--", ".")
		if !ok {
			return nil, fmt.Errorf("hygiene: git ls-files failed in %s", root)
		}
		cands = strings.Split(raw, "\x00")
	} else if err := filepath.WalkDir(root, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() && p != root && skipHygieneDirs[d.Name()] {
			return filepath.SkipDir
		}
		if rel, rerr := filepath.Rel(root, p); !d.IsDir() && rerr == nil && rel != "." {
			cands = append(cands, filepath.ToSlash(rel))
		}
		return nil
	}); err != nil {
		return nil, err
	}
	for _, raw := range cands {
		rel, err := worktree.NormalizeRelPath(raw)
		if err != nil || rel == "" || worktree.IsIgnoredArtifact(rel) ||
			(len(files) > 0 && len(worktree.FindScopeViolations([]string{rel}, files)) > 0) {
			continue
		}
		if kept = append(kept, rel); len(kept) > maxSnapshotFiles {
			return nil, fmt.Errorf("hygiene: file count exceeds limit %d", maxSnapshotFiles)
		}
	}
	sort.Strings(kept)
	return kept, nil
}

// ScanRepoHygiene performs a bounded deterministic P0 secret + P1 hardening scan.
func ScanRepoHygiene(root string, files []string) ([]HygieneFinding, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	list, err := listHygieneFiles(absRoot, files)
	if err != nil {
		return nil, err
	}
	var out []HygieneFinding
	for _, rel := range list {
		full := filepath.Join(absRoot, filepath.FromSlash(rel))
		if st, lerr := os.Lstat(full); lerr != nil || !st.Mode().IsRegular() {
			continue
		}
		if isSecretPath(rel) {
			out = append(out, HygieneFinding{"P0", "secret-file", rel, "sensitive credential filename", 0})
		}
		raw, rerr := readBounded(full, hygieneMaxBytes)
		if rerr != nil || bytes.IndexByte(raw[:min(len(raw), sniffBytes)], 0) >= 0 {
			continue
		}
		for idx, ln := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
			out = append(out, scanLine(rel, idx+1, ln, !p1SkipFile(rel))...)
		}
	}
	return out, nil
}
