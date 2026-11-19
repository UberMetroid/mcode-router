package verify

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/UberMetroid/mcode-router/internal/worktree"
)

const (
	// NativeReviewMaxBytes caps working-tree patches eligible for native review.
	NativeReviewMaxBytes = 8192
	// MaxDiffRunes caps combined inline patch runes sent to a reviewer.
	MaxDiffRunes = 32000
	gitTimeout   = 60
	sniffBytes   = 8192
	maxUntracked = 65536
)

// DiffContext holds gathered git status, diff stat, and unified patch text.
type DiffContext struct {
	Source       string
	Status       string
	DiffStat     string
	Diff         string
	ChangedFiles []string
	HasChanges   bool
}

func formatUntrackedBody(rel, head string, raw []byte, size int64) string {
	if bytes.IndexByte(raw[:min(len(raw), sniffBytes)], 0) >= 0 {
		return head + fmt.Sprintf("Binary files /dev/null and b/%s differ (%d bytes)\n", rel, size)
	}
	if len(raw) > maxUntracked {
		return head + fmt.Sprintf("<untracked text file too large to inline: %d bytes; inspect in cwd>\n", size)
	}
	if !utf8.Valid(raw) {
		return head + fmt.Sprintf("<untracked non-UTF-8 file: %d bytes; inspect in cwd>\n", size)
	}
	text := string(raw)
	if text == "" {
		return head
	}
	lines := strings.Split(text, "\n")
	if strings.HasSuffix(text, "\n") {
		lines = lines[:len(lines)-1]
	}
	var b strings.Builder
	b.WriteString(head)
	fmt.Fprintf(&b, "--- /dev/null\n+++ b/%s\n@@ -0,0 +1,%d @@\n", rel, len(lines))
	for _, ln := range lines {
		b.WriteByte('+')
		b.WriteString(ln)
		b.WriteByte('\n')
	}
	if !strings.HasSuffix(text, "\n") {
		b.WriteString("\\ No newline at end of file\n")
	}
	return b.String()
}

func syntheticFileDiff(targetDir, rel string) string {
	full := filepath.Join(targetDir, filepath.FromSlash(rel))
	head := fmt.Sprintf("diff --git a/%s b/%s\n", rel, rel)
	st, err := os.Lstat(full)
	if err != nil {
		return head + fmt.Sprintf("new file mode 100644\n<untracked file unreadable: %v>\n", err)
	}
	if st.Mode()&os.ModeSymlink != 0 {
		dest, _ := os.Readlink(full)
		return head + fmt.Sprintf("new file mode 120000\n<untracked symlink -> %s>\n", dest)
	}
	if !st.Mode().IsRegular() {
		return head + fmt.Sprintf("new file mode %o\n<untracked non-regular file (%s) skipped>\n", st.Mode(), st.Mode().Type())
	}
	raw, rerr := readBounded(full, maxUntracked)
	if rerr != nil {
		return head + fmt.Sprintf("new file mode 100644\n<untracked file unreadable: %v>\n", rerr)
	}
	mode := "100644"
	if st.Mode()&0o100 != 0 {
		mode = "100755"
	}
	head += fmt.Sprintf("new file mode %s\n", mode)
	return formatUntrackedBody(rel, head, raw, st.Size())
}

func untrackedDiff(targetDir string, budget int) string {
	out, ok := gitOutOK(targetDir, "ls-files", "--others", "--exclude-standard", "-z", "--", ".")
	if !ok {
		return ""
	}
	var b strings.Builder
	used := 0
	skipped := 0
	for _, rawRel := range strings.Split(out, "\x00") {
		if rawRel == "" {
			continue
		}
		norm, err := worktree.NormalizeRelPath(rawRel)
		if err != nil || worktree.IsIgnoredArtifact(norm) {
			continue
		}
		if used >= budget {
			skipped++
			continue
		}
		chunk := syntheticFileDiff(targetDir, norm)
		b.WriteString(chunk)
		used += len(chunk)
	}
	if skipped > 0 {
		fmt.Fprintf(&b, "... <%d more untracked file(s) omitted; inspect files in cwd> ...\n", skipped)
	}
	return b.String()
}

