package dispatch

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/UberMetroid/mcode-router/internal/fleet"
	"github.com/UberMetroid/mcode-router/internal/verify"
	"github.com/UberMetroid/mcode-router/internal/worktree"
)

var maxLineBytes = 1 << 20

// readStream reads one unit of a worker stream: a bounded line for line-oriented
// lanes, or a raw chunk for text lanes that stream model text without line breaks.
func readStream(br *bufio.Reader, limit int, mode string) (string, error) {
	if mode == "text" {
		buf := make([]byte, 4096)
		n, err := br.Read(buf)
		return string(buf[:n]), err
	}
	var out []byte
	for {
		frag, err := br.ReadSlice('\n')
		if len(out) < limit {
			out = append(out, frag[:min(len(frag), limit-len(out))]...)
		}
		if err != bufio.ErrBufferFull {
			if len(out) > 0 && out[len(out)-1] != '\n' {
				out = append(out, '\n')
			}
			return string(out), err
		}
	}
}

// BatchOpts configures parallel manifest execution across git worktrees.
type BatchOpts struct {
	Lane, Model, Effort, Timeout, QuotaPath      string
	VerifyLane, VerifyModel, VerifyEffort        string
	Skills, ExcludeLanes                         []string
	Jobs                                         int
	Stripe, NoStripe, NoReview, NoPivot, SoftPin bool
	LogWriter                                    io.Writer
	TaskRunner                                   func(ExecReq) ExecRes
	ReviewRunner                                 verify.ReviewRunner
	ResolveCLI                                   func(string) string
}

type batchJob struct {
	idx                       int
	task                      worktree.TaskSpec
	root, wt, branch, baseRef string
	wtErr                     error
	wtMu                      *sync.Mutex
}

// ensureBatchTree lazily creates the task worktree under the shared wtMu so
// concurrent `git worktree add` calls serialize on the repo .git dir.
func ensureBatchTree(job *batchJob, opts BatchOpts) {
	job.wtMu.Lock()
	defer job.wtMu.Unlock()
	p, br, err := worktree.EnsureWorktree(job.root, job.task.ID)
	job.wt, job.branch, job.wtErr = p, br, err
	if err != nil {
		job.wt = filepath.Join(job.root, ".worktrees", job.task.ID)
		job.branch = "ooda/" + job.task.ID
		fmt.Fprintf(opts.LogWriter, "[%s] worktree error: %v\n", job.task.ID, err)
		return
	}
	job.baseRef = verify.HeadRef(p)
	fmt.Fprintf(opts.LogWriter, "[%s] worktree=%s branch=%s\n", job.task.ID, p, br)
}

func reviewWorktreeTask(task worktree.TaskSpec, wt string, slotNum int,
	changed, viols []string, opts BatchOpts, authors []string, dc verify.DiffContext) BatchReview {
	if len(viols) > 0 {
		return BatchReview{Model: fleet.ReviewModelEnv(), Verdict: "REJECT", ExitCode: fleet.Ptr(1),
			Reason: "scope violation", Output: "[P0] scope violation: " + strings.Join(viols, ", ")}
	}
	if opts.NoReview || (task.Review != nil && !*task.Review) {
		return BatchReview{Model: fleet.ReviewModelEnv(), Verdict: "SKIPPED", Reason: "disabled"}
	}
	if len(changed) == 0 {
		return BatchReview{Model: fleet.ReviewModelEnv(), Verdict: "SKIPPED", Reason: "no changes in worktree"}
	}
	revSlot, err := worktree.EnsureSlot(fmt.Sprintf("review-%d", slotNum))
	if err != nil {
		return BatchReview{Ran: true, Model: fleet.ReviewModelEnv(), Verdict: "UNKNOWN",
			ExitCode: fleet.Ptr(1), Reason: err.Error()}
	}
	res := ExecReview(verify.ReviewReq{TargetDir: wt, SlotDir: revSlot, DiffCtx: dc,
		TimeoutSec: fleet.ReviewTimeout, ScopeViols: viols, Declared: task.Files, LogWriter: opts.LogWriter,
		ReqLane: opts.VerifyLane, Model: opts.VerifyModel, Effort: opts.VerifyEffort,
		AuthorLanes: authors, QuotaPath: opts.QuotaPath, Runner: opts.ReviewRunner, ResolveCLI: opts.ResolveCLI})
	return BatchReview{Ran: res.Ran, Lane: res.Lane, Model: res.Model, Verdict: res.Verdict,
		ExitCode: fleet.Ptr(res.ExitCode), Reason: res.Reason, Output: tailRunes(res.Output, 4000)}
}

