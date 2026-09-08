package cli

import (
	"fmt"
	"io"
	"strconv"

	"erbrus/internal/client"
)

// runStop: `erbrus stop <run-id>` — the counterpart of `erbrus start`
// for scripts and agents that started a run to look at its screens.
func runStop(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: erbrus stop <run-id>")
		return 2
	}
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil || id <= 0 {
		fmt.Fprintf(stderr, "stop: bad run id %q\n", args[0])
		return 2
	}
	c, _, _ := client.FromEnv()
	r, err := c.StopRun(id)
	if err != nil {
		fmt.Fprintln(stderr, "stop:", err)
		return 1
	}
	fmt.Fprintf(stdout, "stopped %s (run %d) · %s\n", r.AgentName, r.ID, r.Status)
	return 0
}