// HeadRef returns HEAD commit SHA, empty-tree SHA on unborn HEAD, or "" outside git.
func HeadRef(targetDir string) string {
	if _, inGit := gitOutOK(targetDir, "rev-parse", "--git-dir"); !inGit {
		return ""
	}
	if sha, ok := gitOutOK(targetDir, "rev-parse", "--verify", "--quiet", "HEAD"); ok {
		return strings.TrimSpace(sha)
	}
	if empty, ok := gitOutOK(targetDir, "hash-object", "-t", "tree", "/dev/null"); ok {
		return strings.TrimSpace(empty)
	}
	return ""
}

func changedAgainst(targetDir, ref string) []string {
	queries := [][]string{
		{"diff", "-z", "--name-only", ref, "--", "."},
		{"diff", "-z", "--cached", "--name-only", ref, "--", "."},
		{"ls-files", "-z", "--others", "--exclude-standard", "--", "."},
	}
	seen := make(map[string]bool)
	for _, args := range queries {
		out, ok := gitOutOK(targetDir, args...)
		if !ok {
			continue
		}
		for _, raw := range strings.Split(out, "\x00") {
			if raw == "" {
				continue
			}
			if norm, err := worktree.NormalizeRelPath(raw); err == nil && !worktree.IsIgnoredArtifact(norm) {
				seen[norm] = true
			}
		}
	}
	files := make([]string, 0, len(seen))
	for k := range seen {
		files = append(files, k)
	}
	sort.Strings(files)
	return files
}

// CollectDiffContext gathers git status, stat, and patch (against optional baseRef or HEAD).
func CollectDiffContext(targetDir string, includeCommit bool, baseRefOpt ...string) DiffContext {
	customBase := len(baseRefOpt) > 0 && strings.TrimSpace(baseRefOpt[0]) != ""
	baseRef := HeadRef(targetDir)
	source := "working-tree"
	var changed []string
	if customBase {
		baseRef = strings.TrimSpace(baseRefOpt[0])
		source = "commit..working-tree"
	}
	if baseRef != "" {
		changed = changedAgainst(targetDir, baseRef)
	} else {
		baseRef = "HEAD"
	}
	rawStatus, _ := gitOutOK(targetDir, "status", "--short", "--", ".")
	statusOut := strings.TrimSpace(rawStatus)
	rawStat, _ := gitOutOK(targetDir, "diff", "--stat", baseRef, "--", ".")
	diffStatOut := strings.TrimSpace(rawStat)
	diffOut, _ := gitOutOK(targetDir, "diff", baseRef, "--", ".")
	if statusOut != "" {
		diffOut += untrackedDiff(targetDir, 4*MaxDiffRunes)
	}
	if !customBase && statusOut == "" && strings.TrimSpace(diffOut) == "" && includeCommit {
		if _, ok := gitOutOK(targetDir, "rev-parse", "--verify", "--quiet", "HEAD~1"); ok {
			s, _ := gitOutOK(targetDir, "diff", "--stat", "HEAD~1..HEAD")
			diffStatOut = strings.TrimSpace(s)
			diffOut, _ = gitOutOK(targetDir, "diff", "HEAD~1..HEAD")
			source = "HEAD~1..HEAD"
		} else if _, ok := gitOutOK(targetDir, "rev-parse", "--verify", "--quiet", "HEAD"); ok {
			s, _ := gitOutOK(targetDir, "show", "--stat", "HEAD")
			diffStatOut = strings.TrimSpace(s)
			diffOut, _ = gitOutOK(targetDir, "show", "HEAD")
			source = "HEAD"
		}
	}
	hasChanges := statusOut != "" || strings.TrimSpace(diffOut) != ""
	if statusOut == "" {
		statusOut = "(clean)"
	}
	if diffStatOut == "" {
		diffStatOut = "(no tracked diff)"
	}
	return DiffContext{
		Source: source, Status: statusOut, DiffStat: diffStatOut,
		Diff: diffOut, ChangedFiles: changed, HasChanges: hasChanges,
	}
}
