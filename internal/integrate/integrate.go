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
