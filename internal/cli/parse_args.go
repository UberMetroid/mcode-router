package cli

import (
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/UberMetroid/mcode-router/internal/fleet"
	"github.com/UberMetroid/mcode-router/internal/worktree"
)

// CLIOpts holds parsed command-line options for the mcode-router CLI.
type CLIOpts struct {
	Manifest, Complexity, Lane, Model, Effort, Timeout, Cwd       string
	Enable, Disable, SetCycle, Reason, SetStage, Profile          string
	Recon, Orient, Task, Review, Hygiene                          *string
	Files, Skills, ExcludeLanes                                   []string
	Jobs                                                          int
	Hours, CostBudget                                             *float64
	TokenBudget, MaxParallel                                      *int
	LaneSet, Stripe, NoStripe, NoPivot, NoReview, NoCache, Status bool
	Discover, Usage, Probe, AutoWake, ResetNow, Select, Local     bool
	ResetStages, UntilReset, Cleanup, Help, Version, Live         bool
}

func usageText(prog string) string {
	return fmt.Sprintf(
		"usage: %s [--version | --select | --status | --discover | --usage | --probe |\n"+
			"             --recon P | --orient P | --task P | --review [PATH] | --hygiene [PATH] |\n"+
			"             --cleanup | --set-stage S=L | --reset-stages | --enable P | --disable P |\n"+
			"             --set-cycle P | manifest]\n\n"+
			"options:\n  -h, --help             show this help message and exit\n"+
			"  -v, --version          show binary version and exit\n"+
			"  -s, --select           interactive arrow-key OODA stage & model selector\n"+
			"  --recon/--orient PROMPT  read-only Observe recon or Orient architecture analysis\n"+
			"  --task PROMPT          execute scoped coding task in CWD (use --cwd DIR to target a repo)\n"+
			"  --review [PATH]        standalone read-only cross-model review (default CWD)\n"+
			"  --hygiene [PATH]       0-token secret, key & hardening footgun scan (default CWD)\n"+
			"  --files / --skill CSV  scoped paths / skills to inject (repeatable)\n"+
			"  --complexity HINT      heavy, deep, architecture, fast, tests, recon, review\n"+
			"  --lane / --exclude-lane / --profile  auto, lane CSV, or preset profile\n"+
			"  --model / --effort     override worker model id or reasoning effort\n"+
			"  --timeout DUR          per-task timeout (e.g. 120s, 8m)\n"+
			"  -j, --jobs, --slots N  max parallel workers for manifest mode (default %d, max %d)\n"+
			"  --stripe/--no-stripe/--no-pivot/--no-review/--no-cache  stripe opt-in, pivot, verify, cache\n"+
			"  --status/--discover/--usage/--probe  stage matrix, fleet, CLI cache, telemetry, canary\n"+
			"  --live / --statusline    worker telemetry: model, effort, phase, tok/s\n"+
			"  --set-stage S=L / --reset-stages  pin stage (or preset=NAME); modifiers: --local, --until-reset\n"+
			"  --enable/--disable/--set-cycle P  quota/cycle admin (--auto-wake, --reason, --hours,\n"+
			"                         --token-budget, --cost-budget, --max-parallel, --reset-now)\n"+
			"  --cleanup              remove .worktrees and ooda/* branches\n",
		prog, fleet.DefaultJobs, fleet.MaxJobs,
	)
}

func takeArg(argv []string, idx *int, hasEq bool, eqVal, flag string) (string, error) {
	if hasEq {
		return eqVal, nil
	}
	if *idx+1 >= len(argv) {
		return "", fleet.UsageErr("error: argument %s: expected one argument", flag)
	}
	*idx++
	return argv[*idx], nil
}

