package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"

	"erbrus/internal/client"
)

const screenUsage = `usage: erbrus screen capture --run N [--lines M]
       erbrus screen test --provider P --run N [--working RE]... [--waiting RE]... [--question RE]...
`

// runScreen lets an agent read a run's terminal (its own, usually) and
// try screen rules against it before saving them.
func runScreen(args []string, stdout, stderr io.Writer) int {
	if len(args) < 1 {
		fmt.Fprint(stderr, screenUsage)
		return 2
	}
	c, _, _ := client.FromEnv()
	switch args[0] {
	case "capture":
		fs := flag.NewFlagSet("screen capture", flag.ContinueOnError)
		fs.SetOutput(stderr)
		run := fs.Int64("run", 0, "run id (default: ERBRUS_RUN_ID)")
		lines := fs.Int("lines", 15, "how many tail lines (max 200)")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		id := runIDOr(*run)
		if id == 0 {
			fmt.Fprintln(stderr, "screen capture: --run required (or ERBRUS_RUN_ID)")
			return 2
		}
		sc, err := c.RunScreen(id, *lines)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		state := sc.State
		if state == "" {
			state = "unknown"
		}
		fmt.Fprintf(stdout, "state: %s\n", state)
		if sc.Dead {
			fmt.Fprintln(stdout, "(process exited; final screen)")
		}
		for _, o := range sc.Options {
			fmt.Fprintf(stdout, "option %s: %s\n", o.Key, o.Label)
		}
		fmt.Fprintf(stdout, "--- %dx%d, last %d lines ---\n", sc.Cols, sc.Rows, len(sc.Lines))
		for _, l := range sc.Lines {
			fmt.Fprintln(stdout, l)
		}
		return 0
	case "test":
		fs := flag.NewFlagSet("screen test", flag.ContinueOnError)
		fs.SetOutput(stderr)
		provider := fs.String("provider", "", "provider name (need not exist yet)")
		run := fs.Int64("run", 0, "run id (default: ERBRUS_RUN_ID)")
		var working, waiting, question multiFlag
		fs.Var(&working, "working", "pattern (repeatable)")
		fs.Var(&waiting, "waiting", "pattern (repeatable)")
		fs.Var(&question, "question", "pattern (repeatable)")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		id := runIDOr(*run)
		if *provider == "" || id == 0 {
			fmt.Fprint(stderr, screenUsage)
			return 2
		}
		res, err := c.ScreenTest(*provider, id, working, waiting, question)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		state := res.State
		if state == "" {
			state = "unknown"
		}
		fmt.Fprintf(stdout, "state: %s\n", state)
		for _, l := range res.Lines {
			tag := ""
			if l.Match != "" {
				tag = "[" + l.Match + "] "
			}
			fmt.Fprintf(stdout, "%s%s\n", tag, l.Text)
		}
		return 0
	}
	fmt.Fprint(stderr, screenUsage)
	return 2
}

// runIDOr returns the flag when set, else ERBRUS_RUN_ID (an agent
// inspecting its own screen).
func runIDOr(flagVal int64) int64 {
	if flagVal != 0 {
		return flagVal
	}
	id, _ := strconv.ParseInt(os.Getenv("ERBRUS_RUN_ID"), 10, 64)
	return id
}
