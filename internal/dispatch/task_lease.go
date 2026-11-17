package dispatch

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/UberMetroid/mcode-router/internal/envelope"
	"github.com/UberMetroid/mcode-router/internal/fleet"
	"github.com/UberMetroid/mcode-router/internal/worktree"
)

const maxStreamBytes = 256 * 1024

type tailBuf struct {
	buf []byte
	max int
}

func (tb *tailBuf) Write(p []byte) (int, error) {
	limit := tb.max
	if limit <= 0 {
		limit = maxStreamBytes
	}
	tb.buf = append(tb.buf, p...)
	if len(tb.buf) > 2*limit {
		tb.buf = []byte(tb.str())
	}
	return len(p), nil
}

func (tb *tailBuf) addStr(s string) {
	if s != "" {
		_, _ = tb.Write([]byte(s))
	}
}

func (tb *tailBuf) str() string {
	limit := tb.max
	if limit <= 0 {
		limit = maxStreamBytes
	}
	b := tb.buf
	if len(b) > limit {
		start := len(b) - limit
		for start < len(b) && (b[start]&0xC0) == 0x80 {
			start++
		}
		b = b[start:]
	}
	return string(b)
}

// TaskAttempt captures telemetry for a single provider execution attempt.
type TaskAttempt struct {
	Lane      string  `json:"lane"`
	Model     string  `json:"model"`
	Effort    string  `json:"effort"`
	ExitCode  int     `json:"exit_code"`
	TimedOut  bool    `json:"timed_out"`
	DurationS float64 `json:"duration_s"`
}

// TaskReq defines inputs for running a routed task with fallback.
type TaskReq struct {
	Task       worktree.TaskSpec
	Worktree   string
	SlotDir    string
	Lane       string
	Model      string
	Effort     string
	TimeoutRaw string
	QuotaPath  string
	StripeIdx  *int
	IsParallel bool
	Exclude    []string
	AvoidFam   map[string]bool
	NoPivot    bool
	SoftPin    bool
	LogWriter  io.Writer
	Runner     func(ExecReq) ExecRes
	ResolveCLI func(string) string
}

// TaskRes captures final execution result and fallback history for one task.
type TaskRes struct {
	RequestedLane string        `json:"requested_lane"`
	Lane          string        `json:"lane"`
	CLI           string        `json:"cli"`
	Model         string        `json:"model"`
	Effort        string        `json:"effort"`
	FallbackFrom  string        `json:"fallback_from"`
	Attempts      []TaskAttempt `json:"attempts"`
	ExitCode      int           `json:"exit_code"`
	TimedOut      bool          `json:"timed_out"`
	Output        string        `json:"output"`
	DurationS     float64       `json:"duration_s"`
}

type taskState struct {
	req      TaskReq
	prompt   string
	timeout  int
	canonReq string
	tried    map[string]bool
	avoid    map[string]bool
	pivoted  bool
	attempts []TaskAttempt
	notes    []string
	skipped  string
}

// UsedLanes returns deduplicated non-empty attempted lanes plus the winning lane.
func (r TaskRes) UsedLanes() []string {
	seen, out := make(map[string]bool, len(r.Attempts)+1), make([]string, 0, len(r.Attempts)+1)
	for _, a := range r.Attempts {
		if a.Lane != "" && !seen[a.Lane] {
			seen[a.Lane], out = true, append(out, a.Lane)
		}
	}
	if r.Lane != "" && !seen[r.Lane] {
		out = append(out, r.Lane)
	}
	return out
}

func (st *taskState) canDeferCap() bool {
	return st.canonReq == "auto" || strings.Contains(st.canonReq, ",") || st.req.SoftPin
}

// ResolveTaskTimeout picks regime timeout unless task or CLI timeout is explicit.
func ResolveTaskTimeout(cliRaw string, taskSec int, hasTask bool, comp string, ro bool) (int, error) {
	if hasTask && taskSec > 0 {
		return taskSec, nil
	}
	if cliRaw != "" {
		return fleet.ParseTimeout(cliRaw)
	}
	c := strings.ToLower(strings.TrimSpace(comp))
	if ro && (c == "" || c == "recon") {
		return fleet.RegimeTimeouts["observe"], nil
	}
	if fleet.DeepComplexities[c] {
		return fleet.RegimeTimeouts["deep"], nil
	}
	if fleet.FastComplexities[c] {
		return fleet.RegimeTimeouts["fast"], nil
	}
	if c == "heavy" {
		return fleet.RegimeTimeouts["heavy"], nil
	}
	return fleet.RegimeTimeouts["act"], nil
}

