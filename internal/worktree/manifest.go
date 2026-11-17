// Package worktree manages manifest validation, isolated worktrees, and skills.
package worktree

import (
	"encoding/json"
	"io"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/UberMetroid/mcode-router/internal/fleet"
)

var ignoredCacheDirs = []string{".pytest_cache", ".ruff_cache", ".mypy_cache"}

const (
	MaxManifestBytes = 1 << 20
	MaxManifestTasks = 64
)

// TaskSpec represents a single manifest task entry.
type TaskSpec struct {
	ID           string          `json:"id"`
	Prompt       string          `json:"prompt"`
	Files        []string        `json:"files"`
	Skills       []string        `json:"skills"`
	ExcludeLanes []string        `json:"exclude_lanes"`
	Lane         string          `json:"lane"`
	Complexity   string          `json:"complexity"`
	Model        string          `json:"model"`
	Effort       string          `json:"effort"`
	Timeout      json.RawMessage `json:"timeout"`
	ReadOnly     bool            `json:"read_only"`
	Review       *bool           `json:"review"`
}

// ParseRawTimeout parses optional JSON string or numeric timeout values.
func ParseRawTimeout(raw json.RawMessage) (int, bool, error) {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return 0, false, nil
	}
	if strings.HasPrefix(s, `"`) {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return 0, true, fleet.UsageErr("error: invalid timeout %s", s)
		}
		sec, err := fleet.ParseTimeout(text)
		return sec, true, err
	}
	if val, err := strconv.ParseFloat(s, 64); err == nil {
		if val <= 0 {
			return 0, true, fleet.UsageErr("error: timeout must be > 0 (got %s)", s)
		}
		return int(val), true, nil
	}
	return 0, true, fleet.UsageErr("error: invalid timeout %s", s)
}

// NormalizeRelPath validates a workspace-relative path against traversal escapes.
func NormalizeRelPath(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	p := strings.ReplaceAll(trimmed, "\\", "/")
	hasDrive := len(p) >= 2 && ((p[0] >= 'A' && p[0] <= 'Z') || (p[0] >= 'a' && p[0] <= 'z')) && p[1] == ':'
	if strings.ContainsAny(trimmed, "\x00\r\n") || strings.HasPrefix(p, "/") || hasDrive {
		return "", fleet.UsageErr("error: path %q escapes the workspace", raw)
	}
	for strings.HasPrefix(p, "./") {
		p = p[2:]
	}
	norm := "."
	if p != "" {
		norm = path.Clean(p)
	}
	if strings.HasPrefix(norm, "/") || norm == ".." || strings.HasPrefix(norm, "../") {
		return "", fleet.UsageErr("error: path %q escapes the workspace", raw)
	}
	if norm == "." {
		return "", nil
	}
	return norm, nil
}

// IsIgnoredArtifact skips transient worktree dirs and Python bytecode caches.
func IsIgnoredArtifact(rel string) bool {
	if rel == "" || rel == ".worktrees" || strings.HasPrefix(rel, ".worktrees/") {
		return true
	}
	if strings.Contains("/"+rel+"/", "/__pycache__/") ||
		strings.HasSuffix(rel, ".pyc") || strings.HasSuffix(rel, ".pyo") {
		return true
	}
	for _, d := range ignoredCacheDirs {
		if rel == d || strings.HasPrefix(rel, d+"/") {
			return true
		}
	}
	return false
}

// ParseCSVArg flattens repeatable comma-separated CLI argument lists.
func ParseCSVArg(vals []string) []string {
	var out []string
	for _, item := range vals {
		for _, part := range strings.Split(item, ",") {
			if t := strings.TrimSpace(part); t != "" {
				out = append(out, t)
			}
		}
	}
	return out
}

func checkOverlap(norm, tid string, owners map[string]string) error {
	for prevPath, prevTID := range owners {
		if prevTID == tid {
			continue
		}
		a := strings.TrimRight(norm, "/")
		b := strings.TrimRight(prevPath, "/")
		if norm == prevPath || norm == "." || prevPath == "." ||
			strings.HasPrefix(norm, b+"/") || strings.HasPrefix(prevPath, a+"/") {
			return fleet.UsageErr(
				"error: overlapping file %q (conflicts with %q in task %q)",
				norm, prevPath, prevTID,
			)
		}
	}
	return nil
}

