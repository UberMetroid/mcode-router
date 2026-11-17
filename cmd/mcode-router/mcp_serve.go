// MCP stdio transport: newline-delimited JSON-RPC 2.0 for MiniMax Code.
package main

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"

	"github.com/UberMetroid/mcode-router/internal/fleet"
)

const (
	mcpProto   = "2024-11-05"
	mcpName    = "mcode-router"
	mcpMaxLine = 8 << 20
)

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type mcpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *mcpError       `json:"error,omitempty"`
}

type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// mcpServe runs the JSON-RPC read loop until stdin closes.
func mcpServe(in io.Reader, out io.Writer) int {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), mcpMaxLine)
	for scanner.Scan() {
		mcpHandleLine(scanner.Text(), out)
	}
	return 0
}

// mcpHandleLine decodes one inbound message and routes it.
func mcpHandleLine(line string, out io.Writer) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return
	}
	var req mcpRequest
	if err := json.Unmarshal([]byte(trimmed), &req); err != nil {
		return
	}
	mcpDispatch(req, out)
}

// mcpDispatch answers one MCP method call.
func mcpDispatch(req mcpRequest, out io.Writer) {
	switch req.Method {
	case "initialize":
		mcpOK(req.ID, mcpInit(req.Params), out)
	case "notifications/initialized", "notifications/cancelled":
	case "ping":
		mcpOK(req.ID, map[string]any{}, out)
	case "tools/list":
		mcpOK(req.ID, map[string]any{"tools": mcpToolSpecs()}, out)
	case "tools/call":
		mcpCallTool(req, out)
	case "resources/list":
		mcpOK(req.ID, map[string]any{"resources": []any{}}, out)
	case "prompts/list":
		mcpOK(req.ID, map[string]any{"prompts": []any{}}, out)
	default:
		if len(req.ID) > 0 {
			mcpFail(req.ID, -32601, "method not found: "+req.Method, out)
		}
	}
}

// mcpInit builds the initialize handshake result, echoing the client version.
func mcpInit(raw json.RawMessage) map[string]any {
	version := mcpProto
	if requested := mcpStr(mcpArgs(raw), "protocolVersion"); requested != "" {
		version = requested
	}
	return map[string]any{
		"protocolVersion": version,
		"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
		"serverInfo":      map[string]any{"name": mcpName, "version": fleet.Version},
	}
}

// mcpOK writes a successful JSON-RPC response.
func mcpOK(id json.RawMessage, result any, out io.Writer) {
	mcpWrite(out, mcpResponse{JSONRPC: "2.0", ID: id, Result: result})
}

// mcpFail writes a JSON-RPC error response.
func mcpFail(id json.RawMessage, code int, message string, out io.Writer) {
	mcpWrite(out, mcpResponse{JSONRPC: "2.0", ID: id, Error: &mcpError{Code: code, Message: message}})
}

// mcpWrite emits one newline-delimited JSON message on stdout.
func mcpWrite(out io.Writer, payload any) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_, _ = out.Write(append(encoded, '\n'))
}
