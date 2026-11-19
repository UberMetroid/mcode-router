package worktree

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/UberMetroid/mcode-router/internal/fleet"
)

// WorkspaceStagesPath resolves the out-of-git stage override file for cwd.
func WorkspaceStagesPath(cwd string) string {
	root, _ := filepath.Abs(strings.TrimSpace(cwd))
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	for d := root; d != filepath.Dir(d); d = filepath.Dir(d) {
		gp := filepath.Join(d, ".git")
		fi, err := os.Stat(gp)
		if err == nil && fi.IsDir() {
			root = d
			break
		}
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		raw, _ := os.ReadFile(gp)
		if gd, ok := strings.CutPrefix(strings.SplitN(string(raw), "\n", 2)[0], "gitdir:"); ok {
			if gd = strings.TrimSpace(gd); !filepath.IsAbs(gd) {
				gd = filepath.Join(d, gd)
			}
			if gd = filepath.Clean(gd); filepath.Base(filepath.Dir(gd)) == "worktrees" &&
				filepath.Base(filepath.Dir(filepath.Dir(gd))) == ".git" {
				root = filepath.Dir(filepath.Dir(filepath.Dir(gd)))
				break
			}
		}
	}
	sum := sha256.Sum256([]byte(root))
	id := fleet.SafeID(filepath.Base(root)) + "-" + hex.EncodeToString(sum[:6])
	return filepath.Join(filepath.Dir(fleet.QuotaFilePath("")), "workspaces", id+".json")
}

// warnedPins keeps a stage pin warning to one line per stage per process.
var warnedPins = func() func(string, string) {
	var mu sync.Mutex
	seen := map[string]bool{}
	return func(key, msg string) {
		mu.Lock()
		defer mu.Unlock()
		if seen[key] {
			return
		}
		seen[key] = true
		fmt.Fprintln(os.Stderr, msg)
	}
}()

// sanitizeStageRoute re-validates a stage route that came from mutable state,
// applying the same invariants as the write path: known lanes only, no paid
// or PAYG lane, and no reserved model.
var sanitizeStageRoute = func(r fleet.StageRoute) (fleet.StageRoute, bool) {
	lanes, err := fleet.ParseLanes(r.Lane, true)
	if err != nil || len(lanes) == 0 {
		return fleet.StageRoute{}, false
	}
	for _, l := range lanes {
		spec, known := fleet.ProviderSpecs()[l]
		if l == "devin-fusion" || (known && spec.Tier == "credits") ||
			(l == "muse" && fleet.MusePayAsYouGoCached()) {
			return fleet.StageRoute{}, false
		}
	}
	if r.ExpiresAt != nil && *r.ExpiresAt != "" {
		dt, ok := fleet.ParseISOUTC(*r.ExpiresAt)
		if !ok || !dt.After(time.Now().UTC()) {
			return fleet.StageRoute{}, false
		}
	}
	if r.Model != nil {
		if err := fleet.CheckModelGuard(*r.Model); err != nil {
			return fleet.StageRoute{}, false
		}
	}
	if len(lanes) == 1 && lanes[0] == "auto" && (r.Model != nil || r.Effort != nil) {
		// An auto route may not carry a model or an effort.
		return fleet.StageRoute{}, false
	}
	r.Lane = strings.Join(lanes, ",")
	return r, true
}

// LoadWorkspaceStages reads repo-scoped overrides and ignores expired pins.
func LoadWorkspaceStages(cwd string) map[string]fleet.StageRoute {
	out := make(map[string]fleet.StageRoute, len(fleet.StageNames))
	p, parsed := WorkspaceStagesPath(cwd), map[string]fleet.StageRoute(nil)
	if raw, err := os.ReadFile(p); err != nil {
		return out
	} else if json.Unmarshal(raw, &parsed) != nil || parsed == nil {
		fmt.Fprintf(os.Stderr, "warning: ignoring corrupt workspace stage file %s\n", p)
		return out
	}
	for _, st := range fleet.StageNames {
		if r := parsed[st]; r.Lane != "" {
			if r.ExpiresAt != nil && *r.ExpiresAt != "" {
				if dt, ok := fleet.ParseISOUTC(*r.ExpiresAt); ok && !dt.After(time.Now().UTC()) {
					continue
				}
			}
			clean, ok := sanitizeStageRoute(r)
			if !ok {
				warnedPins("workspace-"+st, "warning: ignoring invalid workspace stage pin for "+st)
				continue
			}
			out[st] = clean
		}
	}
	return out
}

// SaveWorkspaceStages writes repo stage overrides outside git or deletes when empty.
func SaveWorkspaceStages(cwd string, stages map[string]fleet.StageRoute) error {
	p, kept := WorkspaceStagesPath(cwd), make(map[string]fleet.StageRoute)
	for _, st := range fleet.StageNames {
		if r := stages[st]; r.Lane != "" {
			kept[st] = r
		}
	}
	if len(kept) == 0 {
		return os.RemoveAll(p)
	}
	return fleet.AtomicWriteJSON(p, kept, false, true)
}