func dedupMerge(a, b []string) []string {
	seen, out := make(map[string]bool, len(a)+len(b)), []string(nil)
	for _, s := range append(append([]string(nil), a...), b...) {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func processBatchTask(slotPool chan int, job batchJob, opts BatchOpts) BatchRecord {
	slotNum := <-slotPool
	defer func() { slotPool <- slotNum }()
	ensureBatchTree(&job, opts)
	if job.wtErr != nil {
		return BatchRecord{ID: job.task.ID, Branch: job.branch, Worktree: filepath.Base(job.wt), ExitCode: 1,
			Output: job.wtErr.Error(), Review: BatchReview{Model: fleet.ReviewModelEnv(),
				Verdict: "SKIPPED", Reason: "worktree error"}}
	}
	slotDir, err := worktree.EnsureSlot(strconv.Itoa(slotNum))
	if err != nil {
		return BatchRecord{ID: job.task.ID, Branch: job.branch, Worktree: filepath.Base(job.wt), ExitCode: -1,
			Output: err.Error(), Review: BatchReview{Model: fleet.ReviewModelEnv(), Verdict: "SKIPPED", Reason: "slot error"}}
	}
	tCopy := job.task
	if tCopy.Complexity == "" {
		tCopy.Complexity = worktree.InferComplexity(tCopy.Prompt, tCopy.Files, tCopy.ReadOnly)
	}
	tCopy.Skills = worktree.ResolveTaskSkills(dedupMerge(job.task.Skills, worktree.ParseCSVArg(opts.Skills)),
		tCopy.Prompt, tCopy.Files, job.root)
	excl := dedupMerge(job.task.ExcludeLanes, worktree.ParseCSVArg(opts.ExcludeLanes))
	var stripe *int
	if opts.Stripe && !opts.NoStripe {
		stripe = &job.idx
	}
	res, runErr := RunTaskWithFallback(TaskReq{Task: tCopy, Worktree: job.wt, SlotDir: slotDir,
		Lane: opts.Lane, Model: opts.Model, Effort: opts.Effort, TimeoutRaw: opts.Timeout, QuotaPath: opts.QuotaPath,
		StripeIdx: stripe, IsParallel: true, Exclude: excl, NoPivot: opts.NoPivot, SoftPin: opts.SoftPin,
		Runner: opts.TaskRunner, ResolveCLI: opts.ResolveCLI, LogWriter: opts.LogWriter})
	if runErr != nil {
		res = TaskRes{ExitCode: -1, Output: runErr.Error(), RequestedLane: opts.Lane}
	}
	var changed, viols []string
	var stat string
	rev := BatchReview{Model: fleet.ReviewModelEnv(), Verdict: "SKIPPED", Reason: "worker exited non-zero"}
	if bErr := worktree.VerifyWorktreeBinding(job.root, job.wt); bErr != nil {
		viols = []string{"@GIT_BINDING_TAMPERED"}
		rev = BatchReview{Model: fleet.ReviewModelEnv(), Verdict: "REJECT", ExitCode: fleet.Ptr(1),
			Reason: "worktree git binding tampered", Output: "[P0] " + bErr.Error()}
	} else {
		dc := verify.CollectDiffContext(job.wt, false)
		if job.baseRef != "" && verify.HeadRef(job.wt) != job.baseRef {
			dc = verify.CollectDiffContext(job.wt, false, job.baseRef)
		}
		changed, stat = dc.ChangedFiles, dc.DiffStat
		viols = worktree.FindScopeViolations(changed, job.task.Files)
		if res.ExitCode == 0 {
			rev = reviewWorktreeTask(job.task, job.wt, slotNum, changed, viols, opts, res.UsedLanes(), dc)
		}
	}
	relWT, rerr := filepath.Rel(job.root, job.wt)
	if rerr != nil {
		relWT = filepath.Base(job.wt)
	}
	return BatchRecord{ID: job.task.ID, Branch: job.branch, Worktree: relWT, Slot: filepath.Base(slotDir),
		RequestedLane: res.RequestedLane, Lane: res.Lane, CLI: res.CLI, Model: res.Model, Effort: res.Effort,
		FallbackFrom: res.FallbackFrom, Attempts: res.Attempts, ExitCode: res.ExitCode, TimedOut: res.TimedOut,
		DurationS: res.DurationS, ChangedFiles: changed, ScopeViolations: viols, DiffStat: stat,
		Output: tailRunes(res.Output, 4000), Review: rev}
}

func runBatchWorkers(root string, tasks []worktree.TaskSpec, opts BatchOpts, jobs int, outW io.Writer) []BatchRecord {
	slotPool := make(chan int, jobs)
	for n := 0; n < jobs; n++ {
		_, _ = worktree.EnsureSlot(strconv.Itoa(n))
		slotPool <- n
	}
	wtMu := &sync.Mutex{}
	items, ch := make([]batchJob, len(tasks)), make(chan int, len(tasks))
	for i, t := range tasks {
		items[i] = batchJob{idx: i, task: t, root: root, wtMu: wtMu}
		ch <- i
	}
	close(ch)
	var results []BatchRecord
	var wg sync.WaitGroup
	for w := 0; w < jobs; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range ch {
				rec := processBatchTask(slotPool, items[i], opts)
				wtMu.Lock()
				results = append(results, rec)
				st := map[bool]string{true: fmt.Sprintf("FAIL(%d)", rec.ExitCode), false: "OK"}[rec.ExitCode != 0]
				viol := ""
				if len(rec.ScopeViolations) > 0 {
					viol = fmt.Sprintf(" SCOPE_VIOLATION(%d)", len(rec.ScopeViolations))
				}
				fmt.Fprintf(outW, "[%s] %s lane=%s effort=%s %.1fs review=%s%s\n%s\n---\n",
					rec.ID, st, rec.Lane, rec.Effort, rec.DurationS, rec.Review.Verdict, viol, rec.DiffStat)
				wtMu.Unlock()
			}
		}()
	}
	wg.Wait()
	return results
}

