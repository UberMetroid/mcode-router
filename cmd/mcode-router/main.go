// Binary mcode-router is the multi-call OODA router, MCP server, and setup entrypoint.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/UberMetroid/mcode-router/internal/cli"
	"github.com/UberMetroid/mcode-router/internal/fleet"
	"github.com/UberMetroid/mcode-router/internal/install"
	"github.com/UberMetroid/mcode-router/internal/ui"
)

func dispatchSubcommand(prog string, args []string) int {
	switch prog {
	case "mcode-router-mcp", "ooda-mcp":
		if err := fleet.EnsureStateRoots(); err != nil {
			fmt.Fprintln(os.Stderr, "error: "+err.Error())
			return 2
		}
		return mcpServe(os.Stdin, os.Stdout)
	}
	if len(args) > 0 {
		switch args[0] {
		case "--statusline":
			return ui.StatusLine(os.Stdin, os.Stdout)
		case "__mcode":
			return install.RunMCodeCmd(args[1:], os.Stdout, os.Stderr)
		case "__setup":
			return install.RunSetupCmd(args[1:], os.Stderr)
		}
	}
	return cli.RunCLI(prog, args, os.Stdout, os.Stderr)
}

func main() {
	os.Exit(dispatchSubcommand(filepath.Base(os.Args[0]), os.Args[1:]))
}
