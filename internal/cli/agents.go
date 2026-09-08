package cli

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"erbrus/internal/agents"
	"erbrus/internal/agentskill"
	"erbrus/internal/client"
	"erbrus/internal/config"
)

// Injectable for tests: never probe the developer's real home there.
var (
	agentsHomeDir  = os.UserHomeDir
	agentsLookPath = exec.LookPath
)

const agentsUsage = `usage: erbrus agents list
       erbrus agents setup [--all | --update | --agents id,id]
`

// runAgents detects the agent CLIs on this machine and, for setup,
// installs the erbrus skill into each chosen one and seeds a provider
// entry when config.yaml has none (same selection UX as gg init).
func runAgents(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) < 1 {
		fmt.Fprint(stderr, agentsUsage)
		return 2
	}
	cfgPath := configPath()
	cfg, cfgErr := config.LoadGlobal(cfgPath)
	home, _ := agentsHomeDir()
	dets := agents.Detect(home, agentsLookPath, cfg)
	switch args[0] {
	case "list":
		if len(dets) == 0 {
			fmt.Fprintln(stdout, "no supported agents detected")
			return 0
		}
		printAgents(stdout, dets)
		return 0
	case "setup":
	default:
		fmt.Fprint(stderr, agentsUsage)
		return 2
	}
	fs := flag.NewFlagSet("agents setup", flag.ContinueOnError)
	fs.SetOutput(stderr)
	all := fs.Bool("all", false, "set up every detected agent")
	update := fs.Bool("update", false, "refresh agents that already have the skill")
	ids := fs.String("agents", "", "comma-separated agent ids to set up")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if len(dets) == 0 {
		fmt.Fprintln(stdout, "no supported agents detected")
		return 0
	}
	var chosen []agents.Detection
	switch {
	case *all:
		chosen = dets
	case *update:
		for _, d := range dets {
			if d.Status.Checked() {
				chosen = append(chosen, d)
			}
		}
	case *ids != "":
		for _, id := range strings.Split(*ids, ",") {
			id = strings.TrimSpace(id)
			found := false
			for _, d := range dets {
				if d.Agent.ID == id {
					chosen = append(chosen, d)
					found = true
				}
			}
			if !found {
				fmt.Fprintf(stderr, "agents setup: unknown or undetected agent %q\n", id)
				return 2
			}
		}
	default:
		printAgents(stdout, dets)
		fmt.Fprint(stderr, "Apply? [enter]=checked / a=all / numbers (e.g. 1,3) / [q]uit: ")
		line, err := bufio.NewReader(stdin).ReadString('\n')
		if err != nil && line == "" {
			fmt.Fprintln(stderr, "agents setup: no selection (non-interactive?); use --all, --update, or --agents")
			return 2
		}
		checked := make([]bool, len(dets))
		for i, d := range dets {
			checked[i] = d.Status.Checked()
		}
		idx, err := parseSelection(strings.TrimSpace(line), len(dets), checked)
		if err != nil {
			fmt.Fprintln(stderr, "agents setup:", err)
			return 2
		}
		for _, i := range idx {
			chosen = append(chosen, dets[i])
		}
	}
	if len(chosen) == 0 {
		fmt.Fprintln(stdout, "nothing selected")
		return 0
	}

	failed := false
	c, _, _ := client.FromEnv()
	serverUp := pingServer(c)
	for _, d := range chosen {
		if err := agents.Install(d); err != nil {
			fmt.Fprintf(stderr, "agents setup: %s: %v\n", d.Agent.Label, err)
			failed = true
			continue
		}
		verb := "installed"
		if d.Status != agents.StatusNew {
			verb = "refreshed"
		}
		fmt.Fprintf(stdout, "✓ %s %s skill v%d → %s\n", verb, d.Agent.Label, agentskill.Version, d.SkillPath)
		if d.Configured {
			continue
		}
		if cfgErr != nil {
			fmt.Fprintf(stderr, "agents setup: cannot add provider %s: %v\n", d.Agent.ID, cfgErr)
			failed = true
			continue
		}
		if err := addProvider(c, serverUp, cfgPath, d.Agent); err != nil {
			fmt.Fprintf(stderr, "agents setup: provider %s: %v\n", d.Agent.ID, err)
			failed = true
			continue
		}
		note := ""
		if d.Agent.Note != "" {
			note = " (" + d.Agent.Note + ")"
		}
		if serverUp {
			fmt.Fprintf(stdout, "✓ provider %s added%s and applied to the running erbrus\n", d.Agent.ID, note)
		} else {
			fmt.Fprintf(stdout, "✓ provider %s added to %s%s — restart erbrus serve to pick it up\n", d.Agent.ID, cfgPath, note)
		}
	}
	fmt.Fprintln(stdout, "\nNext: let an agent refine its own rules — spawn it with the prompt")
	fmt.Fprintln(stdout, `  "configure yourself as an erbrus provider using the erbrus skill"`)
	fmt.Fprintln(stdout, "or check the screen rules under Settings → Screen rules.")
	if failed {
		return 1
	}
	return 0
}

func pingServer(c *client.Client) bool {
	if c == nil {
		return false
	}
	_, err := c.ListProjects()
	return err == nil
}

// addProvider prefers the running server (writes the file AND applies
// live); without one it edits config.yaml directly.
func addProvider(c *client.Client, serverUp bool, cfgPath string, a agents.Agent) error {
	p := a.Provider
	if serverUp {
		_, err := c.PutProvider(a.ID, map[string]any{
			"command": p.Command, "default_model": p.DefaultModel, "prompt": p.Prompt,
			"screen_working": nz(p.ScreenWorking), "screen_waiting": nz(p.ScreenWaiting), "screen_question": nz(p.ScreenQuestion),
		})
		return err
	}
	doc, err := os.ReadFile(cfgPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	out, err := config.SetProvider(doc, a.ID, config.ProviderPatch{
		Command: config.Str(p.Command), DefaultModel: config.Str(p.DefaultModel), Prompt: config.Str(p.Prompt),
		Working: config.List(p.ScreenWorking), Waiting: config.List(p.ScreenWaiting), Question: config.List(p.ScreenQuestion),
	})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(cfgPath, out, 0o644)
}

func nz(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}

func printAgents(w io.Writer, dets []agents.Detection) {
	fmt.Fprintln(w, "Detected agents:")
	for i, d := range dets {
		box := "[ ]"
		if d.Status.Checked() {
			box = "[x]"
		}
		prov := "provider: configured"
		if !d.Configured {
			prov = "provider: will be added"
			if d.Agent.Note != "" {
				prov += " (" + d.Agent.Note + ")"
			}
		}
		fmt.Fprintf(w, "  %d. %s %-12s %-45s %-11s %s\n", i+1, box, d.Agent.Label, d.SkillPath, d.Status, prov)
	}
}

// parseSelection: "" → checked ones, "a" → all, "q" → none, "1,3" → those.
func parseSelection(in string, n int, checked []bool) ([]int, error) {
	switch strings.ToLower(in) {
	case "":
		var out []int
		for i := 0; i < n; i++ {
			if checked[i] {
				out = append(out, i)
			}
		}
		return out, nil
	case "a":
		out := make([]int, n)
		for i := range out {
			out[i] = i
		}
		return out, nil
	case "q":
		return nil, nil
	}
	var out []int
	for _, tok := range strings.Split(in, ",") {
		v, err := strconv.Atoi(strings.TrimSpace(tok))
		if err != nil || v < 1 || v > n {
			return nil, fmt.Errorf("invalid selection %q", tok)
		}
		out = append(out, v-1)
	}
	return out, nil
}