// RunManifestBatch fans tasks out across worktrees, writes results.json, and returns exit code.
func RunManifestBatch(root string, tasks []worktree.TaskSpec, opts BatchOpts) int {
	if opts.LogWriter == nil {
		opts.LogWriter = os.Stdout
	}
	if absRoot, err := filepath.Abs(root); err == nil {
		root = absRoot
	}
	for _, lane := range worktree.ParseCSVArg(opts.ExcludeLanes) {
		if _, err := fleet.NormalizeLane(lane, false); err != nil {
			fmt.Fprintln(opts.LogWriter, err)
			return 2
		}
	}
	jobs := min(max(1, opts.Jobs), max(1, len(tasks)))
	_ = os.MkdirAll(filepath.Join(root, ".worktrees"), 0o755)
	results := runBatchWorkers(root, tasks, opts, jobs, opts.LogWriter)
	sort.Slice(results, func(i, j int) bool { return results[i].ID < results[j].ID })
	outPath := filepath.Join(root, ".worktrees", "results.json")
	if werr := fleet.AtomicWriteJSON(outPath, results, false, false); werr != nil {
		fmt.Fprintf(opts.LogWriter, "error: write %s: %v\n", outPath, werr)
		return 1
	}
	fmt.Fprintf(opts.LogWriter, "wrote %s\n", outPath)
	var failed []string
	for _, r := range results {
		revBad := r.Review.ExitCode != nil && (r.Review.Verdict != "ACCEPT" || *r.Review.ExitCode != 0)
		if r.ExitCode != 0 || len(r.ScopeViolations) > 0 || r.Review.Verdict == "REJECT" || revBad {
			failed = append(failed, r.ID)
		}
	}
	if len(failed) > 0 {
		fmt.Fprintf(opts.LogWriter, "%d task(s) failed/rejected: %s\n", len(failed), strings.Join(failed, ", "))
		return 1
	}
	fmt.Fprintf(opts.LogWriter, "all tasks exited 0\n")
	return 0
}
