package fleet

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const shimMarker = ".superconductor"

var maxCmdOutBytes int64 = 16 << 20

// CmdSpec defines a bounded argv-only subprocess invocation (no shell).
type CmdSpec struct {
	Argv       []string
	Cwd        string
	Env        []string
	TimeoutSec int
}

// CmdRes holds captured separate stdout/stderr streams and exit status.
type CmdRes struct {
	ExitCode int
	Stdout   string
	Stderr   string
	Combined string
	TimedOut bool
}

// NowISO returns the local timezone ISO-8601 timestamp at second precision.
func NowISO() string {
	return time.Now().Format(time.RFC3339)
}

// FormatLocalISO formats a timestamp in local timezone at second precision.
func FormatLocalISO(t time.Time) string {
	return t.Local().Format(time.RFC3339)
}

// ParseISOUTC parses common ISO-8601 timestamps into UTC.
func ParseISOUTC(ts string) (time.Time, bool) {
	s := strings.TrimSpace(ts)
	if s == "" {
		return time.Time{}, false
	}
	s = strings.Replace(s, " ", "T", 1)
	if strings.HasSuffix(s, "Z") {
		s = s[:len(s)-1] + "+00:00"
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.999999999-0700",
		"2006-01-02T15:04:05-0700",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), true
		}
	}
	for _, layout := range []string{"2006-01-02T15:04:05.999999999", "2006-01-02T15:04:05"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// FormatCountdown renders human-readable remaining duration until targetTS.
func FormatCountdown(targetTS *string, nowUTC time.Time) string {
	if targetTS == nil || *targetTS == "" {
		return ""
	}
	t, ok := ParseISOUTC(*targetTS)
	if !ok {
		return ""
	}
	if nowUTC.IsZero() {
		nowUTC = time.Now().UTC()
	}
	secs := int(t.Sub(nowUTC).Seconds())
	if secs <= 0 {
		return "due now"
	}
	hours := secs / 3600
	mins := (secs % 3600) / 60
	if hours >= 24 {
		return fmt.Sprintf("%dd %dh", hours/24, hours%24)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %02dm", hours, mins)
	}
	if mins > 0 {
		return fmt.Sprintf("%dm", mins)
	}
	return fmt.Sprintf("%ds", secs)
}

// RunCmd executes an argv-only command with optional timeout and separate streams.
func RunCmd(spec CmdSpec) (CmdRes, error) {
	if len(spec.Argv) == 0 {
		return CmdRes{ExitCode: 2}, fmt.Errorf("empty command argv")
	}
	ctx := context.Background()
	var cancel context.CancelFunc
	if spec.TimeoutSec > 0 {
		ctx, cancel = context.WithTimeout(ctx, time.Duration(spec.TimeoutSec)*time.Second)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, spec.Argv[0], spec.Argv[1:]...)
	if spec.Cwd != "" {
		cmd.Dir = spec.Cwd
	}
	if spec.Env != nil {
		cmd.Env = spec.Env
	}
	var outBuf, errBuf bytes.Buffer
	outP, err1 := cmd.StdoutPipe()
	errP, err2 := cmd.StderrPipe()
	if err1 != nil || err2 != nil {
		return CmdRes{ExitCode: 1}, fmt.Errorf("pipe setup failed")
	}
	if serr := cmd.Start(); serr != nil {
		return CmdRes{ExitCode: 1}, serr
	}
	var wg sync.WaitGroup
	capCopy := func(dst *bytes.Buffer, src io.ReadCloser) {
		defer wg.Done()
		_, _ = io.Copy(dst, io.LimitReader(src, maxCmdOutBytes))
		_, _ = io.Copy(io.Discard, src)
	}
	wg.Add(2)
	go capCopy(&outBuf, outP)
	go capCopy(&errBuf, errP)
	wg.Wait()
	err := cmd.Wait()
	res := CmdRes{
		Stdout:   outBuf.String(),
		Stderr:   errBuf.String(),
		Combined: outBuf.String() + errBuf.String(),
	}
	if ctx.Err() == context.DeadlineExceeded {
		res.TimedOut = true
		res.ExitCode = -1
		return res, ctx.Err()
	}
	if err == nil {
		return res, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		res.ExitCode = exitErr.ExitCode()
		return res, nil
	}
	res.ExitCode = 1
	return res, err
}

// TryRepoRoot returns the enclosing git toplevel directory, or empty string.
func TryRepoRoot(startDir string) string {
	res, err := RunCmd(CmdSpec{
		Argv:       []string{"git", "rev-parse", "--show-toplevel"},
		Cwd:        startDir,
		TimeoutSec: 15,
	})
	if err != nil || res.ExitCode != 0 {
		return ""
	}
	return strings.TrimSpace(res.Stdout)
}

// RepoRoot returns the enclosing git repository root or an ExitError(2).
func RepoRoot(startDir string) (string, error) {
	root := TryRepoRoot(startDir)
	if root == "" {
		return "", UsageErr("error: not inside a git repository")
	}
	return root, nil
}

// ResolveCLI finds an executable cliName on PATH or fallback paths, skipping shims.
func ResolveCLI(cliName string) string {
	if !SafeIDRe.MatchString(cliName) {
		return ""
	}
	var cands []string
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" || strings.Contains(dir, shimMarker) {
			continue
		}
		cands = append(cands, filepath.Join(dir, cliName))
	}
	cands = append(cands, CLIFallbackPaths(cliName)...)
	for _, cand := range cands {
		st, err := os.Stat(cand)
		if err != nil || st.IsDir() || (st.Mode()&0o111) == 0 {
			continue
		}
		if strings.Contains(cand, shimMarker) {
			continue
		}
		if realPath, rerr := filepath.EvalSymlinks(cand); rerr == nil {
			if strings.Contains(realPath, shimMarker) {
				continue
			}
		}
		return cand
	}
	return ""
}
