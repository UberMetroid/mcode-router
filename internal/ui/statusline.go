// Status line rendering: the MiniMax Code status bar entry and live telemetry view.
package ui

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/UberMetroid/mcode-router/internal/dispatch"
	"github.com/UberMetroid/mcode-router/internal/fleet"
)

const ansiReset = "\033[0m"

// StatusContext is the JSON payload MiniMax Code writes to a status line's stdin.
type StatusContext struct {
	Protocol     int    `json:"protocol"`
	Event        string `json:"event"`
	WorkspaceDir string `json:"workspace_dir"`
	Model        string `json:"model"`
	SessionTitle string `json:"session_title"`
	TUIVersion   string `json:"tui_version"`
}

// ReadStatusContext parses the status line context, waiting briefly for stdin.
func ReadStatusContext(r io.Reader) StatusContext {
	ctx := StatusContext{}
	if r == nil {
		return ctx
	}
	ch := make(chan StatusContext, 1)
	go func() {
		var c StatusContext
		_ = json.NewDecoder(r).Decode(&c)
		ch <- c
	}()
	select {
	case ctx = <-ch:
	case <-time.After(400 * time.Millisecond):
	}
	return ctx
}

// ReadActiveWorkers loads the live worker markers written by running dispatches.
func ReadActiveWorkers() []dispatch.WorkerMarker {
	dir := filepath.Join(fleet.ScratchDirPath(), "active")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []dispatch.WorkerMarker
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".json") {
			continue
		}
		raw, rerr := os.ReadFile(filepath.Join(dir, ent.Name()))
		if rerr != nil {
			continue
		}
		var m dispatch.WorkerMarker
		if json.Unmarshal(raw, &m) != nil {
			continue
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt < out[j].StartedAt })
	return out
}

// ReadLastWorker loads the marker for the most recently completed dispatch.
func ReadLastWorker() (dispatch.LastRunMarker, bool) {
	raw, err := os.ReadFile(filepath.Join(fleet.ScratchDirPath(), "last.json"))
	if err != nil {
		return dispatch.LastRunMarker{}, false
	}
	var m dispatch.LastRunMarker
	if json.Unmarshal(raw, &m) != nil {
		return dispatch.LastRunMarker{}, false
	}
	return m, true
}

// PrintLiveTelemetry renders live dispatch telemetry: active workers and the last run.
func PrintLiveTelemetry(w io.Writer) {
	fmt.Fprintln(w, "\nLive Worker Telemetry:")
	active := ReadActiveWorkers()
	if len(active) == 0 {
		fmt.Fprintln(w, "  (no active workers)")
	}
	now := time.Now()
	for _, m := range active {
		meta := readWorkerMeta(m, now)
		fmt.Fprintf(w, "  ACTIVE lane=%s cli=%s model=%s effort=%s phase=%s tok/s=%s elapsed=%.1fs\n",
			fleet.SafeText(m.Lane), fleet.SafeText(m.CLI),
			fleet.SafeText(dispatch.ShortModel(m.Model)), fleet.SafeText(m.Effort),
			fleet.SafeText(meta.phase), rateText(meta), meta.elapsed)
		fmt.Fprintf(w, "         task=%s cwd=%s skills=%s\n",
			fleet.SafeText(m.TaskID), fleet.SafeText(m.Cwd),
			fleet.SafeText(strings.Join(dispatch.VisibleExtraSkills(m.Skills), ",")))
	}
	if last, ok := ReadLastWorker(); ok {
		fmt.Fprintf(w, "  LAST   lane=%s model=%s effort=%s exit=%d duration=%.1fs\n",
			fleet.SafeText(last.Lane), fleet.SafeText(dispatch.ShortModel(last.Model)),
			fleet.SafeText(last.Effort), last.ExitCode, last.DurationS)
	}
}

// StatusLine renders the MiniMax Code status bar entry for active router workers.
// It stays silent while no dispatch is running so the bar shows nothing extra.
func StatusLine(r io.Reader, w io.Writer) int {
	_ = ReadStatusContext(r)
	if err := fleet.EnsureStateRoots(); err != nil {
		return 0
	}
	active := ReadActiveWorkers()
	if len(active) == 0 {
		return 0
	}
	pal := ResolvePalette(nil)
	now := time.Now()
	for _, m := range active {
		fmt.Fprintln(w, FormatWorkerLine(pal, m, now))
	}
	return 0
}

// FormatWorkerLine renders one active worker as a status line row.
// The row carries the worker token rate and the worker model, nothing else:
// the rate leads so it is never confused with MiniMax Code's own session
// telemetry, and it is coloured by throughput, from muted while idle to
// bright when fast.
func FormatWorkerLine(pal Palette, m dispatch.WorkerMarker, now time.Time) string {
	meta := readWorkerMeta(m, now)
	rate := fmt.Sprintf("%.0f", meta.rate)
	if !meta.exact {
		rate = "~" + rate
	}
	parts := []string{
		rateColor(pal, meta.rate) + rateBolt + " " + rate + " tok/s" + ansiReset,
		pal.StatusAccent + fleet.SafeText(dispatch.ShortModel(m.Model)) + ansiReset,
	}
	return strings.Join(parts, " · ")
}

// workerMeta summarises a worker's live phase, token rate and elapsed time.
type workerMeta struct {
	phase   string
	rate    float64
	exact   bool
	elapsed float64
}

// readWorkerMeta summarises a worker's live phase, token rate and elapsed time.
func readWorkerMeta(m dispatch.WorkerMarker, now time.Time) workerMeta {
	meta := workerMeta{phase: "idle", rate: m.TokS, exact: m.TokExact}
	if phase := strings.TrimSpace(m.Phase); phase != "" {
		meta.phase = phase
	}
	if m.StartedAt > 0 {
		meta.elapsed = now.Sub(time.Unix(0, int64(m.StartedAt*1e9))).Seconds()
		if meta.elapsed < 0 {
			meta.elapsed = 0
		}
	}
	return meta
}

// rateText renders the token rate, marked as estimated when it is not exact.
func rateText(meta workerMeta) string {
	if meta.exact {
		return fmt.Sprintf("%.1f", meta.rate)
	}
	return "~" + fmt.Sprintf("%.1f", meta.rate)
}
