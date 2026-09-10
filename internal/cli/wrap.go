package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"erbrus/internal/client"
	"erbrus/internal/spawn"
)

// runWrap executes a run's command file (cmd.sh or cmd.json) and reports the
// exit status back to the server. It is spawned inside the tmux window (or
// by `start --fg`) and is deliberately absent from the usage text.
func runWrap(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: erbrus wrap <cmdfile>")
		return 2
	}
	cmdFile := args[0]
	fileEnv, err := spawn.ReadEnvFile(cmdFile)
	if err != nil {
		fmt.Fprintln(stderr, "wrap:", err)
		return 127
	}
	env := append(os.Environ(), fileEnv...) // later entries win in exec
	for _, kv := range fileEnv {
		if k, v, ok := strings.Cut(kv, "="); ok {
			os.Setenv(k, v) // so ERBRUS_RUN_ID / client.FromEnv see the file too
		}
	}

	var cmd *exec.Cmd
	if strings.HasSuffix(cmdFile, ".json") {
		b, err := os.ReadFile(cmdFile)
		var argv []string
		if err == nil {
			err = json.Unmarshal(b, &argv)
		}
		if err != nil || len(argv) == 0 {
			fmt.Fprintln(stderr, "wrap: bad argv file:", err)
			return 127
		}
		cmd = exec.Command(argv[0], argv[1:]...)
	} else {
		cmd = exec.Command("sh", cmdFile)
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = env

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
