package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"

	"erbrus/internal/client"
)

// runWrap executes a run's cmd.sh and reports the exit status back to the
// server. It is spawned inside the tmux window (or by `start --fg`) and is
// deliberately absent from the usage text.
func runWrap(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: erbrus wrap <cmdfile>")
		return 2
	}
	cmd := exec.Command("sh", args[0])
	cmd.Stdin = os.Stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = os.Environ()

	code := 0
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		} else {
			fmt.Fprintln(stderr, "wrap:", err)
			code = 127
		}
	}

	runID, _ := strconv.ParseInt(os.Getenv("ERBRUS_RUN_ID"), 10, 64)
	c, _, _ := client.FromEnv()
	if runID != 0 {
		if err := c.ReportExit(runID, code); err != nil {
			fmt.Fprintln(stderr, "wrap: exit report failed:", err)
		}
	}
	return code
}
