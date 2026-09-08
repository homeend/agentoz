package cli

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"erbrus/internal/client"
)

// multiFlag collects a repeatable string flag.
type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ", ") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

const providerUsage = `usage: erbrus provider set <name> [--command S] [--default-model S] [--prompt arg|paste]
                           [--working RE]... [--waiting RE]... [--question RE]... [--clear-rules]
       erbrus provider show <name>
`

// runProvider edits providers through the running server, which rewrites
// config.yaml and applies the change live (the settings page does the
// same). This is what the erbrus skill tells agents to use.
func runProvider(args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 {
		fmt.Fprint(stderr, providerUsage)
		return 2
	}
	sub, name := args[0], args[1]
	c, _, _ := client.FromEnv()
	switch sub {
	case "show":
		p, err := c.GetProvider(name)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		printProvider(stdout, p)
		return 0
	case "set":
		fs := flag.NewFlagSet("provider set", flag.ContinueOnError)
		fs.SetOutput(stderr)
		command := fs.String("command", "", "command template with {model} {args} {prompt}")
		model := fs.String("default-model", "", "default model (empty: the CLI's own default)")
		prompt := fs.String("prompt", "", "arg|paste")
		var working, waiting, question multiFlag
		fs.Var(&working, "working", "screen_working pattern (repeatable)")
		fs.Var(&waiting, "waiting", "screen_waiting pattern (repeatable)")
		fs.Var(&question, "question", "screen_question pattern (repeatable)")
		clear := fs.Bool("clear-rules", false, "remove all three lists (back to built-in rules)")
		if err := fs.Parse(args[2:]); err != nil {
			return 2
		}
		patch := map[string]any{}
		visited := map[string]bool{}
		fs.Visit(func(f *flag.Flag) { visited[f.Name] = true })
		if visited["command"] {
			patch["command"] = *command
		}
		if visited["default-model"] {
			patch["default_model"] = *model
		}
		if visited["prompt"] {
			patch["prompt"] = *prompt
		}
		if *clear {
			patch["screen_working"], patch["screen_waiting"], patch["screen_question"] = []string{}, []string{}, []string{}
		} else {
			if visited["working"] {
				patch["screen_working"] = []string(working)
			}
			if visited["waiting"] {
				patch["screen_waiting"] = []string(waiting)
			}
			if visited["question"] {
				patch["screen_question"] = []string(question)
			}
		}
		if len(patch) == 0 {
			fmt.Fprintln(stderr, "nothing to set")
			return 2
		}
		p, err := c.PutProvider(name, patch)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "saved provider %s (applied live)\n", name)
		printProvider(stdout, p)
		return 0
	}
	fmt.Fprint(stderr, providerUsage)
	return 2
}

func printProvider(w io.Writer, p client.Provider) {
	fmt.Fprintf(w, "command: %s\n", p.Command)
	if p.DefaultModel != "" {
		fmt.Fprintf(w, "default_model: %s\n", p.DefaultModel)
	}
	prompt := p.Prompt
	if prompt == "" {
		prompt = "arg"
		if !strings.Contains(p.Command, "{prompt}") {
			prompt = "paste (no {prompt} in command)"
		}
	}
	fmt.Fprintf(w, "prompt: %s\n", prompt)
	fmt.Fprintf(w, "screen rules: %s\n", p.RulesSource)
	for _, kv := range []struct {
		k string
		v []string
	}{{"working", p.ScreenWorking}, {"waiting", p.ScreenWaiting}, {"question", p.ScreenQuestion}} {
		for _, re := range kv.v {
			fmt.Fprintf(w, "  %s: %s\n", kv.k, re)
		}
	}
}
