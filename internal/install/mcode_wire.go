// MCode adapter file copies and config merges.
package install

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type mcodeHookSpec struct {
	Event   string
	Name    string
	Command string
}

var mcodeHookSpecs = []mcodeHookSpec{
	{"SessionStart", "OODA fleet context",
		"mcode-router --status 2>/dev/null | head -40"},
	{"UserPromptSubmit", "OODA routing state",
		"mcode-router --status 2>/dev/null | sed -n '/OODA Stage Routing Matrix/,$p'"},
}

// mcodeCopySkills copies the plugin skills into the MCode skills directory.
func mcodeCopySkills(layout mcodeLayout, install bool) error {
	source := filepath.Join(layout.PluginDir, "skills")
	entries, err := os.ReadDir(source)
	if err != nil {
		if !install && os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("cannot read plugin skills: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		target := filepath.Join(layout.SkillsDir, entry.Name())
		if !install {
			os.RemoveAll(target)
			continue
		}
		src := filepath.Join(source, entry.Name(), "SKILL.md")
		if _, err := os.Stat(src); err != nil {
			continue
		}
		if err := os.MkdirAll(target, 0o755); err != nil {
			return err
		}
		if err := copyFileAtomic(src, filepath.Join(target, "SKILL.md")); err != nil {
			return err
		}
	}
	return nil
}

// mcodeCopyAgents copies the plugin agent profiles into the MCode agents directory.
func mcodeCopyAgents(layout mcodeLayout, install bool) error {
	source := filepath.Join(layout.PluginDir, "agents")
	entries, err := os.ReadDir(source)
	if err != nil {
		if !install && os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("cannot read plugin agents: %w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".md" {
			continue
		}
		target := filepath.Join(layout.AgentsDir, name[:len(name)-3])
		if !install {
			os.RemoveAll(target)
			continue
		}
		if err := os.MkdirAll(target, 0o755); err != nil {
			return err
		}
		if err := copyFileAtomic(filepath.Join(source, name), filepath.Join(target, "agent.md")); err != nil {
			return err
		}
	}
	return nil
}

// mcodeMergeMCP registers or removes the mcode-router MCP server entry.
func mcodeMergeMCP(layout mcodeLayout, install bool) error {
	doc := map[string]any{}
	if raw, err := os.ReadFile(layout.MCPFile); err == nil {
		if err := json.Unmarshal(raw, &doc); err != nil {
			return fmt.Errorf("cannot parse %s: %w", layout.MCPFile, err)
		}
	}
	servers, _ := doc["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	if !install {
		delete(servers, "mcode-router")
	} else {
		command, err := mcodeMCPCommand()
		if err != nil {
			return err
		}
		servers["mcode-router"] = map[string]any{
			"command": command,
			"args":    []any{},
			"timeout": 3600000,
		}
	}
	doc["mcpServers"] = servers
	return writeJSONAtomic(layout.MCPFile, doc)
}

// mcodeMCPCommand resolves the multi-call binary and ensures an mcp symlink exists.
func mcodeMCPCommand() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("cannot resolve executable: %w", err)
	}
	dir := filepath.Dir(exe)
	link := filepath.Join(dir, "mcode-router-mcp")
	if _, err := os.Lstat(link); err != nil {
		if err := os.Symlink(filepath.Base(exe), link); err != nil {
			return "", fmt.Errorf("cannot create %s: %w", link, err)
		}
	}
	return link, nil
}

// mcodeMergeHooks registers or removes the OODA context injection hooks.
func mcodeMergeHooks(layout mcodeLayout, install bool) error {
	doc := map[string]any{"enabled": true, "hooks": map[string]any{}}
	if raw, err := os.ReadFile(layout.HooksFile); err == nil {
		if err := json.Unmarshal(raw, &doc); err != nil {
			return fmt.Errorf("cannot parse %s: %w", layout.HooksFile, err)
		}
	}
	hooks, _ := doc["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	for _, spec := range mcodeHookSpecs {
		groups, _ := hooks[spec.Event].([]any)
		groups = mcodeStripNamed(groups, spec.Name)
		if install {
			groups = append(groups, map[string]any{
				"hooks": []any{map[string]any{
					"type":    "command",
					"name":    spec.Name,
					"command": spec.Command,
					"timeout": 8,
				}},
			})
		}
		hooks[spec.Event] = groups
	}
	doc["hooks"] = hooks
	doc["enabled"] = true
	return writeJSONAtomic(layout.HooksFile, doc)
}

// mcodeStripNamed removes hook groups that already carry the given handler name.
func mcodeStripNamed(groups []any, name string) []any {
	kept := make([]any, 0, len(groups))
	for _, raw := range groups {
		group, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		handlers, _ := group["hooks"].([]any)
		matched := false
		for _, item := range handlers {
			handler, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if label, _ := handler["name"].(string); label == name {
				matched = true
			}
		}
		if !matched {
			kept = append(kept, raw)
		}
	}
	return kept
}

// copyFileAtomic copies a file through a temp file and an atomic rename.
func copyFileAtomic(src, dst string) error {
	raw, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".mcode-router-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Chmod(name, 0o644); err != nil {
		os.Remove(name)
		return err
	}
	return os.Rename(name, dst)
}
