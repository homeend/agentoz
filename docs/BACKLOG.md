# Backlog

What is open after round 4 (agents setup), as of 2026-09-09. Design and
verification history lives in `docs/superpowers/specs/*`; this file is
only the to-do list. Remove items as they land.

## Verification still owed

- A junie run on the real server with the current binary: preamble
  pasted ("prompt delivered"), report lands in the channel. Pick
  "Always allow" once when junie asks about the erbrus command.
- The skill in action: a Claude Code session asked to "configure junie
  as an erbrus provider using the erbrus skill", watched end to end.
- Kimi's real *working* screen. Its API was down when the rule was
  written; only the retry spinner has ever matched.
- Antigravity's `--model` with display-style names (spaces, parentheses).
- The sidebar "+" (worktree creation) on the user's real server with the
  real gg: both modes, a failure, the shell terminal. Verified 2026-09-10
  on the throwaway instance only.
- `gg_bin` on a machine where `gg` is only a shell function: the server
  needs the binary itself on PATH (the function wraps `command gg`).

## Ideas agreed but not built

- Built-in agents as implicit providers: if config has no entry for a
  registry id and the binary is on PATH, the server uses the registry
  definition. A fresh machine could spawn any of the five with no
  config. `agents setup` would then only install the skill.
- Pre-seed junie's "Always allow ("erbrus msg send *")" the way codex
  (execpolicy rules) and Claude Code (settings allow) get theirs, so it
  never asks. Junie persists it in `~/.junie/allowlist.json` under
  `rules.executables` as `{"prefix": "erbrus msg send", "action": "allow"}`
  (seen 2026-09-09; `defaultBehavior: ask` governs everything else, brave
  mode does not cover shell commands).
- Tests for the browser JavaScript (terminal, keypad, confirmation
  dialog, collapsible rail). The missing confirmation on the projects
  page was a JS-only bug the Go suite cannot see.
- `agents setup --update` could also refresh provider entries that still
  equal an older registry template (junie's one-shot command had to be
  fixed by hand with `provider set`).
- Worktree removal from the channel page (`gg worktree remove`, with the
  same shell-terminal fallback as creation).
- `erbrus agents list` could show the provider type; today only
  `provider show` prints it. The skill's provider-key table does not
  mention `type` either (agents configure agents, never tools); adding
  a row means a skill version bump, so it waits for the next skill edit.
- A "+" for the shell alone (open a terminal in any channel's directory
  without going through a failed worktree creation).

## Parked earlier

- Auto-pipelines; tickets; Zellij spawner; MCP adapter; multi-hop
  forward provenance; strict YAML validation for settings; terminal
  scrollback in the browser; README.

## Known behaviour, not bugs

- Antigravity can sit minutes on "Generating…" with no output; erbrus
  shows it as working, then stalled after two minutes of silence.
- Junie answers in its own screen when a prompt says "reply with … and
  nothing else"; it obeys the prompt over the preamble.
- Run ids are never reused (sequence table); a database created before
  this may show a gap after the first restart.
