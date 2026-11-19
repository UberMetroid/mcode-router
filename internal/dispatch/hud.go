package dispatch

import (
	"fmt"
	"github.com/UberMetroid/mcode-router/internal/fleet"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// WorkerMarker is written to <hud_dir>/active/<pid>-<task>.json for the live HUD.
type WorkerMarker struct {
	PID       int      `json:"pid"`
	TaskID    string   `json:"task_id"`
	Mode      string   `json:"mode"`
	Lane      string   `json:"lane"`
	CLI       string   `json:"cli"`
	Model     string   `json:"model"`
	Effort    string   `json:"effort"`
	Skills    []string `json:"skills"`
	Cwd       string   `json:"cwd"`
	StartedAt float64  `json:"started_at"`
	Phase     string   `json:"phase,omitempty"`
	TokS      float64  `json:"tok_s,omitempty"`
	TokExact  bool     `json:"tok_exact,omitempty"`
	UpdatedAt float64  `json:"updated_at,omitempty"`
}

// LastRunMarker is written to <hud_dir>/last.json when a worker completes.
type LastRunMarker struct {
	Mode       string   `json:"mode"`
	Lane       string   `json:"lane"`
	Model      string   `json:"model"`
	Effort     string   `json:"effort"`
	Skills     []string `json:"skills"`
	ExitCode   int      `json:"exit_code"`
	DurationS  float64  `json:"duration_s"`
	FinishedAt float64  `json:"finished_at"`
}

// BatchReview is the serialized review section written into results.json.
type BatchReview struct {
	Ran      bool   `json:"ran"`
	Lane     string `json:"lane"`
	Model    string `json:"model"`
	Verdict  string `json:"verdict"`
	ExitCode *int   `json:"exit_code"`
	Reason   string `json:"reason,omitempty"`
	Output   string `json:"output"`
}

// BatchRecord summarizes one worktree task outcome in .worktrees/results.json.
type BatchRecord struct {
	ID              string        `json:"id"`
	Branch          string        `json:"branch"`
	Worktree        string        `json:"worktree"`
	Slot            string        `json:"slot"`
	RequestedLane   string        `json:"requested_lane"`
	Lane            string        `json:"lane"`
	CLI             string        `json:"cli"`
	Model           string        `json:"model"`
	Effort          string        `json:"effort"`
	FallbackFrom    string        `json:"fallback_from"`
	Attempts        []TaskAttempt `json:"attempts"`
	ExitCode        int           `json:"exit_code"`
	TimedOut        bool          `json:"timed_out"`
	DurationS       float64       `json:"duration_s"`
	ChangedFiles    []string      `json:"changed_files"`
	ScopeViolations []string      `json:"scope_violations"`
	DiffStat        string        `json:"diff_stat"`
	Output          string        `json:"output"`
	Review          BatchReview   `json:"review"`
}

// VisibleExtraSkills filters built-in core skills out of banner and HUD display.
func VisibleExtraSkills(skills []string) []string {
	var out []string
	for _, s := range skills {
		if s != "" && !fleet.CoreHiddenSkills[s] {
			out = append(out, s)
		}
	}
	return out
}

// ShortModel formats a compact model label for router banners and logs.
func ShortModel(modelName string) string {
	name := strings.TrimSpace(modelName)
	if name == "" {
		return "default"
	}
	if m := fusionModelRe.FindStringSubmatch(strings.ToLower(name)); len(m) == 7 {
		eff := effortAbbr[m[4]]
		if eff == "" {
			eff = m[4]
		}
		label := fmt.Sprintf("%s-%s.%s-%s", m[1], m[2], m[3], eff)
		if m[5] != "" {
			return fmt.Sprintf("fusion(%s+%s%s)", label, m[5], m[6])
		}
		return fmt.Sprintf("fusion(%s)", label)
	}
	if idx := strings.LastIndexByte(name, '/'); idx >= 0 && idx+1 < len(name) {
		return name[idx+1:]
	}
	return name
}

// HudWorkerStart records an active worker marker and returns its file path.
func HudWorkerStart(m WorkerMarker) string {
	if m.PID <= 0 {
		m.PID = os.Getpid()
	}
	if m.Model == "" {
		m.Model = "default"
	}
	if m.Effort == "" {
		m.Effort = "default"
	}
	if m.Skills == nil {
		m.Skills = []string{}
	}
	if m.StartedAt <= 0 {
		m.StartedAt = math.Round(float64(time.Now().UnixNano())/1e7) / 100
	}
	path := filepath.Join(fleet.ScratchDirPath(), "active",
		fmt.Sprintf("%d-%s.json", m.PID, fleet.SafeID(m.TaskID)))
	_ = fleet.AtomicWriteJSON(path, m, true, true)
	return path
}

// HudWorkerUpdate refreshes live phase and tok/s telemetry in an active worker marker.
func HudWorkerUpdate(path string, m WorkerMarker) {
	if path == "" {
		return
	}
	if m.Skills == nil {
		m.Skills = []string{}
	}
	_ = fleet.AtomicWriteJSON(path, m, true, true)
}

// HudWorkerFinish deletes the active worker marker and updates last.json.
func HudWorkerFinish(path string, last LastRunMarker) {
	if path != "" {
		_ = os.Remove(path)
	}
	if last.Model == "" {
		last.Model = "default"
	}
	if last.Effort == "" {
		last.Effort = "default"
	}
	if last.Skills == nil {
		last.Skills = []string{}
	}
	last.DurationS = math.Round(last.DurationS*10) / 10
	last.FinishedAt = math.Round(float64(time.Now().UnixNano())/1e7) / 100
	_ = fleet.AtomicWriteJSON(filepath.Join(fleet.ScratchDirPath(), "last.json"), last, true, true)
}
