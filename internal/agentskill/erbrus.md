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

### Ground rules

- Everything happens through the `erbrus` command talking to the running
  erbrus server. There is nothing to learn from erbrus's binary, source
  tree or config file: do not read, grep or `strings` them, and do not
  edit `config.yaml` by hand.
- If a command answers "server unreachable", STOP and tell the human to
  start `erbrus serve`; nothing below works without it. If it answers
  with a 404 or an unknown route, the server is an older build: ask the
  human to restart it with the current binary.
- The provider name is the CLI's id from `erbrus agents list`
  (`claude-code`, `codex`, `kimi`, `junie`, `antigravity`, …).
  `erbrus provider show <name>` prints the entry. If it does not exist
  and the CLI is one of those built in, `erbrus agents setup
  --agents <id>` creates it (plus its preset) with the built-in template
  and screen rules; for any other CLI, write both yourself (step 2
  below). Then refine the screen rules (steps 3–6).
- Done means: provider entry, preset, and all three screen-rule lists
  saved and tested. Report anything you could not verify.
- Screens come only from a run that erbrus itself started. The one way
  to get such a run is `erbrus start <name> --prompt '…'` (step 3), which
  prints the run id. Never run the CLI or tmux yourself, never list or
  attach tmux sessions, never guess run ids. If `ERBRUS_RUN_ID` is set
  you already are a run and can capture yourself with that id.

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

Working has several faces and the list must cover ALL of them: thinking,
sending the prompt, streaming an answer, and running a tool or shell
command. They often differ — junie's "⠼ Thinking... esc to stop" becomes
"⠼ Running git status" with no suffix while a command runs. Match what
every face shares (the spinner glyph at the start of a line), not a
phrase that only one of them carries. This matters doubly when the CLI
keeps its input box on screen while busy: the waiting rule then matches
the whole time, and only an exhaustive working list stops the agent
from showing idle mid-task.

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
   Then make sure a preset exists — the short name the spawn form and
   `erbrus start` offer, normally the CLI's binary name (`agy` for
   antigravity, `claude` for claude-code):
   ```
   erbrus preset show <binary>
   erbrus preset set <binary> --provider <name>
   ```
   Add `--model <id>` only if the CLI's own default is wrong for erbrus.
3. Start the CLI as an erbrus run, from inside any git repository (erbrus
   registers it as a project when needed):
   ```
   erbrus start <name> --prompt 'Run the shell command `git status`, then count from one to forty in words, one word per line, then stop.'
   ```
   The command makes the CLI show its "running a tool" screen; the
   counting keeps it busy long enough to capture the plain busy screen.
   The output says `spawned <name> (run <id>)`. If you ARE already an
   erbrus run (`ERBRUS_RUN_ID` set), use that id instead of starting one.
4. Look at the screens, several times over the next minute:
   ```
   erbrus screen capture --run <id> --lines 40
   ```
   Right after the start the CLI may show a trust or permission dialog
   (a *question* screen; leave it, that is the sample you want, then tell
   the human it needs answering or stop the run and start it in a
   directory the CLI already trusts). While it runs `git status` you see
   the tool-running screen, and while it counts the plain busy screen
   (both *working*: spinner, elapsed time — note what they share and where
   they differ). When it has finished you see the empty input box
   (*waiting*). Keep one capture of each, including both working faces.
5. Write the three lists and test them without saving:
   ```
   erbrus screen test --provider <name> --run <id> --working '<re>' --waiting '<re>' --question '<re>'
   ```
   It prints the resulting state and marks which line matched which kind.
   Flags repeat for more patterns.
6. Save: `erbrus provider set <name> --working '<re>' --waiting '<re>' --question '<re>'`
   (repeat flags; `--clear-rules` returns to built-ins). The running
   erbrus applies the change immediately.
7. `erbrus stop <id>` the run you started, then report what you
   configured and which of the three states you could not verify.

### Worked examples (built in)

claude-code — working `\S+… \(\d+`, `⎿\s+Running…`; waiting `^─{8,}\n❯`
(input box under a rule); question `^❯ \d+\.`, `Esc to cancel`,
`Esc to go back`, `\(y/n\)`, `\[Y/n\]`, `Do you want to proceed`.

codex — working `Working \(\d+`, `(?i)esc to interrupt`; waiting
`^›[^\n]*\n[^\n]*· /` (input line above the model/cwd status line);
question `\(y/n\)`, `\[Y/n\]`, `Press Enter`, `^\s*[›>] \d+\.`.

junie — command `junie --brave --model {model} {args}` with `prompt:
paste`: a task given on the command line runs ONCE and the process
exits, and `--prompt` is swallowed by the trust dialog, so erbrus pastes
the prompt into interactive mode. working `^[⠋-⠿] ` (any spinner line:
"Thinking... esc to stop", "Sending 1 prompt", "Running <cmd>" — the last has
no "esc to stop" suffix, and the input box stays on screen while junie
works, so the spinner glyph alone must decide); waiting
`^>[^\n]*\n~ ` (the "> Type your prompt..." box right above the "~ <dir>"
status bar); question `Trust this project`, `needs your trust decision`,
`Allow running this command\?`, `Or reject with a reason`, `space to
select`, `Or type your own answer`.

### How the keypad reads a dialog's choices

Once a screen is a *question*, erbrus reads the choices itself; they are
not configured per provider. Three layouts are understood:

- numbered lines: `❯ 1. Yes` / `2. No` — the button presses the digit;
- a cursor list: a marker (`> ❯ › → ● ▶`) on the current line and the
  other choices in the same text column right above/below it — the
  button moves the cursor there and presses Enter;
- a radio list: `→ ○ Ice cream (recommended)` / `○ Sweet rolls`, with
  description lines indented deeper between them — same as above, the
  glyph is stripped from the label.

Question lines ("…?"), navigation hints (`↑/↓`, `space to select`,
`esc …`) and the free-text prompt (`> Or type your own answer…`) are
never offered as choices. If your CLI's dialog is a different layout the
keypad shows only ↑ ↓ Enter Esc; then `erbrus screen capture --run <id>
--lines 30` while the dialog is up, and put the exact lines in your
report so the layout can be added.

Watch for this one-shot trap with any CLI: if the spawned run ends
seconds after start with "finished · exit 0" and a report, the command
ran a single task instead of an interactive session. Look for a flag
that starts interactive mode with a prompt, or use `prompt: paste`.

kimi (registry default, working rule unverified) — command
`kimi --yolo --model {model} {args}`, `prompt: paste`; working
`^[🌑🌒🌓🌔🌕🌖🌗🌘] `, `Retrying \(\d+/\d+\)`; waiting `^│ >[^\n]*\n╰`;
question `Trust this folder\?`, `Enter select`, `Esc exit`.

antigravity — `agy --dangerously-skip-permissions --model "{model}" {args} -i "{prompt}"`.
