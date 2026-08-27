// Package integrate wires spawned agents to erbrus: an instruction preamble
// every provider gets, and per-provider hooks where the CLI supports them.
package integrate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"erbrus/internal/provider"
)

func Preamble(erbrusBin, agentName, channelName string) string {
	return fmt.Sprintf(`You are agent %q working in the erbrus channel #%s.
Report progress and results to the channel with this exact command (flags go before the message text):
  %s msg send --report "what you did"
Attach a produced document with --file:
  %s msg send --report --file /abs/path/to/report.md "summary"
Read the channel history with:
  %s msg read
Post a report as your FINAL step — the next agent picks up from it.`,
		agentName, channelName, erbrusBin, erbrusBin, erbrusBin)
}

func AssemblePrompt(preamble, prompt, context string) string {
	parts := []string{}
	for _, p := range []string{preamble, prompt, context} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, "\n\n")
}

// ChatReplySuffix is appended to every composer message delivered into a
// running agent's terminal. The agent cannot tell chat input from typed
// input, and the spawn-time preamble is out of focus by the end of a long
// task — observed live: an agent answered in-terminal only and the channel
// got nothing. This puts the routing right next to the question.
func ChatReplySuffix(erbrusBin string) string {
	return fmt.Sprintf("\n\n(sent from the erbrus chat — the sender sees only the channel, not this terminal; post your answer with (flags before the text): %s msg send --report \"your answer\")",
		erbrusBin)
}

// ForwardToAgent builds the text typed into a RUNNING agent's terminal when
// a message is forwarded to it: where it came from (project, channel, git
// coordinates), the body and artifacts, and how to reply — the origin
// channel, reachable with the agent's own token via --channel.
func ForwardToAgent(erbrusBin string, originChannelID int64, project, channel, branch, worktree, author, createdAt, body string, artifactPaths []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "--- forwarded from %s / #%s (branch %s, worktree %s) by %s at %s ---\n",
		project, channel, branch, worktree, author, createdAt)
	b.WriteString(body)
	for _, p := range artifactPaths {
		fmt.Fprintf(&b, "\nattached file (read it yourself): %s", p)
	}
	fmt.Fprintf(&b, "\nWhen done, reply to the ORIGIN channel (flags before the text):\n  %s msg send --report --channel %d \"your reply\"",
		erbrusBin, originChannelID)
	return b.String()
}

func HandoffContext(project, channel, author, createdAt, body string, artifactPaths []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "--- context: report from %s / #%s by %s at %s ---\n", project, channel, author, createdAt)
	b.WriteString(body)
	for _, p := range artifactPaths {
		fmt.Fprintf(&b, "\nattached file (read it yourself): %s", p)
	}
	return b.String()
}

func ProviderHook(provider, runDir, erbrusBin string) (string, error) {
	switch provider {
	case "claude-code":
		return claudeHook(runDir, erbrusBin)
	case "codex":
		// Verified against the installed codex binary at implementation
		// time; see the task note. Falls through to no-hook when the
		// notify syntax could not be confirmed.
		return codexHook(runDir, erbrusBin)
	default:
		return "", nil
	}
}

func claudeHook(runDir, erbrusBin string) (string, error) {
	settings := map[string]any{
		"hooks": map[string]any{
			"Stop": []any{map[string]any{
				"hooks": []any{map[string]any{
					"type":    "command",
					"command": fmt.Sprintf(`%s msg send --system "claude stop hook: prompt finished"`, provider.ShellQuote(erbrusBin)),
				}},
			}},
		},
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(runDir, "claude-settings.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return "--settings " + path, nil
}

func codexHook(runDir, erbrusBin string) (string, error) {
	// Write notify.sh script that codex will invoke when a turn completes.
	notifyScript := filepath.Join(runDir, "notify.sh")
	scriptContent := fmt.Sprintf(`#!/bin/sh
exec %s msg send --system "codex notify: turn complete"
`, provider.ShellQuote(erbrusBin))

	if err := os.WriteFile(notifyScript, []byte(scriptContent), 0o755); err != nil {
		return "", err
	}

	// Return the codex config override args to register the notify script.
	return fmt.Sprintf(`-c notify=[%s]`, provider.ShellQuote(notifyScript)), nil
}
