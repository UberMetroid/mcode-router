package envelope

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/UberMetroid/mcode-router/internal/fleet"
	"github.com/UberMetroid/mcode-router/internal/worktree"
)

const mismatchTTLSecs = 1800

var mismatchMu sync.Mutex

// MismatchEntry records a recently failed/rejected OODA loop lane.
type MismatchEntry struct {
	Lane       string  `json:"lane"`
	Family     string  `json:"family"`
	Reason     string  `json:"reason"`
	RecordedAt float64 `json:"recorded_at"`
}

// MismatchKey builds a stable '<repo-root>::<sorted files|*>' lookup key.
func MismatchKey(cwd string, files []string) string {
	base := cwd
	if base == "" {
		base = "."
	}
	if abs, err := filepath.Abs(base); err == nil {
		base = abs
	}
	root := fleet.TryRepoRoot(base)
	if root == "" {
		root = base
	}
	var norm []string
	for _, f := range files {
		if strings.TrimSpace(f) == "" {
			continue
		}
		if n, err := worktree.NormalizeRelPath(f); err == nil {
			norm = append(norm, n)
		}
	}
	sort.Strings(norm)
	scope := "*"
	if len(norm) > 0 {
		scope = strings.Join(norm, ",")
	}
	return root + "::" + scope
}

func readMismatchMap() map[string]MismatchEntry {
	path := filepath.Join(fleet.ScratchDirPath(), "mismatch.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return map[string]MismatchEntry{}
	}
	var out map[string]MismatchEntry
	if err := json.Unmarshal(raw, &out); err != nil || out == nil {
		return map[string]MismatchEntry{}
	}
	return out
}

// LoadRecentMismatch returns a recent (<30m) mismatch record for cwd/files.
func LoadRecentMismatch(cwd string, files []string) *MismatchEntry {
	mapping := readMismatchMap()
	entry, ok := mapping[MismatchKey(cwd, files)]
	if !ok || entry.Family == "" {
		return nil
	}
	now := float64(time.Now().UnixNano()) / 1e9
	if now-entry.RecordedAt > mismatchTTLSecs {
		return nil
	}
	return &entry
}

// RecordMismatch persists a failed/rejected loop so the next loop rotates family.
func RecordMismatch(cwd string, files []string, lane, reason string) {
	if lane == "" {
		return
	}
	key := MismatchKey(cwd, files)
	mismatchMu.Lock()
	defer mismatchMu.Unlock()
	now := float64(time.Now().UnixNano()) / 1e9
	cleaned := make(map[string]MismatchEntry)
	for k, v := range readMismatchMap() {
		if now-v.RecordedAt <= mismatchTTLSecs {
			cleaned[k] = v
		}
	}
	cleaned[key] = MismatchEntry{
		Lane:       lane,
		Family:     ModelFamily(lane),
		Reason:     reason,
		RecordedAt: math.Round(now*100) / 100,
	}
	_ = fleet.AtomicWriteJSON(filepath.Join(fleet.ScratchDirPath(), "mismatch.json"), cleaned, true, true)
}

// ClearMismatch removes any stored mismatch entry after a verified green loop.
func ClearMismatch(cwd string, files []string) {
	key := MismatchKey(cwd, files)
	mismatchMu.Lock()
	defer mismatchMu.Unlock()
	mpath := filepath.Join(fleet.ScratchDirPath(), "mismatch.json")
	if _, err := os.Stat(mpath); err != nil {
		return
	}
	mapping := readMismatchMap()
	if _, ok := mapping[key]; ok {
		delete(mapping, key)
		_ = fleet.AtomicWriteJSON(mpath, mapping, true, true)
	}
}
