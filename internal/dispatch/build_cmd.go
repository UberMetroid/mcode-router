package dispatch

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/UberMetroid/mcode-router/internal/fleet"
	"github.com/UberMetroid/mcode-router/internal/worktree"
)

// CmdReq defines parameters to construct a single headless CLI invocation.
type CmdReq struct {
	Provider, CLIPath, Model, Effort, Prompt, Worktree, SlotDir, TaskID, ReviewMode string
	Files                                                                           []string
	ReadOnly                                                                        bool
}

// BuiltCmd holds the argv, environment, stream mode, and temp paths for cleanup.
type BuiltCmd struct {
	Argv                                 []string
	Env                                  []string
	PromptFile, VibeExportDir, StreamFmt string
	// Stdin carries the prompt to the child over standard input so it never
	// appears in the process argument list, where any local user could read
	// it from /proc.
	Stdin string
}

var roEnvExact = strings.Fields("PATH HOME USER LOGNAME SHELL TMPDIR TEMP TMP TZ LANG TERM " +
	"NO_COLOR COLORTERM SSL_CERT_FILE SSL_CERT_DIR CURL_CA_BUNDLE NODE_EXTRA_CA_CERTS " +
	"HTTPS_PROXY HTTP_PROXY NO_PROXY https_proxy http_proxy no_proxy GOROOT GOPATH GOMODCACHE " +
	"GOCACHE CARGO_HOME RUSTUP_HOME VIRTUAL_ENV NVM_DIR PYTHONPATH NODE_OPTIONS " +
	"SYSTEMROOT WINDIR COMSPEC APPDATA LOCALAPPDATA USERPROFILE")

var roEnvProv = map[string][]string{
	"grok": {"XAI_", "GROK_"}, "mcode": {"MINIMAX_"},
	"mistral": {"MISTRAL_", "ZAI_", "VIBE_"}, "muse": {"MUSE_"},
}

// provEnvAll lists every provider credential prefix the router knows about, so
// a worker can be denied the credentials of a provider it is not running. The
// list covers the router's own provider prefixes only: credentials the router
// does not own, such as CI tokens or cloud keys, still reach a write worker.
var provEnvAll = []string{"XAI_", "GROK_", "MINIMAX_", "MISTRAL_", "ZAI_", "VIBE_", "MUSE_", "DEVIN_"}

func childEnv(depth int, prov string, readOnly bool) []string {
	exact, pfxs := map[string]bool{}, append([]string{"LC_", "XDG_"}, roEnvProv[prov]...)
	if strings.HasPrefix(prov, "devin-") {
		pfxs = append(pfxs, "DEVIN_")
	}
	if readOnly {
		for _, k := range roEnvExact {
			exact[k] = true
		}
	}
	match := func(k string, list []string) bool {
		for _, p := range list {
			if (strings.HasSuffix(p, "_") && strings.HasPrefix(k, p)) || k == p {
				return true
			}
		}
		return false
	}
	var out []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch {
		case readOnly:
			if !exact[k] && !match(k, pfxs) {
				continue
			}
		case match(k, provEnvAll) && !match(k, pfxs):
			// Another provider's credential is not this worker's to carry.
			continue
		case strings.HasPrefix(kv, "ROUTER_DEPTH=") || strings.HasPrefix(kv, "MCODE_ROUTER_DEPTH=") ||
			strings.HasPrefix(kv, "GIT_TERMINAL_PROMPT="):
			continue
		}
		out = append(out, kv)
	}
	return append(out, fmt.Sprintf("ROUTER_DEPTH=%d", depth),
		fmt.Sprintf("MCODE_ROUTER_DEPTH=%d", depth), "GIT_TERMINAL_PROMPT=0")
}

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

// BuildWorkerCommand constructs argv, env, prompt file, and stream mode for a CLI lane.
func BuildWorkerCommand(req CmdReq) (BuiltCmd, error) {
	model, err := fleet.NormalizeModel(req.Provider, req.Model)
	if err != nil {
		return BuiltCmd{}, err
	}
	safeTID := fleet.SafeID(req.TaskID)
	var promptFile string
	writePrompt := func() error {
		if err := os.MkdirAll(req.SlotDir, 0o700); err != nil {
			return err
		}
		promptFile = filepath.Join(req.SlotDir, "prompt-"+safeTID+".md")
		return os.WriteFile(promptFile, []byte(req.Prompt), 0o600)
	}
	switch req.Provider {
	case "mcode":
		// The mcode lane carries the prompt on stdin, so nothing lands on
		// disk unless the native review branch needs a prompt file.
		if req.ReviewMode == "native" && req.Prompt != "" {
			if err := writePrompt(); err != nil {
				return BuiltCmd{}, err
			}
		}
	case "devin-fusion", "devin-pro", "devin-fast", "grok", "mistral", "muse":
		if err := writePrompt(); err != nil {
			return BuiltCmd{}, err
		}
	}
	switch req.Provider {
	case "devin-fusion", "devin-pro", "devin-fast":
		return buildDevinCmd(req, model, promptFile)
	case "mcode":
		return buildMcodeCmd(req, model, promptFile)
	case "grok":
		return buildGrokCmd(req, model, promptFile)
	case "mistral":
		return buildMistralCmd(req, model, promptFile, safeTID)
	case "muse":
		return buildMuseCmd(req, model, promptFile)
	default:
		return BuiltCmd{}, fleet.UsageErr("unsupported provider %q", req.Provider)
	}
}