func initTaskState(req TaskReq) (*taskState, error) {
	if err := fleet.CheckModelGuard(req.Model); err != nil {
		return nil, err
	}
	if err := fleet.CheckModelGuard(req.Task.Model); err != nil {
		return nil, err
	}
	prompt := req.Task.Prompt
	if len(req.Task.Files) > 0 {
		prompt = "Files in scope: " + strings.Join(req.Task.Files, ", ") + "\n\n" + prompt
	}
	if len(req.Task.Skills) > 0 {
		blocks, err := worktree.LoadSkillBlocks(req.Task.Skills, req.Worktree)
		if err != nil {
			return nil, err
		}
		prompt = blocks + prompt
	}
	lane := req.Task.Lane
	if lane == "" {
		lane = req.Lane
	}
	if lane == "" {
		lane = "auto"
	}
	lanes, err := fleet.ParseLanes(lane, true)
	if err != nil {
		return nil, err
	}
	canon := strings.Join(lanes, ",")
	if len(lanes) == 1 && lanes[0] == "auto" {
		canon = "auto"
	}
	taskTo, hasTo, err := worktree.ParseRawTimeout(req.Task.Timeout)
	if err != nil {
		return nil, err
	}
	to, err := ResolveTaskTimeout(req.TimeoutRaw, taskTo, hasTo, req.Task.Complexity, req.Task.ReadOnly)
	if err != nil {
		return nil, err
	}
	tried := make(map[string]bool, len(req.Exclude)+len(req.Task.ExcludeLanes))
	for _, x := range append(req.Exclude, req.Task.ExcludeLanes...) {
		if norm, nerr := fleet.NormalizeLane(x, false); nerr == nil {
			tried[norm] = true
		}
	}
	avoid := map[string]bool{}
	for k, v := range req.AvoidFam {
		if v {
			avoid[k] = true
		}
	}
	req.SoftPin = req.SoftPin && req.Task.Lane == ""
	return &taskState{req: req, prompt: prompt, timeout: to, canonReq: canon, tried: tried, avoid: avoid}, nil
}

func acquireTaskLease(st *taskState, sel envelope.SelectRes, q *fleet.QuotaState) (*os.File, string, *TaskRes) {
	if sel.Info == nil || sel.Info.MaxParallel == nil || *sel.Info.MaxParallel <= 0 {
		return nil, "", nil
	}
	maxP := *sel.Info.MaxParallel
	if st.canDeferCap() {
		if lease, _, _ := fleet.AcquireParallelLease(sel.Lane, maxP, false, 0); lease != nil {
			return lease, "", nil
		}
		ex := map[string]bool{sel.Lane: true}
		for k, v := range st.tried {
			ex[k] = v
		}
		alt, _ := envelope.SelectProvider(envelope.SelectReq{
			RequestedLane: st.canonReq, Complexity: st.req.Task.Complexity, Quota: q,
			Exclude: ex, StripeIndex: st.req.StripeIdx, AvoidFamilies: st.avoid,
			DeferAtCap: true, SoftPin: st.req.SoftPin, ResolveCLI: st.req.ResolveCLI,
		})
		if alt.Lane != "" {
			st.tried[sel.Lane] = true
			st.notes = append(st.notes, fmt.Sprintf("%s: %d/%d parallel slots busy", sel.Lane, maxP, maxP))
			return nil, "retry", nil
		}
		if st.req.LogWriter != nil {
			fmt.Fprintf(st.req.LogWriter, "[%s] waiting for free %s slot (%d/%d busy)\n",
				st.req.Task.ID, sel.Lane, maxP, maxP)
		}
	}
	lease, timedOut, lerr := fleet.AcquireParallelLease(sel.Lane, maxP, true, st.timeout)
	if timedOut {
		msg := fmt.Sprintf("<PARALLEL SLOT TIMEOUT after %ds waiting on %s (%d/%d busy)>",
			st.timeout, sel.Lane, maxP, maxP)
		return nil, "timeout", &TaskRes{
			RequestedLane: st.canonReq, Lane: sel.Lane, CLI: sel.Info.CLI,
			Attempts: st.attempts, ExitCode: -1, TimedOut: true, Output: msg, DurationS: float64(st.timeout),
		}
	}
	if lerr != nil {
		msg := fmt.Sprintf("error: acquire %s parallel lease: %v", sel.Lane, lerr)
		return nil, "error", &TaskRes{RequestedLane: st.canonReq, Lane: sel.Lane,
			CLI: sel.Info.CLI, Attempts: st.attempts, ExitCode: 1, Output: msg}
	}
	return lease, "", nil
}
