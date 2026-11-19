package dispatch

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/UberMetroid/mcode-router/internal/fleet"
	"github.com/UberMetroid/mcode-router/internal/verify"
)

const outputTailRunes, streamTick = 20000, 1 * time.Second

var stallIdleSec = 180.0

// ExecReq specifies a single worker subprocess execution with streaming HUD updates.
type ExecReq struct {
	Provider, CLIPath, Model, Effort, Prompt          string
	Worktree, SlotDir, TaskID, ReviewMode, MarkerPath string
	Info                                              *fleet.ProviderConfig
	Files                                             []string
	TimeoutSec                                        int
	ReadOnly                                          bool
	Marker                                            WorkerMarker
}

// ExecRes holds normalized subprocess exit code, assistant output, timing, and usage.
type ExecRes struct {
	ExitCode   int
	Output     string
	TimedOut   bool
	DurationS  float64
	ExportInfo *fleet.ExportUsage
}

func nowFloatSec() float64 {
	return math.Round(float64(time.Now().UnixNano())/1e7) / 100
}

func tailRunes(s string, limit int) string {
	if r := []rune(s); len(r) > limit {
		return string(r[len(r)-limit:])
	}
	return s
}

func safeKillGroup(mu *sync.Mutex, reaped *bool, p *os.Process) error {
	mu.Lock()
	defer mu.Unlock()
	if !*reaped && p != nil {
		*reaped = true
		return syscall.Kill(-p.Pid, syscall.SIGKILL)
	}
	return nil
}

func pumpStream(
	r io.Reader, mode string, checkStderrQuota bool, wg *sync.WaitGroup,
	mu *sync.Mutex, st *streamTracker, onAbort func(),
) {
	defer wg.Done()
	br := bufio.NewReaderSize(r, 16*1024)
	for {
		line, err := readStream(br, maxLineBytes, mode)
		if line != "" {
			now := nowFloatSec()
			mu.Lock()
			st.lastIO = now
			if mode == "ndjson" {
				st.consumeLine(line, now)
			} else {
				st.rawBuf.addStr(line)
				if mode == "text" {
					st.addRateBytes(now, len(line), false)
				}
				trimmed := strings.TrimSpace(line)
				low := strings.ToLower(trimmed)
				errFrame := checkStderrQuota || strings.HasPrefix(low, "error:") ||
					strings.HasPrefix(low, "api error") || strings.HasPrefix(low, "fatal:") || strings.HasPrefix(low, "{")
				if errFrame && IsLiveQuotaLine(trimmed) {
					st.isError, st.errText, st.qAbort = true, trimmed, true
				}
			}
			abort := st.qAbort
			mu.Unlock()
			if abort && onAbort != nil {
				onAbort()
			}
		}
		if err != nil {
			break
		}
	}
}

func runHudTicker(ctx context.Context, wg *sync.WaitGroup, ndjson bool, req ExecReq,
	mu *sync.Mutex, st, errSt *streamTracker, onAbort func()) {
	defer wg.Done()
	if req.MarkerPath == "" && !ndjson {
		return
	}
	ticker := time.NewTicker(streamTick)
	defer ticker.Stop()
	lastSig := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := nowFloatSec()
			mu.Lock()
			rate, exact := st.currentRate(now)
			phase := st.phase
			lim := stallIdleSec
			if phase == "tool" {
				lim *= 1.6
			}
			if ndjson && lim > 0 && now-max(st.lastIO, errSt.lastIO) >= lim {
				st.stalled = true
				mu.Unlock()
				onAbort()
				return
			}
			mu.Unlock()
			sig := fmt.Sprintf("%s|%.0f|%t", phase, rate, exact)
			if req.MarkerPath == "" || (sig == lastSig &&
				!(phase == "stream" && rate >= 1 && (now-req.Marker.UpdatedAt) >= 2.0)) {
				continue
			}
			lastSig = sig
			req.Marker.Phase, req.Marker.TokS, req.Marker.TokExact, req.Marker.UpdatedAt = phase, rate, exact, now
			HudWorkerUpdate(req.MarkerPath, req.Marker)
		}
	}
}

