// Command release prepares an mcode-router release: it bumps the version
// shared by the CLI and the MiniMax Code plugin manifest, and verifies that
// every release file agrees.
package main

import (
	"fmt"
	"io"
	"os"
)

// usage is the command line synopsis.
const usage = "usage: release bump <version> | release check"

// main runs the release helper.
func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run dispatches a release helper subcommand.
func run(args []string, outW, errW io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(errW, usage)
		return 2
	}
	switch args[0] {
	case "check":
		if len(args) != 1 {
			fmt.Fprintln(errW, "error: check takes no arguments")
			return 2
		}
		return runCheck(outW, errW)
	case "bump":
		return runBump(args[1:], outW, errW)
	default:
		fmt.Fprintf(errW, "error: unknown command %q\n%s\n", args[0], usage)
		return 2
	}
}
