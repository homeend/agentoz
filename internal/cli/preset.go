package cli

import (
	"flag"
	"fmt"
	"io"

	"erbrus/internal/client"
)

const presetUsage = `usage: erbrus preset set <name> --provider P [--model S] [--prompt S] [--args S] [--agent-name S]
       erbrus preset show <name>
`

// runPreset edits presets through the running server (file + live). A
// preset is the short name humans and `erbrus start` use for a provider
// with a chosen model.
func runPreset(args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 {
		fmt.Fprint(stderr, presetUsage)
		return 2
	}
	sub, name := args[0], args[1]
	c, _, _ := client.FromEnv()
	switch sub {
	case "show":
		p, err := c.GetPreset(name)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		printPreset(stdout, p)
		return 0
	case "set":
		fs := flag.NewFlagSet("preset set", flag.ContinueOnError)
		fs.SetOutput(stderr)
		provider := fs.String("provider", "", "provider name (required for a new preset)")
		model := fs.String("model", "", "model (empty: the provider's default_model)")
		prompt := fs.String("prompt", "", "default prompt")
		extra := fs.String("args", "", "extra CLI args")
		agent := fs.String("agent-name", "", "agent name shown in the channel (default: the preset name)")
		if err := fs.Parse(args[2:]); err != nil {
			return 2
		}
		patch := map[string]any{}
		visited := map[string]bool{}
		fs.Visit(func(f *flag.Flag) { visited[f.Name] = true })
		for flagName, key := range map[string]string{"provider": "provider", "model": "model", "prompt": "prompt", "args": "args", "agent-name": "agent_name"} {
			if visited[flagName] {
				patch[key] = map[string]string{"provider": *provider, "model": *model, "prompt": *prompt, "args": *extra, "agent-name": *agent}[flagName]
			}
		}
		p, err := c.PutPreset(name, patch)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintln(stdout, "saved and applied:")
		printPreset(stdout, p)
		return 0
	default:
		fmt.Fprint(stderr, presetUsage)
		return 2
	}
}

func printPreset(w io.Writer, p client.Preset) {
	fmt.Fprintf(w, "preset %s\n  provider: %s\n", p.Name, p.Provider)
	for _, kv := range [][2]string{{"model", p.Model}, {"prompt", p.Prompt}, {"args", p.Args}, {"agent_name", p.Agent}} {
		if kv[1] != "" {
			fmt.Fprintf(w, "  %s: %s\n", kv[0], kv[1])
		}
	}
}