func finalizeOutput(built BuiltCmd, st *streamTracker, errText string,
	code int, timedOut bool, timeoutSec int) (string, int, *fleet.ExportUsage) {
	exp := ParseVibeExport(built.VibeExportDir)
	if exp == nil {
		exp = &fleet.ExportUsage{Tokens: st.usage.Tokens, CostUSD: st.usage.CostUSD}
	}
	exp.Failed = code != 0 || timedOut
	out := st.rawBuf.str()
	if built.StreamFmt == "ndjson" {
		switch {
		case st.isError && st.errText != "":
			out = "[error] " + st.errText
		case st.hasFinal && st.finalResp != "":
			out = st.finalResp
		case len(st.cleanBuf.buf) > 0:
			out = st.cleanBuf.str()
		case len(st.turnBuf.buf) > 0:
			out = st.turnBuf.str()
		}
	}
	out = verify.NormalizeNativeReview(out)
	if st.isError && code == 0 {
		code = 1
	}
	if errText != "" && !strings.Contains(out, errText) {
		switch {
		case out == "":
			out = errText
		case code != 0:
			out = strings.TrimSuffix(out, "\n") + "\n" + errText
		case fleet.HasErrorPattern(errText, 1, []string{"error:", "fatal:", "panic:", "traceback"}):
			out = "[stderr] " + strings.TrimSpace(errText) + "\n" + out
		}
	}
	if timedOut && st.stalled {
		out += "\n<TIMEOUT: stream stalled>"
	} else if timedOut {
		out += fmt.Sprintf("\n<TIMEOUT after %ds>", timeoutSec)
	}
	return tailRunes(out, outputTailRunes), code, exp
}

func runStreamProcess(req ExecReq, built BuiltCmd, timeoutSec int) ExecRes {
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, built.Argv[0], built.Argv[1:]...)
	cmd.Dir, cmd.Env, cmd.WaitDelay = req.Worktree, built.Env, 2*time.Second
	if built.Stdin != "" {
		cmd.Stdin = strings.NewReader(built.Stdin)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var mu sync.Mutex
	var reaped bool
	cmd.Cancel = func() error { return safeKillGroup(&mu, &reaped, cmd.Process) }
	prOut, pwOut := io.Pipe()
	prErr, pwErr := io.Pipe()
	defer prOut.Close()
	defer prErr.Close()
	cmd.Stdout, cmd.Stderr = pwOut, pwErr
	if serr := cmd.Start(); serr != nil {
		_ = pwOut.Close()
		_ = pwErr.Close()
		return ExecRes{ExitCode: 1, Output: serr.Error(), DurationS: time.Since(start).Seconds()}
	}
	now, onAbort := nowFloatSec(), func() { _ = safeKillGroup(&mu, &reaped, cmd.Process) }
	st, errSt := &streamTracker{phase: "wait", lastIO: now}, &streamTracker{lastIO: now}
	if built.StreamFmt == "ndjson" && req.MarkerPath != "" {
		req.Marker.Phase, req.Marker.UpdatedAt = "wait", now
		HudWorkerUpdate(req.MarkerPath, req.Marker)
	}
	tickCtx, stopTick := context.WithCancel(ctx)
	defer stopTick()
	var wg sync.WaitGroup
	wg.Add(3)
	go pumpStream(prOut, built.StreamFmt, false, &wg, &mu, st, onAbort)
	go pumpStream(prErr, "plain", true, &wg, &mu, errSt, onAbort)
	go runHudTicker(tickCtx, &wg, built.StreamFmt == "ndjson", req, &mu, st, errSt, onAbort)
	werr := cmd.Wait()
	mu.Lock()
	reaped = true
	mu.Unlock()
	_ = pwOut.Close()
	_ = pwErr.Close()
	stopTick()
	wg.Wait()
	qAborted, stalled, errText := st.qAbort || errSt.qAbort, st.stalled, strings.TrimSpace(errSt.rawBuf.str())
	code, timedOut := 1, (errors.Is(ctx.Err(), context.DeadlineExceeded) || stalled) && !qAborted
	if timedOut {
		code = -1
	} else if !qAborted && werr == nil {
		code = 0
	} else if !qAborted && cmd.ProcessState != nil && cmd.ProcessState.Exited() {
		code = cmd.ProcessState.ExitCode()
	}
	out, code, exp := finalizeOutput(built, st, errText, code, timedOut, timeoutSec)
	return ExecRes{ExitCode: code, Output: out, TimedOut: timedOut, DurationS: time.Since(start).Seconds(), ExportInfo: exp}
}

// ExecuteOnce spawns one bounded worker process, streams live HUD telemetry, and returns output.
func ExecuteOnce(req ExecReq) ExecRes {
	built, err := BuildWorkerCommand(CmdReq{Provider: req.Provider, CLIPath: req.CLIPath,
		Model: req.Model, Effort: req.Effort, Prompt: req.Prompt, Files: req.Files, Worktree: req.Worktree,
		SlotDir: req.SlotDir, TaskID: req.TaskID, ReadOnly: req.ReadOnly, ReviewMode: req.ReviewMode})
	if err != nil {
		return ExecRes{ExitCode: 2, Output: err.Error()}
	}
	defer os.RemoveAll(built.PromptFile)
	defer os.RemoveAll(built.VibeExportDir)
	return runStreamProcess(req, built, max(1, req.TimeoutSec))
}
