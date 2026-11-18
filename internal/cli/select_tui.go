package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/UberMetroid/mcode-router/internal/envelope"
	"github.com/UberMetroid/mcode-router/internal/fleet"
	"github.com/UberMetroid/mcode-router/internal/ui"
	"github.com/UberMetroid/mcode-router/internal/worktree"
)

func readTUIKey(r io.Reader) string {
	var b, b2, b3 [1]byte
	if n, err := r.Read(b[:]); err != nil || n == 0 {
		return "quit"
	}
	if b[0] == 0x1b {
		if n2, err2 := r.Read(b2[:]); err2 != nil || n2 == 0 || b2[0] == 0x1b {
			return "quit"
		}
		if b2[0] == '[' || b2[0] == 'O' {
			if n3, e3 := r.Read(b3[:]); e3 == nil && n3 == 1 {
				return map[byte]string{'A': "up", 'B': "down"}[b3[0]]
			}
		}
		return ""
	}
	return map[byte]string{
		'q': "quit", 'Q': "quit", 3: "quit", '\r': "select", '\n': "select",
		'b': "back", 'B': "back", 0x7f: "back", 0x08: "back",
		'\t': "tab", 'l': "tab", 'k': "up", 'K': "up", 'j': "down", 'J': "down",
	}[b[0]]
}

func tuiPick(in io.Reader, out io.Writer, title, hint string, items []string) (int, string) {
	idx := 0
	if hint != "" {
		hint = "  " + hint + "\n"
	}
	for {
		pal := ui.ResolvePalette(nil)
		fmt.Fprintf(out, "\033[H\033[2J%s[AGENT-OODA v%s] %s\033[0m\n%s%s\n",
			pal.TUIHeader, fleet.Version, title, hint, pal.Muted+strings.Repeat("-", 72)+"\033[0m")
		for i, item := range items {
			mark := "  "
			if i == idx {
				mark = pal.TUIAccent + "▸ "
			}
			fmt.Fprintf(out, "%s%s\033[0m\n", mark, item)
		}
		fmt.Fprintf(out, "\n  %s[↑/↓ j/k] move  [Enter] select  [b/Bksp] back  [q] quit\033[0m\n", pal.Muted)
		switch k := readTUIKey(in); k {
		case "up":
			idx = (idx - 1 + len(items)) % len(items)
		case "down":
			idx = (idx + 1) % len(items)
		case "select", "tab":
			return idx, k
		case "back", "quit":
			return -1, k
		}
	}
}

func tuiStageRows(cwd string, local bool, q *fleet.QuotaState) []string {
	ws, rows := worktree.LoadWorkspaceStages(cwd), make([]string, 0, len(fleet.StageNames)+3)
	for _, st := range fleet.StageNames {
		r := q.Stages[st]
		if local {
			r = fleet.StageRoute{Lane: "(inherit)"}
			if lr, ok := ws[st]; ok {
				r = lr
			}
		}
		lbl := nonEmptyOr(r.Lane, "auto")
		if r.Model != nil && *r.Model != "" {
			lbl += " · " + *r.Model
		}
		if r.Effort != nil && *r.Effort != "" {
			lbl += " (" + *r.Effort + ")"
		}
		rows = append(rows, fmt.Sprintf("%-8s -> %s", st, lbl))
	}
	return append(rows, "Apply Preset (auto/conserve/deep/devin/fusion/grok/mcode/muse/mistral)",
		"Reset All Stages in Scope to auto", "Save & Exit")
}

func tuiApplyAction(
	in io.Reader, out io.Writer, idx, nSt int, local, until bool, cwd string, q *fleet.QuotaState,
) (string, error) {
	if idx < nSt {
		return tuiEditStage(in, out, fleet.StageNames[idx], local, until, cwd, q)
	}
	p, lbl := worktree.StagePatch{Stage: "all", Lane: "auto", Local: local, Cwd: cwd}, "✓ Reset all stages to auto"
	if idx == nSt {
		names := []string{"auto", "conserve", "deep", "devin", "fusion", "grok", "mcode", "muse", "mistral"}
		pi, pact := tuiPick(in, out, "Apply Workflow Preset", "", names)
		if pact != "select" || pi < 0 {
			return "", nil
		}
		p.Stage, p.Lane, lbl = "preset", names[pi], "✓ Applied preset "+names[pi]
	}
	if _, err := worktree.ConfigureStage(p); err != nil {
		return "", err
	}
	return lbl, nil
}

