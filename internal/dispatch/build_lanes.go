package dispatch

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/UberMetroid/mcode-router/internal/fleet"
	"github.com/UberMetroid/mcode-router/internal/worktree"
)

func buildDevinCmd(req CmdReq, model, promptFile string) (BuiltCmd, error) {
	ro := req.ReadOnly || req.ReviewMode != ""
	perm := "smart"
	if ro {
		perm = "auto"
	}
	argv := []string{req.CLIPath}
	if model != "" && model != "default" {
		argv = append(argv, "--model", model)
	}
	argv = append(argv, "--permission-mode", perm, "--respect-workspace-trust", "false",
		"--verbosity", "3", "--prompt-file", promptFile, "-p")
	return BuiltCmd{Argv: argv, Env: childEnv(fleet.RouterDepth()+1, req.Provider, ro),
		PromptFile: promptFile, StreamFmt: "text"}, nil
}

func buildMcodeCmd(req CmdReq, model, promptFile string) (BuiltCmd, error) {
	argv := []string{req.CLIPath, "exec"}
	if req.ReviewMode == "native" {
		argv = append(argv, "review")
	}
	argv = append(argv, "--cwd", req.Worktree)
	if model != "" && model != "default" {
		argv = append(argv, "--model", model)
	}
	if req.Effort != "" {
		if eff := fleet.McodeEffortMap[strings.ToLower(req.Effort)]; eff != "" && eff != "default" {
			argv = append(argv, "--effort", eff)
		} else if fleet.McodeEffortMap[strings.ToLower(req.Effort)] == "" {
			argv = append(argv, "--effort", "high")
		}
	}
	if req.ReviewMode == "native" {
		argv = append(argv, "--permission", "smart", "--max-steps", "15", "--output-format", "json")
		if req.Prompt != "" {
			argv = append(argv, "--append-system-prompt-file", promptFile)
		}
		return BuiltCmd{Argv: argv, Env: childEnv(fleet.RouterDepth()+1, req.Provider, true),
			PromptFile: promptFile, StreamFmt: "plain"}, nil
	}
	if req.ReviewMode != "" {
		steps := "35"
		if strings.Contains(req.Prompt, "<diff capped") {
			steps = "45"
		}
		argv = append(argv, "--permission", "smart", "--prompt-mode", "coding",
			"--max-steps", steps, "--output-format", "stream-json", "--input", "-")
		return BuiltCmd{Argv: argv, Env: childEnv(fleet.RouterDepth()+1, req.Provider, true),
			PromptFile: promptFile, StreamFmt: "ndjson", Stdin: req.Prompt}, nil
	}
	perm := "full"
	if req.ReadOnly {
		perm = "smart"
	}
	argv = append(argv, "--permission", perm, "--prompt-mode", "coding",
		"--output-format", "stream-json", "--input", "-")
	if req.ReadOnly && req.ReviewMode == "" {
		argv = append(argv, "--max-steps", "20")
	}
	for _, f := range worktree.SafeWorktreeFiles(req.Files, req.Worktree) {
		argv = append(argv, "--file", f)
	}
	return BuiltCmd{Argv: argv, Env: childEnv(fleet.RouterDepth()+1, req.Provider, req.ReadOnly),
		PromptFile: promptFile, StreamFmt: "ndjson", Stdin: req.Prompt}, nil
}

func buildGrokCmd(req CmdReq, model, promptFile string) (BuiltCmd, error) {
	ro := req.ReadOnly || req.ReviewMode != ""
	perm := "bypassPermissions"
	if ro {
		perm = "plan"
	}
	argv := []string{req.CLIPath, "--cwd", req.Worktree, "--permission-mode", perm,
		"--prompt-file", promptFile, "--output-format", "streaming-json"}
	if model != "" && model != "default" {
		argv = append(argv, "--model", model)
	}
	if req.Effort != "" {
		eff := fleet.GrokEffortMap[strings.ToLower(req.Effort)]
		if eff == "" {
			eff = "high"
		}
		argv = append(argv, "--reasoning-effort", eff)
	}
	return BuiltCmd{
		Argv: argv, Env: childEnv(fleet.RouterDepth()+1, req.Provider, ro),
		PromptFile: promptFile, StreamFmt: "ndjson",
	}, nil
}

func buildMistralCmd(req CmdReq, model, promptFile, safeTID string) (BuiltCmd, error) {
	ro := req.ReadOnly || req.ReviewMode != ""
	exportDir := filepath.Join(req.SlotDir, "vibe-export-"+safeTID)
	_ = os.RemoveAll(exportDir)
	approve := "--auto-approve"
	if ro {
		approve = "--smart-approve"
	}
	argv := []string{req.CLIPath, "--workdir", req.Worktree, "--trust", approve,
		"--output", "streaming", "--output-dir", exportDir, "--prompt-file", promptFile}
	if model != "" && model != "default" {
		argv = append(argv, "--model", model)
	}
	return BuiltCmd{
		Argv: argv, Env: childEnv(fleet.RouterDepth()+1, req.Provider, ro),
		PromptFile: promptFile, VibeExportDir: exportDir, StreamFmt: "ndjson",
	}, nil
}

func buildMuseCmd(req CmdReq, model, promptFile string) (BuiltCmd, error) {
	ro := req.ReadOnly || req.ReviewMode != ""
	argv := []string{req.CLIPath, "exec", "--workspace", req.Worktree, "--yolo",
		"--json", "--prompt-file", promptFile}
	if ro {
		argv = append(argv, "--disable-write")
	}
	if req.Effort != "" {
		eff := strings.ToLower(req.Effort)
		if fleet.MuseValidEfforts[eff] {
			argv = append(argv, "--reasoning-effort", eff)
		}
	}
	if model != "" && model != "default" {
		argv = append(argv, "--model", model)
	}
	return BuiltCmd{
		Argv: argv, Env: childEnv(fleet.RouterDepth()+1, req.Provider, ro),
		PromptFile: promptFile, StreamFmt: "ndjson",
	}, nil
}
