package cli

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"erbrus/internal/client"
)

func runMsg(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: erbrus msg <send|read> [flags]")
		return 2
	}
	switch args[0] {
	case "send":
		return runMsgSend(args[1:], stdin, stdout, stderr)
	case "read":
		return runMsgRead(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown msg subcommand %q\n", args[0])
		return 2
	}
}

func runMsgSend(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("msg send", flag.ContinueOnError)
	fs.SetOutput(stderr)
	report := fs.Bool("report", false, "post as a report")
	system := fs.Bool("system", false, "post as a system note")
	file := fs.String("file", "", "attach a file as artifact")
	channel := fs.Int64("channel", 0, "channel id (overrides ERBRUS_CHANNEL)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	c, envCh, _ := client.FromEnv()
	ch := envCh
	if *channel != 0 {
		ch = *channel
	}
	if ch == 0 {
		fmt.Fprintln(stderr, "no channel: set ERBRUS_CHANNEL or pass --channel <id>")
		return 2
	}
	body := strings.Join(fs.Args(), " ")
	if body == "-" || body == "" {
		b, _ := io.ReadAll(stdin)
		body = strings.TrimSpace(string(b))
	}
	if body == "" {
		fmt.Fprintln(stderr, "empty message")
		return 2
	}
	kind := "message"
	if *report {
		kind = "report"
	}
	if *system {
		kind = "system"
	}
	var files []string
	if *file != "" {
		files = []string{*file}
	}
	m, err := c.SendMessage(ch, kind, body, files)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "sent message %d to channel %d\n", m.ID, m.ChannelID)
	return 0
}

func runMsgRead(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("msg read", flag.ContinueOnError)
	fs.SetOutput(stderr)
	since := fs.Int64("since", 0, "only messages after this id")
	limit := fs.Int("limit", 50, "max messages")
	channel := fs.Int64("channel", 0, "channel id (overrides ERBRUS_CHANNEL)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	c, envCh, _ := client.FromEnv()
	ch := envCh
	if *channel != 0 {
		ch = *channel
	}
	if ch == 0 {
		fmt.Fprintln(stderr, "no channel: set ERBRUS_CHANNEL or pass --channel <id>")
		return 2
	}
	msgs, err := c.ReadMessages(ch, *since, *limit)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	for _, m := range msgs {
		fmt.Fprintf(stdout, "[%d] %s (%s): %s\n", m.ID, m.AuthorName, m.Kind, m.Body)
		for _, a := range m.Artifacts {
			fmt.Fprintf(stdout, "  ↳ file: %s (%d bytes)\n", a.Filename, a.Size)
		}
	}
	return 0
}