func applyNumFlag(opts *CLIOpts, name, val string) error {
	if name == "--hours" || name == "--cost-budget" {
		f, err := strconv.ParseFloat(val, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 ||
			(name == "--hours" && f > fleet.MaxHoursCap) || (name == "--cost-budget" && f > 1e6) {
			return fleet.UsageErr("error: %s: invalid or out-of-range number %q", name, val)
		}
		*map[string]**float64{"--hours": &opts.Hours, "--cost-budget": &opts.CostBudget}[name] = &f
		return nil
	}
	n, err := strconv.Atoi(val)
	if err != nil || ((name == "--max-parallel" || name == "--token-budget") && n < 0) ||
		(name == "--max-parallel" && n > fleet.MaxParallelCap) {
		return fleet.UsageErr("error: %s: invalid or out-of-range integer %q", name, val)
	}
	switch name {
	case "--jobs", "-j", "--slots":
		opts.Jobs = n
	case "--token-budget":
		opts.TokenBudget = &n
	case "--max-parallel":
		opts.MaxParallel = &n
	}
	return nil
}

func applyStrFlag(opts *CLIOpts, name, val string) error {
	if name == "--lane" {
		opts.Lane, opts.LaneSet = val, true
		return nil
	}
	strMap := map[string]*string{
		"--model": &opts.Model, "--effort": &opts.Effort, "--timeout": &opts.Timeout, "--cwd": &opts.Cwd,
		"--complexity": &opts.Complexity, "--enable": &opts.Enable, "--disable": &opts.Disable,
		"--reason": &opts.Reason, "--set-cycle": &opts.SetCycle, "--set-stage": &opts.SetStage, "--profile": &opts.Profile,
	}
	if p, ok := strMap[name]; ok {
		*p = val
		return nil
	}
	switch name {
	case "--exclude-lane":
		opts.ExcludeLanes = append(opts.ExcludeLanes, val)
	case "--recon":
		opts.Recon = fleet.Ptr(val)
	case "--orient":
		opts.Orient = fleet.Ptr(val)
	case "--task":
		opts.Task = fleet.Ptr(val)
	case "--files":
		opts.Files = append(opts.Files, val)
	case "--skill", "--skills":
		opts.Skills = append(opts.Skills, val)
	default:
		return applyNumFlag(opts, name, val)
	}
	return nil
}

func applyBoolFlag(opts *CLIOpts, name string) bool {
	m := map[string]*bool{
		"--stripe": &opts.Stripe, "--no-stripe": &opts.NoStripe, "--no-pivot": &opts.NoPivot,
		"--no-review": &opts.NoReview, "--status": &opts.Status, "--discover": &opts.Discover, "--usage": &opts.Usage,
		"--probe": &opts.Probe, "--auto-wake": &opts.AutoWake, "--reset-now": &opts.ResetNow,
		"--cleanup": &opts.Cleanup, "-s": &opts.Select, "--select": &opts.Select, "--no-cache": &opts.NoCache,
		"--reset-stages": &opts.ResetStages, "--local": &opts.Local, "--until-reset": &opts.UntilReset,
		"-v": &opts.Version, "--version": &opts.Version, "-h": &opts.Help, "--help": &opts.Help,
		"--live": &opts.Live,
	}
	ptr, ok := m[name]
	if ok {
		*ptr = true
	}
	return ok
}

func consumeFlag(opts *CLIOpts, argv []string, i *int) error {
	arg := argv[*i]
	if strings.HasPrefix(arg, "-j") && len(arg) > 2 && arg[2] != '=' {
		return applyNumFlag(opts, "-j", arg[2:])
	}
	name, eqVal, hasEq := strings.Cut(arg, "=")
	if !hasEq && applyBoolFlag(opts, name) {
		return nil
	}
	if name == "--review" || name == "--hygiene" {
		dest := &opts.Review
		if name == "--hygiene" {
			dest = &opts.Hygiene
		}
		if hasEq {
			*dest = fleet.Ptr(eqVal)
		} else if *i+1 < len(argv) && !strings.HasPrefix(argv[*i+1], "-") {
			*i++
			*dest = fleet.Ptr(argv[*i])
		} else {
			*dest = fleet.Ptr(".")
		}
		return nil
	}
	knownVal := map[string]bool{
		"--lane": true, "--exclude-lane": true, "--model": true, "--effort": true, "--timeout": true, "--cwd": true,
		"--recon": true, "--orient": true, "--task": true, "--files": true, "--skill": true, "--skills": true,
		"--complexity": true, "--jobs": true, "-j": true, "--slots": true, "--enable": true, "--disable": true,
		"--reason": true, "--hours": true, "--set-cycle": true, "--set-stage": true, "--profile": true,
		"--token-budget": true, "--cost-budget": true, "--max-parallel": true,
	}
	if !knownVal[name] {
		return fleet.UsageErr("error: unrecognized arguments: %s", arg)
	}
	val, err := takeArg(argv, i, hasEq, eqVal, name)
	if err != nil {
		return err
	}
	return applyStrFlag(opts, name, val)
}

func validateModes(opts CLIOpts) error {
	opCount := 0
	for _, op := range []bool{opts.Recon != nil, opts.Orient != nil, opts.Task != nil} {
		if op {
			opCount++
		}
	}
	if opCount > 1 {
		return fleet.UsageErr("error: --recon, --orient, and --task are mutually exclusive")
	}
	quotaAdmin := opts.Enable != "" || opts.Disable != "" || opts.SetCycle != ""
	stageAdmin := opts.SetStage != "" || opts.ResetStages
	modes := 0
	for _, on := range []bool{
		opts.Version, opts.Select, opts.Status, opts.Discover, opts.Usage, opts.Probe,
		quotaAdmin, stageAdmin, opts.Cleanup, opts.Recon != nil, opts.Orient != nil,
		opts.Task != nil, opts.Review != nil, opts.Hygiene != nil, opts.Live,
	} {
		if on {
			modes++
		}
	}
	if modes > 1 {
		return fleet.UsageErr("error: conflicting mode flags; specify one action")
	}
	if opts.Manifest != "" && modes > 0 && opts.Review == nil && opts.Hygiene == nil {
		return fleet.UsageErr("error: unexpected positional manifest %q with mode flag", opts.Manifest)
	}
	if opts.Profile != "" {
		if _, ok := worktree.PresetStages(opts.Profile); !ok {
			return fleet.UsageErr("error: unknown --profile %q", opts.Profile)
		}
	}
	return nil
}

// ParseArgs parses ooda / router CLI flags and positional manifest path.
func ParseArgs(prog string, argv []string, helpOut io.Writer) (CLIOpts, error) {
	opts := CLIOpts{Lane: "auto", Jobs: fleet.DefaultJobs}
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		if arg == "--" {
			for j := i + 1; j < len(argv); j++ {
				if opts.Manifest != "" {
					return opts, fleet.UsageErr("error: unrecognized arguments: %s", argv[j])
				}
				opts.Manifest = argv[j]
			}
			break
		}
		if strings.HasPrefix(arg, "-") && arg != "-" {
			if err := consumeFlag(&opts, argv, &i); err != nil {
				return opts, err
			}
			continue
		}
		if opts.Manifest != "" {
			return opts, fleet.UsageErr("error: unrecognized arguments: %s", arg)
		}
		opts.Manifest = arg
	}
	if opts.Help {
		if helpOut == nil {
			helpOut = os.Stdout
		}
		_, _ = io.WriteString(helpOut, usageText(prog))
		return opts, nil
	}
	return opts, validateModes(opts)
}
