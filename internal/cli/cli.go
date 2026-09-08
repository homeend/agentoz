// Package cli dispatches erbrus subcommands. main() is a shim over Run.
package cli

import (
	"fmt"
	"io"
	"os"
)

const Version = "0.1.0-dev"

const usage = `usage: erbrus <command> [flags]

commands:
  serve      run the server
  init       register the current repo as a project (auto-configure)
  start      spawn an agent run from a preset or provider name (start <name> [--prompt S])
  stop       stop a run by id
  msg        send/read channel messages (msg send | msg read)
  agents     detect installed agent CLIs; setup installs the erbrus skill (agents list | agents setup)
  provider   show/set a provider in config.yaml through the server (provider show | provider set)
  screen     read or classify a run's terminal (screen capture | screen test)
  launcher   install a script on your PATH that runs this binary as "erbrus"
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
	case "serve":
		return runServe(args[1:], stdout, stderr)
	case "init":
		return runInit(args[1:], stdout, stderr)
	case "start":
		return runStart(args[1:], stdout, stderr)
	case "stop":
		return runStop(args[1:], stdout, stderr)
	case "msg":
		return runMsg(args[1:], os.Stdin, stdout, stderr)
	case "wrap":
		return runWrap(args[1:], stdout, stderr)
	case "agents":
		return runAgents(args[1:], os.Stdin, stdout, stderr)
	case "provider":
		return runProvider(args[1:], stdout, stderr)
	case "screen":
		return runScreen(args[1:], stdout, stderr)
	case "launcher":
		return runLauncher(args[1:], os.Stdin, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n%s", args[0], usage)
		return 2
	}
}