func validateTask(t TaskSpec, seen map[string]bool, owners map[string]string) error {
	if t.ID == "" || t.Prompt == "" {
		return fleet.UsageErr(
			"error: each task needs {id, prompt} (+ optional files, skills, timeout, lane, model)",
		)
	}
	if !fleet.SafeIDRe.MatchString(t.ID) {
		return fleet.UsageErr("error: bad task id %q (use [A-Za-z0-9._-], no slashes)", t.ID)
	}
	if seen[t.ID] {
		return fleet.UsageErr("error: duplicate task id %q", t.ID)
	}
	seen[t.ID] = true
	if t.Lane != "" {
		if _, err := fleet.ParseLanes(t.Lane, true); err != nil {
			return err
		}
	}
	if t.Model != "" {
		if _, err := fleet.NormalizeModel("auto", t.Model); err != nil {
			return err
		}
	}
	if _, _, err := ParseRawTimeout(t.Timeout); err != nil {
		return err
	}
	for _, ex := range t.ExcludeLanes {
		if _, err := fleet.NormalizeLane(ex, false); err != nil {
			return err
		}
	}
	for _, f := range t.Files {
		norm, err := NormalizeRelPath(f)
		if err != nil {
			return err
		}
		if norm == "" {
			return fleet.UsageErr("error: task %s file path %q must target a file or dir", t.ID, f)
		}
		if err := checkOverlap(norm, t.ID, owners); err != nil {
			return err
		}
		owners[norm] = t.ID
	}
	return nil
}

// LoadManifest reads and validates [], {tasks: []}, or {subtasks: []} JSON.
func LoadManifest(filePath string) ([]TaskSpec, error) {
	st, err := os.Stat(filePath)
	if err != nil {
		return nil, fleet.UsageErr("error: cannot read manifest %s: %v", filePath, err)
	}
	if !st.Mode().IsRegular() {
		return nil, fleet.UsageErr("error: manifest %s is not a regular file", filePath)
	}
	f, err := os.Open(filePath)
	if err != nil {
		return nil, fleet.UsageErr("error: cannot read manifest %s: %v", filePath, err)
	}
	raw, rerr := io.ReadAll(io.LimitReader(f, MaxManifestBytes+1))
	_ = f.Close()
	if rerr != nil {
		return nil, fleet.UsageErr("error: cannot read manifest %s: %v", filePath, rerr)
	}
	if len(raw) > MaxManifestBytes {
		return nil, fleet.UsageErr("error: manifest %s exceeds %d bytes", filePath, MaxManifestBytes)
	}
	trimmed := strings.TrimSpace(string(raw))
	var tasks []TaskSpec
	if strings.HasPrefix(trimmed, "{") {
		var wrap struct {
			Tasks    []TaskSpec `json:"tasks"`
			Subtasks []TaskSpec `json:"subtasks"`
		}
		if err := json.Unmarshal(raw, &wrap); err != nil {
			return nil, fleet.UsageErr("error: cannot read manifest %s: %v", filePath, err)
		}
		if len(wrap.Tasks) > 0 {
			tasks = wrap.Tasks
		} else {
			tasks = wrap.Subtasks
		}
	} else if err := json.Unmarshal(raw, &tasks); err != nil {
		return nil, fleet.UsageErr("error: cannot read manifest %s: %v", filePath, err)
	}
	if len(tasks) == 0 {
		return nil, fleet.UsageErr(
			"error: manifest must be a non-empty JSON list (or {tasks/subtasks: [...]})",
		)
	}
	if len(tasks) > MaxManifestTasks {
		return nil, fleet.UsageErr("error: manifest %s exceeds %d tasks", filePath, MaxManifestTasks)
	}
	seen := make(map[string]bool)
	owners := make(map[string]string)
	for _, t := range tasks {
		if err := validateTask(t, seen, owners); err != nil {
			return nil, err
		}
	}
	return tasks, nil
}
