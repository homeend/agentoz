package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"

	"erbrus/internal/client"
	"erbrus/internal/wt"
)

func runStart(args []string, stdout, stderr io.Writer) int {
	// The preset name is a required positional argument that precedes the
	// flags (`start <preset> [flags]`), but flag.Parse stops consuming at
	// the first non-flag argument — so it must be peeled off before the
	// rest of args is handed to the FlagSet.
	if len(args) < 1 || len(args[0]) > 0 && args[0][0] == '-' {
		fmt.Fprintln(stderr, "usage: erbrus start <preset> [flags]")
		return 2
	}
	presetName := args[0]

	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	fs.SetOutput(stderr)
	model := fs.String("model", "", "model override")
	prompt := fs.String("prompt", "", "prompt override")
	extraArgs := fs.String("args", "", "extra CLI args for the provider")
	name := fs.String("name", "", "agent name override")
	channel := fs.String("channel", "", "channel id or name (default: current worktree's channel)")
	fg := fs.Bool("fg", false, "run in this terminal instead of tmux")
	noCreate := fs.Bool("no-create", false, "fail instead of auto-configuring")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: erbrus start <preset> [flags]")
		return 2
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	worktreeRoot, err := wt.GitRoot(wt.ExecRunner, cwd)
	if err != nil {
		fmt.Fprintln(stderr, "not inside a git repository:", err)
		return 1
	}
	mainRoot, err := wt.MainRoot(wt.ExecRunner, cwd)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	c, _, _ := client.FromEnv()
	var project client.Project
	if *noCreate {
		projects, err := c.ListProjects()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		found := false
		for _, p := range projects {
			if p.RepoPath == mainRoot {
				project, found = p, true
			}
		}
		if !found {
			fmt.Fprintf(stderr, "project %s not registered and --no-create given\n", mainRoot)
			return 1
		}
	} else {
		project, err = c.AddProject(mainRoot)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if project.Created {
			fmt.Fprintf(stdout, "created project %s (%s)\n", project.Name, project.RepoPath)
		}
		if project.Warning != "" {
			fmt.Fprintln(stdout, "warning:", project.Warning)
		}
	}

	ch, err := pickChannel(project, worktreeRoot, *channel)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	req := map[string]any{"channel_id": ch.ID, "preset": presetName, "fg": *fg}
	for k, v := range map[string]string{"model": *model, "prompt": *prompt, "args": *extraArgs, "name": *name} {
		if v != "" {
			req[k] = v
		}
	}
	res, err := c.SpawnRun(req)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	if !*fg {
		fmt.Fprintf(stdout, "spawned %s → tmux %s (channel #%s)\n", res.AgentName, res.TmuxTarget, ch.Name)
		if sess, _, ok := cutHandle(res.TmuxTarget); ok {
			fmt.Fprintf(stdout, "attach: tmux attach -t %s\n", sess)
		}
		return 0
	}

	// fg: run the command here, wrap-style. Env comes from the server.
	if res.Run == nil || res.CmdFile == "" {
		fmt.Fprintln(stderr, "server returned an incomplete fg spawn response")
		return 1
	}
	for k, v := range res.Env {
		os.Setenv(k, v)
	}
	fmt.Fprintf(stdout, "running %s in this terminal (channel #%s)\n", res.Run.AgentName, ch.Name)
	code := runWrap([]string{res.CmdFile}, stdout, stderr)
	fmt.Fprintf(stdout, "%s finished · exit %d\n", res.Run.AgentName, code)
	return code
}

func pickChannel(p client.Project, worktreeRoot, override string) (client.Channel, error) {
	if override != "" {
		if id, err := strconv.ParseInt(override, 10, 64); err == nil {
			for _, ch := range p.Channels {
				if ch.ID == id {
					return ch, nil
				}
			}
		}
		for _, ch := range p.Channels {
			if ch.Name == override {
				return ch, nil
			}
		}
		return client.Channel{}, fmt.Errorf("channel %q not found in project %s", override, p.Name)
	}
	for _, ch := range p.Channels {
		if ch.WorktreePath == worktreeRoot {
			return ch, nil
		}
	}
	for _, ch := range p.Channels {
		if ch.Name == "general" {
			return ch, nil
		}
	}
	return client.Channel{}, fmt.Errorf("no channel for worktree %s and no general channel", worktreeRoot)
}

func cutHandle(h string) (string, string, bool) {
	for i := 0; i < len(h); i++ {
		if h[i] == ':' {
			return h[:i], h[i+1:], true
		}
	}
	return "", "", false
}
