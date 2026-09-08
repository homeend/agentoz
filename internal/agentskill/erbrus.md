# erbrus

erbrus is a local server that runs AI coding agents in tmux windows and
gives each one a chat channel. Two situations bring you here.

## 1. You are running as an erbrus agent

erbrus started you with a preamble naming your agent and channel. Your
environment has `ERBRUS_URL`, `ERBRUS_TOKEN`, `ERBRUS_CHANNEL` and
`ERBRUS_RUN_ID`; the `erbrus` command in the preamble is an absolute path
and already knows them.

- Do not start any work on your own until you are explicitly asked to do
  something. Humans and other agents post tasks into your channel.
- Report progress and results to the channel. Flags go before the text,
  and the text goes in SINGLE quotes:
  ```
  erbrus msg send --report --md 'what you did'
  ```
- Text with quotes or backticks goes on stdin instead:
  ```
  erbrus msg send --report --md - <<'EOF'
  what you did, including "quotes" and `code`, untouched
  EOF
  ```
- Attach a produced document with `--file /abs/path/to/report.md`.
- A message forwarded to you from another channel says so in its header
  and ends with the exact reply command; reply to that ORIGIN channel
  with `--channel <id>`, not to your own.
- Post a report as your FINAL step — the next agent picks up from it.
- Chat messages typed into your terminal come from the erbrus composer;
  the sender only sees the channel, so answer with `erbrus msg send`,
  not only on screen.

## 2. You are configuring a CLI as an erbrus provider

A provider is an entry under `providers:` in erbrus's global
`config.yaml` (`~/.config/erbrus/config.yaml`). Keys:

| key | meaning |
|---|---|
| `command` | shell template. Placeholders: `{model}`, `{args}`, `{prompt}`. erbrus shell-quotes `{model}` and `{prompt}`; an empty placeholder is dropped together with the flag before it (`--model {model}` vanishes when no model is set). |
| `default_model` | used when a run names no model; leave unset to let the CLI use its own default |
| `prompt` | `arg` (default when the command contains `{prompt}`) or `paste`: erbrus starts the CLI without the prompt and pastes it into the terminal once the input box is visible. Needed for CLIs whose interactive mode takes no initial prompt (Kimi Code). |
| `screen_working`, `screen_waiting`, `screen_question` | RE2 regex lists that read the CLI's screen; see below |

### How screen classification works

Every few seconds erbrus captures the tmux window, strips colors, trims
each line, drops empty lines, keeps the last 15 lines and joins them with
newlines. Each pattern is compiled with `(?m)` and may span lines with
`\n`. Order: if any *working* pattern matches → working; else any
*waiting* pattern → waiting (idle, turn finished); else any *question*
pattern → question (a dialog needs a human); else unknown, which changes
nothing. Any non-empty list replaces ALL built-in rules for that provider,
so give all three when you override.

Good rules are structural, not textual: the spinner line with its timer
for working; "the prompt glyph directly under a rule line" for waiting;
the dialog chrome (`Esc to cancel`, `❯ 1.`) for question. Scrollback text
must not be able to fake a state.

### The workflow

1. Read the CLI's `--help`. Find: the flag for the model, the flag or
   positional argument for an initial prompt in interactive mode (if there
   is none, use `prompt: paste`), and the auto-approve flag erbrus agents
   need (`--yolo`, `--dangerously-skip-permissions`, …).
2. Save a first version:
   ```
   erbrus provider set <name> --command '<cli> --model {model} {args} "{prompt}"' --prompt arg
   ```
   `erbrus provider show <name>` prints what is saved and whether the
   rules are built-in.
3. Get a running instance of the CLI under erbrus: spawn it from the web
   UI, or, if you ARE that CLI, use your own run id from `ERBRUS_RUN_ID`.
4. Look at the screens:
   ```
   erbrus screen capture --run <id> --lines 40
   ```
   Capture it idle at its input box, while it is busy (if you are the
   agent, run the capture from a tool call while your turn is in
   progress — the spinner is on screen then), and on a dialog if you can
   provoke one safely (a trust prompt, a permission question).
5. Write the three lists and test them without saving:
   ```
   erbrus screen test --provider <name> --run <id> --working '<re>' --waiting '<re>' --question '<re>'
   ```
   It prints the resulting state and marks which line matched which kind.
   Flags repeat for more patterns.
6. Save: `erbrus provider set <name> --working '<re>' --waiting '<re>' --question '<re>'`
   (repeat flags; `--clear-rules` returns to built-ins). The running
   erbrus applies the change immediately.
7. Report what you configured and what you could not verify.

### Worked examples (built in)

claude-code — working `\S+… \(\d+`, `⎿\s+Running…`; waiting `^─{8,}\n❯`
(input box under a rule); question `^❯ \d+\.`, `Esc to cancel`,
`Esc to go back`, `\(y/n\)`, `\[Y/n\]`, `Do you want to proceed`.

codex — working `Working \(\d+`, `(?i)esc to interrupt`; waiting
`^›[^\n]*\n[^\n]*· /` (input line above the model/cwd status line);
question `\(y/n\)`, `\[Y/n\]`, `Press Enter`, `^\s*[›>] \d+\.`.

kimi (registry default, working rule unverified) — command
`kimi --yolo --model {model} {args}`, `prompt: paste`; working
`^[🌑🌒🌓🌔🌕🌖🌗🌘] `, `Retrying \(\d+/\d+\)`; waiting `^│ >[^\n]*\n╰`;
question `Trust this folder\?`, `Enter select`, `Esc exit`.

antigravity — `agy --dangerously-skip-permissions --model "{model}" {args} -i "{prompt}"`.