func tuiEditStage(
	in io.Reader, out io.Writer, stage string, local, defUntil bool, cwd string, q *fleet.QuotaState,
) (string, error) {
	caps := envelope.DiscoverFleetCaps(envelope.DiscoverOpts{Quota: q})
	musePayg, nowUTC := fleet.CachedMuseProbe(900).PayAsYouGo, time.Now().UTC()
	lanes, items := []string{"auto"}, []string{"auto (quota-aware routing)"}
	for _, name := range fleet.ProviderOrder {
		info := q.Providers[name]
		if info == nil || info.Tier == "credits" || (name == "muse" && musePayg) {
			continue
		}
		note := info.Status
		if cd := fleet.FormatCountdown(info.CooldownUntil, nowUTC); info.Status == "cooldown" && cd != "" {
			note = "cooldown " + cd
		}
		lanes = append(lanes, name)
		items = append(items, fmt.Sprintf("%-13s %-9s (%s)", name, info.Tier, note))
	}
	li, act := tuiPick(in, out, "Select Lane for "+stage, "", items)
	if act != "select" || li < 0 {
		return "", nil
	}
	chosenLane, mod, eff, untilReset := lanes[li], "", "", defUntil
	if chosenLane != "auto" {
		mods := tuiLaneModels(chosenLane, caps[chosenLane])
		if mi, mact := tuiPick(in, out, chosenLane+" Model", "", mods); mact == "select" && mi > 0 {
			mod = mods[mi]
		} else if mact != "select" {
			return "", nil
		}
		if !strings.Contains(caps[chosenLane].EffortMode, "via-model") {
			effs := []string{"cli-default", "low", "medium", "high", "xhigh", "max"}
			if ei, eact := tuiPick(in, out, chosenLane+" Effort", "", effs); eact == "select" && ei > 0 {
				eff = effs[ei]
			} else if eact != "select" {
				return "", nil
			}
		}
		if !defUntil {
			pi, pact := tuiPick(in, out, "Pin Duration", "", []string{"Sticky (keep until changed)", "Until window resets"})
			if pact != "select" {
				return "", nil
			}
			untilReset = pi == 1
		}
	}
	if _, err := worktree.ConfigureStage(worktree.StagePatch{
		Stage: stage, Lane: chosenLane, Model: mod, Effort: eff, UntilReset: untilReset, Local: local, Cwd: cwd,
	}); err != nil {
		return "", err
	}
	return fmt.Sprintf("✓ Saved %s -> %s", stage, chosenLane), nil
}

func openTUIInput() (*os.File, func(), error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, nil, err
	}
	stty := func(args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "stty", args...)
		cmd.Stdin = tty
		return cmd.Output()
	}
	saved, serr := stty("-g")
	if serr != nil {
		_ = tty.Close()
		return nil, nil, serr
	}
	restore := func() {
		_, _ = stty(strings.TrimSpace(string(saved)))
		_ = tty.Close()
	}
	if _, rerr := stty("cbreak", "-echo", "-isig"); rerr != nil {
		restore()
		return nil, nil, rerr
	}
	return tty, restore, nil
}

// RunSelectTUI runs the arrow-key OODA stage/lane/model selector interactively.
func RunSelectTUI(opts CLIOpts, in io.Reader, outW, errW io.Writer) int {
	if outW == nil {
		outW = os.Stdout
	}
	cwd, ok := ResolveCWD(opts, errW)
	if !ok {
		return 1
	}
	if in == nil {
		tty, restore, err := openTUIInput()
		if err != nil {
			fmt.Fprintf(errW, "error: --select requires an interactive TTY (%v); use --set-stage instead\n", err)
			return 2
		}
		defer restore()
		in, outW = tty, tty
	}
	local, nSt, status := opts.Local, len(fleet.StageNames), ""
	for {
		q, err := fleet.LoadQuota("")
		if err != nil {
			fmt.Fprintln(errW, err)
			return 1
		}
		hint := "Scope: GLOBAL (quota.json)  [Tab: toggle scope]"
		if local {
			hint = "Scope: REPO-LOCAL (" + worktree.WorkspaceStagesPath(cwd) + ")  [Tab: toggle scope]"
		}
		if status != "" {
			pal := ui.ResolvePalette(nil)
			col := pal.TUIOK
			if strings.HasPrefix(status, "✗") {
				col = pal.FleetLow
			}
			hint += "  " + col + status + "\033[0m"
			status = ""
		}
		idx, act := tuiPick(in, outW, "OODA Stage Matrix", hint, tuiStageRows(cwd, local, q))
		if act == "tab" {
			local = !local
			continue
		}
		if act == "back" {
			continue
		}
		if act != "select" || idx == nSt+2 {
			return 0
		}
		msg, stepErr := tuiApplyAction(in, outW, idx, nSt, local, opts.UntilReset, cwd, q)
		if stepErr != nil {
			status = "✗ " + stepErr.Error()
		} else if msg != "" {
			status = msg
		}
	}
}
