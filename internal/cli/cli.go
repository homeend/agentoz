// Package cli dispatches erbrus subcommands. main() is a shim over Run.
package cli

import (
	"fmt"
	"io"
)

const Version = "0.1.0-dev"

const usage = `usage: erbrus <command> [flags]

commands:
  serve      run the server
  init       register the current repo as a project (auto-configure)
  msg        send/read channel messages (msg send | msg read)
  version    print version
`

// Run executes one subcommand and returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "version":
		fmt.Fprintf(stdout, "erbrus %s\n", Version)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\n%s", args[0], usage)
		return 2
	}
}
