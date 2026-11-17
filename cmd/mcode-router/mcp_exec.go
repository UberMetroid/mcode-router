// MCP execution: runs mcode-router verbs in-process and captures their output.
package main

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/UberMetroid/mcode-router/internal/cli"
)

// mcpStage runs an observe, orient or act stage verb.
func mcpStage(verb string, params map[string]any) (string, bool) {
	argv, ok := mcpStageArgv(verb, params)
	if !ok {
		return "prompt is required for " + verb, true
	}
	return mcpExec(argv)
}

// mcpSetStage pins or resets a stage lane binding.
func mcpSetStage(params map[string]any) (string, bool) {
	argv, ok := mcpSetArgv(params)
	if !ok {
		return "stage and lane are required", true
	}
	return mcpExec(argv)
}

// mcpExec runs mcode-router in-process and captures stdout and stderr.
func mcpExec(argv []string) (string, bool) {
	var outBuf, errBuf bytes.Buffer
	code := cli.RunCLI("mcode-router", argv, &outBuf, &errBuf)
	text := strings.TrimRight(outBuf.String(), "\n")
	errText := strings.TrimRight(errBuf.String(), "\n")
	if errText != "" {
		if text != "" {
			text += "\n\nstderr:\n" + errText
		} else {
			text = errText
		}
	}
	if code != 0 {
		return fmt.Sprintf("exit=%d\n%s", code, text), true
	}
	return text, false
}
