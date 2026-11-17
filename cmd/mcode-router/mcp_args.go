// MCP argument helpers: JSON schema builders and mcode-router argv assembly.
package main

import (
	"encoding/json"
	"strings"
)

// mcpArgs decodes a JSON-RPC params object into a loose map.
func mcpArgs(raw json.RawMessage) map[string]any {
	out := map[string]any{}
	if len(raw) == 0 {
		return out
	}
	_ = json.Unmarshal(raw, &out)
	return out
}

// mcpStr reads a string argument, returning "" when absent or not a string.
func mcpStr(params map[string]any, key string) string {
	value, ok := params[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

// mcpAddOpt appends a flag and its value when the value is non-empty.
func mcpAddOpt(argv []string, flag, value string) []string {
	if strings.TrimSpace(value) == "" {
		return argv
	}
	return append(argv, flag, value)
}

// mcpSchema builds a JSON Schema object node with properties and required keys.
func mcpSchema(props map[string]any, required []string) map[string]any {
	if required == nil {
		required = []string{}
	}
	return map[string]any{
		"type":       "object",
		"properties": props,
		"required":   required,
	}
}

// mcpProp builds a single JSON Schema property node.
func mcpProp(kind, description string) map[string]any {
	return map[string]any{"type": kind, "description": description}
}

// mcpStageArgv assembles argv for the observe/orient/act stage verbs.
func mcpStageArgv(verb string, params map[string]any) ([]string, bool) {
	prompt := mcpStr(params, "prompt")
	if prompt == "" {
		return nil, false
	}
	argv := []string{verb, prompt}
	if verb == "--task" {
		argv = mcpAddOpt(argv, "--files", mcpStr(params, "files"))
	}
	argv = mcpAddOpt(argv, "--cwd", mcpStr(params, "cwd"))
	argv = mcpAddOpt(argv, "--lane", mcpStr(params, "lane"))
	argv = mcpAddOpt(argv, "--exclude-lane", mcpStr(params, "exclude_lane"))
	argv = mcpAddOpt(argv, "--complexity", mcpStr(params, "complexity"))
	argv = mcpAddOpt(argv, "--model", mcpStr(params, "model"))
	argv = mcpAddOpt(argv, "--effort", mcpStr(params, "effort"))
	argv = mcpAddOpt(argv, "--profile", mcpStr(params, "profile"))
	argv = mcpAddOpt(argv, "--skill", mcpStr(params, "skill"))
	argv = mcpAddOpt(argv, "--timeout", mcpStr(params, "timeout"))
	return argv, true
}

// mcpReviewArgv assembles argv for the read-only cross-model review verb.
func mcpReviewArgv(params map[string]any) []string {
	argv := []string{"--review"}
	if path := mcpStr(params, "path"); path != "" {
		argv = append(argv, path)
	}
	argv = mcpAddOpt(argv, "--lane", mcpStr(params, "lane"))
	argv = mcpAddOpt(argv, "--exclude-lane", mcpStr(params, "exclude_lane"))
	argv = mcpAddOpt(argv, "--model", mcpStr(params, "model"))
	argv = mcpAddOpt(argv, "--effort", mcpStr(params, "effort"))
	argv = mcpAddOpt(argv, "--timeout", mcpStr(params, "timeout"))
	return argv
}

// mcpSetArgv assembles argv for pinning or resetting an OODA stage lane.
func mcpSetArgv(params map[string]any) ([]string, bool) {
	stage := mcpStr(params, "stage")
	lane := mcpStr(params, "lane")
	if stage == "" || lane == "" {
		return nil, false
	}
	argv := []string{"--set-stage", stage + "=" + lane}
	if local, ok := params["local"].(bool); ok && local {
		argv = append(argv, "--local")
	}
	return argv, true
}
