// MCode host adapter wiring for the MiniMax Code plugin.
package install

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type mcodeLayout struct {
	DataDir   string
	PluginDir string
	SkillsDir string
	AgentsDir string
	MCPFile   string
	HooksFile string
}

// RunMCodeCmd wires or removes the MiniMax Code host adapter.
//
// Usage: mcode-router __mcode <install|uninstall|status> [--data-dir DIR] [--plugin-dir DIR]
func RunMCodeCmd(argv []string, outW, errW io.Writer) int {
	if len(argv) == 0 {
		fmt.Fprintln(errW, "error: __mcode requires install, uninstall or status")
		return 2
	}
	action := argv[0]
	flags := parseMCodeFlags(argv[1:])
	layout, err := mcodeResolve(flags)
	if err != nil {
		fmt.Fprintln(errW, err)
		return 1
	}
	switch action {
	case "install":
		return mcodeApply(layout, true, outW, errW)
	case "uninstall":
		return mcodeApply(layout, false, outW, errW)
	case "status":
		return mcodeStatus(layout, outW)
	}
	fmt.Fprintf(errW, "error: unknown __mcode action %q\n", action)
	return 2
}

// parseMCodeFlags reads the optional --data-dir and --plugin-dir flags.
func parseMCodeFlags(argv []string) map[string]string {
	flags := map[string]string{}
	for i := 0; i < len(argv)-1; i++ {
		switch argv[i] {
		case "--data-dir":
			flags["data-dir"] = argv[i+1]
		case "--plugin-dir":
			flags["plugin-dir"] = argv[i+1]
		}
	}
	return flags
}

// mcodeResolve builds the target layout from flags, environment and defaults.
func mcodeResolve(flags map[string]string) (mcodeLayout, error) {
	dataDir := flags["data-dir"]
	if dataDir == "" {
		dataDir = os.Getenv("MINIMAX_DATA_DIR")
	}
	if dataDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return mcodeLayout{}, fmt.Errorf("cannot resolve home dir: %w", err)
		}
		dataDir = filepath.Join(home, ".minimax")
	}
	pluginDir := flags["plugin-dir"]
	if pluginDir == "" {
		exe, err := os.Executable()
		if err != nil {
			return mcodeLayout{}, fmt.Errorf("cannot resolve executable: %w", err)
		}
		pluginDir = filepath.Join(filepath.Dir(filepath.Dir(exe)), "mcode-ooda")
	}
	return mcodeLayout{
		DataDir:   dataDir,
		PluginDir: pluginDir,
		SkillsDir: filepath.Join(dataDir, "skills"),
		AgentsDir: filepath.Join(dataDir, "agents"),
		MCPFile:   filepath.Join(dataDir, "mcp.json"),
		HooksFile: filepath.Join(dataDir, "hooks.json"),
	}, nil
}

// mcodeApply installs or removes the skills, agents, MCP entry and hooks.
func mcodeApply(layout mcodeLayout, install bool, outW, errW io.Writer) int {
	if err := mcodeCopySkills(layout, install); err != nil {
		fmt.Fprintln(errW, err)
		return 1
	}
	if err := mcodeCopyAgents(layout, install); err != nil {
		fmt.Fprintln(errW, err)
		return 1
	}
	if err := mcodeMergeMCP(layout, install); err != nil {
		fmt.Fprintln(errW, err)
		return 1
	}
	if err := mcodeMergeHooks(layout, install); err != nil {
		fmt.Fprintln(errW, err)
		return 1
	}
	verb := "installed"
	if !install {
		verb = "removed"
	}
	fmt.Fprintf(outW, "mcode adapter %s\n  skills: %s\n  agents: %s\n  mcp: %s\n  hooks: %s\n",
		verb, layout.SkillsDir, layout.AgentsDir, layout.MCPFile, layout.HooksFile)
	return 0
}

// mcodeStatus reports which adapter pieces are present.
func mcodeStatus(layout mcodeLayout, outW io.Writer) int {
	skills, err := filepath.Glob(filepath.Join(layout.SkillsDir, "mcode-router"))
	if err != nil || len(skills) == 0 {
		fmt.Fprintln(outW, "skills: missing")
	} else {
		fmt.Fprintln(outW, "skills: present")
	}
	agents, err := filepath.Glob(filepath.Join(layout.AgentsDir, "ooda-*"))
	if err != nil || len(agents) == 0 {
		fmt.Fprintln(outW, "agents: missing")
	} else {
		fmt.Fprintf(outW, "agents: present (%d)\n", len(agents))
	}
	if raw, rerr := os.ReadFile(layout.MCPFile); rerr == nil {
		var doc map[string]any
		if json.Unmarshal(raw, &doc) == nil {
			servers, _ := doc["mcpServers"].(map[string]any)
			if _, ok := servers["mcode-router"]; ok {
				fmt.Fprintln(outW, "mcp: present")
			} else {
				fmt.Fprintln(outW, "mcp: missing")
			}
		}
	} else {
		fmt.Fprintln(outW, "mcp: missing")
	}
	if raw, rerr := os.ReadFile(layout.HooksFile); rerr == nil {
		if strings.Contains(string(raw), "OODA fleet context") {
			fmt.Fprintln(outW, "hooks: present")
		} else {
			fmt.Fprintln(outW, "hooks: missing")
		}
	} else {
		fmt.Fprintln(outW, "hooks: missing")
	}
	return 0
}

// writeJSONAtomic writes JSON through a temp file and an atomic rename.
func writeJSONAtomic(path string, data any) error {
	encoded, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".mcode-router-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(encoded); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Chmod(name, 0o600); err != nil {
		os.Remove(name)
		return err
	}
	return os.Rename(name, path)
}
