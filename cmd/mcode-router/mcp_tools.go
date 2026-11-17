// MCP tool catalogue: OODA stage verbs and fleet introspection for MiniMax Code.
package main

import (
	"io"
)

type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// mcpToolSpecs returns the advertised tool catalogue.
func mcpToolSpecs() []mcpTool {
	tools := mcpStageTools()
	tools = append(tools, mcpVerifyTools()...)
	return append(tools, mcpAdminTools()...)
}

// mcpStageTools returns the observe, orient and act stage tools.
func mcpStageTools() []mcpTool {
	return []mcpTool{
		{
			Name: "ooda_recon",
			Description: "OBSERVE stage, read-only. Answer a question about a repository's " +
				"structure, current state, build and test commands, or the location of a " +
				"symbol. Never writes files. The lane, model and effort are resolved " +
				"automatically from quota state and the capability regime.",
			InputSchema: mcpSchema(map[string]any{
				"prompt":     mcpProp("string", "Specific question. Name exact files or symbols."),
				"lane":       mcpProp("string", "Force a lane. Omit for automatic selection."),
				"complexity": mcpProp("string", "Capability regime."),
				"timeout":    mcpProp("string", "Per-task timeout such as 180s or 5m."),
			}, []string{"prompt"}),
		},
		{
			Name: "ooda_orient",
			Description: "ORIENT stage, read-only. Synthesize root cause, invariants, blast " +
				"radius and file coupling for a problem, then propose an ordered plan. Use " +
				"after observe and before acting. Never writes files.",
			InputSchema: mcpSchema(map[string]any{
				"prompt":     mcpProp("string", "Question to analyze and plan against."),
				"lane":       mcpProp("string", "Force a lane. Omit for automatic selection."),
				"complexity": mcpProp("string", "Capability regime."),
				"timeout":    mcpProp("string", "Per-task timeout such as 600s or 10m."),
			}, []string{"prompt"}),
		},
		{
			Name: "ooda_task",
			Description: "ACT stage. Execute a scoped coding task in the working directory or " +
				"an isolated worktree. The lane and model are resolved from quota state and " +
				"the capability regime, so the same call can land on a building model such " +
				"as muse-ember-1.0 or swe-2-max. Give an exact spec: goal, scope, rules, " +
				"acceptance checks, stop conditions.",
			InputSchema: mcpSchema(map[string]any{
				"prompt":     mcpProp("string", "Exact build spec with acceptance checks."),
				"files":      mcpProp("string", "Comma-separated paths in scope."),
				"lane":       mcpProp("string", "Force a lane. Omit for automatic selection."),
				"complexity": mcpProp("string", "Capability regime."),
				"model":      mcpProp("string", "Force a model id."),
				"effort":     mcpProp("string", "Force reasoning effort."),
				"profile":    mcpProp("string", "Preset profile such as auto, mcode or devin."),
				"skill":      mcpProp("string", "Comma-separated worker skills to inject."),
				"timeout":    mcpProp("string", "Per-task timeout such as 480s or 8m."),
			}, []string{"prompt"}),
		},
	}
}

// mcpVerifyTools returns the independent cross-model review tool.
func mcpVerifyTools() []mcpTool {
	return []mcpTool{
		{
			Name: "ooda_review",
			Description: "VERIFY stage. Independent cross-model review of the working-tree " +
				"diff. The reviewer never comes from the authoring model family. Use after " +
				"a task to obtain an off-family verdict before landing.",
			InputSchema: mcpSchema(map[string]any{
				"path":         mcpProp("string", "Repository path to review. Defaults to the working directory."),
				"lane":         mcpProp("string", "Force a reviewer lane."),
				"exclude_lane": mcpProp("string", "Lane to exclude from selection."),
				"model":        mcpProp("string", "Force a reviewer model id."),
				"effort":       mcpProp("string", "Force reviewer effort."),
				"timeout":      mcpProp("string", "Review timeout such as 240s."),
			}, []string{}),
		},
	}
}

// mcpAdminTools returns the fleet introspection and stage-pinning tools.
func mcpAdminTools() []mcpTool {
	return []mcpTool{
		{
			Name: "ooda_status",
			Description: "Fleet and routing state: lane status, tier, live model, reasoning " +
				"effort, quota reset windows, parallel slots, and the effective " +
				"observe/orient/act/verify routing matrix. Use before choosing a lane, or " +
				"to explain why a stage resolved to a particular model.",
			InputSchema: mcpSchema(map[string]any{}, []string{}),
		},
		{
			Name:        "ooda_usage",
			Description: "Token and cost telemetry per lane for the current quota cycles.",
			InputSchema: mcpSchema(map[string]any{}, []string{}),
		},
		{
			Name: "ooda_set_stage",
			Description: "Pin an OODA stage to a lane, or reset a pin, so routing becomes " +
				"deterministic for a repository or globally. Stages are observe, orient, " +
				"act and verify.",
			InputSchema: mcpSchema(map[string]any{
				"stage": mcpProp("string", "Stage name, or preset."),
				"lane":  mcpProp("string", "Lane to pin, or auto to clear."),
				"local": mcpProp("boolean", "Pin for this repository only."),
			}, []string{"stage"}),
		},
	}
}

// mcpCallTool handles the tools/call method.
func mcpCallTool(req mcpRequest, out io.Writer) {
	args := mcpArgs(req.Params)
	name, _ := args["name"].(string)
	params, _ := args["arguments"].(map[string]any)
	text, failed := mcpRunTool(name, params)
	mcpOK(req.ID, map[string]any{
		"content": []any{map[string]any{"type": "text", "text": text}},
		"isError": failed,
	}, out)
}

// mcpRunTool dispatches a tool name to its mcode-router verb.
func mcpRunTool(name string, params map[string]any) (string, bool) {
	switch name {
	case "ooda_recon":
		return mcpStage("--recon", params)
	case "ooda_orient":
		return mcpStage("--orient", params)
	case "ooda_task":
		return mcpStage("--task", params)
	case "ooda_review":
		return mcpExec(mcpReviewArgv(params))
	case "ooda_status":
		return mcpExec([]string{"--status"})
	case "ooda_usage":
		return mcpExec([]string{"--usage"})
	case "ooda_set_stage":
		return mcpSetStage(params)
	}
	return "unknown tool: " + name, true
}
