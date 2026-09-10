# Backlog

What is open after round 7 (platform driver: tmux on Linux, WezTerm on
Windows), as of 2026-09-10. Design and verification history lives in
`docs/superpowers/specs/*`; this file is only the to-do list. Remove
items as they land.

## Verification still owed

- A junie run on the real server with the current binary: preamble
  pasted ("prompt delivered"), report lands in the channel. Pick
  "Always allow" once when junie asks about the erbrus command.
- The skill in action: a Claude Code session asked to "configure junie
  as an erbrus provider using the erbrus skill", watched end to end.
- Kimi's real *working* screen. Its API was down when the rule was
  written; only the retry spinner has ever matched.
- Antigravity's `--model` with display-style names (spaces, parentheses).
- The Tools rail with a real editor on the user's machine (`idea64.exe`
  / `subl.exe` through `{windir}`), and `{windir}` for a directory
  outside `/mnt` (the `\\wsl.localhost\<distro>\…` form). Verified
  2026-09-10 on the throwaway with a script tool and gg only.
- The Windows live check for the platform driver — this session cannot
  run Windows programs, so it must run on the user's Windows box: spawn
  claude-code through WezTerm, a chat round trip, the Tools rail's
  `shell` tool opening a WezTerm window, and `wezterm cli kill-pane`
  while a run is live marking it failed via Reconcile within 10s.

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
  dialog, collapsible rail, queued-message banner, worktree form). The
  Go suite never runs the page script: the projects-page confirmation
  dialog was missing for a day (fixed 2026-09-09) while every test
  passed. A headless-browser or DOM-shim setup would catch that class.
- `agents setup --update` could also refresh provider entries that still
  equal an older registry template (junie's one-shot command had to be
  fixed by hand with `provider set`).
- Worktree removal from the channel page (`gg worktree remove`, with the
  same shell-terminal fallback as creation).
- `erbrus agents list` could show the provider type; today only
  `provider show` prints it. The skill's provider-key table does not
  mention `type` either (agents configure agents, never tools); adding
  a row means a skill version bump, so it waits for the next skill edit.
- Per-repo `tools:` in the repo config (today tools are global only).
- Reporting a GUI tool that starts and then exits at once (a short
  post-start wait); today only a start failure is shown, by design.
- Keep the last screen after an agent exits under WezTerm: `exit_behavior
  = "Hold"` in the mux config plus a dead-pane heuristic (the pane
  otherwise closes with the program, so `erbrus wrap`'s reported exit
  code is right but the final screen is gone).
- A browser terminal on Windows (a ConPTY host inside erbrus; WezTerm's
  headless mux has no pty-attach today).
- User-facing "tmux" wording on the wezterm path: `screen.go`'s "no tmux
  window for this run", `terminal.go`'s "run has no live tmux window",
  `ui_tools.go`'s "terminal tools need tmux", and the channel.html
  tool-row title all still say tmux even when the driver is wezterm.
- `shellToolOverride` is not idempotent across driver switches (only
  matters for tests that swap drivers on one `Server` mid-run).
- The `s.driver` read in `ReloadConfig` (under `rulesMu`) racing the
  unlocked write in `SetDriver` (a data race if the two ever run
  concurrently in practice; today they don't).
- `wrap`'s `os.Setenv` of env-file keys leaks into the process — fine
  for the short-lived real `wrap` binary, but leaves state visible to
  the cli test binary if a later test reads the same env var without
  setting it itself.
- A malformed `cmd.json` or env file makes `wrap` exit 127 without
  reporting the exit to the server.
- Small test-coverage gaps: `NewDriver`'s default wezterm bin, `cli()`'s
  stderr-unwrap path, and test globals not restored after being
  zeroed for wezterm's settle/verify timing.

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
