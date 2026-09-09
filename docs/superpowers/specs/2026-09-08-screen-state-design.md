# erbrus — screen state detection (live screen round 2) design

Date: 2026-09-08. Status: approved in chat ("proceed to phase 2").
Predecessor: /mnt/t/others/erbrus/docs/superpowers/specs/2026-09-08-live-screen-design.md

## Purpose

Turn the rendered tmux screen into a small set of states a human can act on
without opening the screen page: the agent is **working**, **waiting** at its
prompt (turn finished), asking a **question** (a dialog needs a decision),
or **stalled** (working, but no terminal output for too long). Surface the
state on the run card, in the screen page header, and as one system message
per meaningful transition.

## What the real screens look like (captured 2026-09-08, Claude Code)

Working (window 0:7): a spinner line with an elapsed timer sits above the
input box; the input box itself is an empty `❯` line between two rules.

```
  ⎿  Running…
✻ Cogitating… (27s · ↓ 1.5k tokens · thought for 1s)
──────────────────────────────
❯
──────────────────────────────
  homeend@… /mnt/t/others/erbrus (main) …
  ⏵⏵ bypass permissions on (shift+tab to cycle)
```

Waiting (window 0:8): same box, no spinner line anywhere above it.

Question (documented dialog shape, NOT captured live: triggering one in the
user's sessions is off limits): the input box is replaced by a dialog such as

```
 Do you want to proceed?
 ❯ 1. Yes
   2. Yes, and don't ask again
   3. No
 Esc to cancel
```

Consequences:

- Regexes run in multi-line mode over the ANSI-stripped, trimmed,
  non-empty tail lines joined with newlines, so a rule may span two
  adjacent lines. Amended 2026-09-08 after a live miss: the input box can
  hold an unsubmitted message, so "waiting" is "❯ line directly under a
  horizontal rule", not "empty ❯ line". The raw capture
  carries color escapes around `❯`, so `^❯\s*$` can only match after
  stripping.
- Checks are ordered structurally so scrollback text cannot fake a state:
  spinner → working; else empty prompt line → waiting; else dialog markers
  → question; else unknown. While the prompt box is on screen, a quoted
  "Do you want to proceed" in the agent's last message cannot produce
  "question". A dialog's `❯ 1. Yes` starts with `❯` too, so "waiting"
  requires the empty-content form.
- "Nothing happens for too long" has two different faces. The spinner's
  own timer (`(7m 5s ·`) says how long the current step has run; tmux's
  `window_activity` says when the pane last printed anything. A long test
  run keeps the timer ticking, so activity stays fresh. **Stalled** means
  activity older than `stallAfter` (120 s) while the state is working or
  unknown. Silence in waiting or question is expected and never stalled.

## Scope

In:

- `internal/screen` package: ANSI strip + ANSI→HTML (moved from
  `internal/server/ansi.go`), `Tail`, `Classify`, `StepDuration`,
  `DefaultRules`, `Compile`.
- Optional per-provider regex overrides in config.
- A watcher inside the existing reconcile loop (interval lowered to 10 s)
  over running tmux runs; in-memory state per run; system messages on
  transitions; `run` events so the rail refreshes.
- Run card badge; screen page header shows live state and step duration.

Out:

- Persisting states (a restart re-derives them within one tick).
- Acting on states (auto-answering prompts, auto-stopping stalled runs).
- Per-channel or per-preset thresholds. `stallAfter` is a package var.

## States and transitions

```go
package screen
type State string
const (
    Unknown  State = ""         // mid-redraw, blank, or no rule matched
    Working  State = "working"
    Waiting  State = "waiting"  // at the prompt, turn finished
    Question State = "question" // dialog needs a decision
)
```

Watcher rules (per run, per tick, only for runs with status starting or
running and a tmux target; Reconcile runs first so gone windows are
already marked failed):

1. Capture; on error skip the run this tick.
2. `lines = Tail(Strip(raw), 15)`, `st = Classify(rules, lines)`.
3. **Never act on Unknown**: state, `since`, and notifications are left as
   they were. This is the debounce; captures mid-redraw are common.
4. If `st` differs from the stored state: store it with `since = now`,
   publish a `run` event, and post a system message only for
   - entering **question**: "`<agent>` is asking a question — needs your input"
   - entering **waiting** from **working**: "`<agent>` finished its turn — waiting for input"
   (waiting from unknown, e.g. a no-prompt preset that starts at the
   prompt, posts nothing.)
5. `stalled = activity known && now − activity > stallAfter && state ∈ {working, unknown}`.
   On becoming stalled: system message "`<agent>` has printed nothing for
   2m0s — stalled?" and a `run` event. On clearing: `run` event only.
6. `stepFor = StepDuration(lines)` is stored every tick but publishes
   nothing (it changes every second; the rail picks it up on its next
   refresh, the screen page gets it live).
7. States of runs no longer running are dropped.

## Rules

```go
type Rules struct{ Working, Waiting, Question []*regexp.Regexp }
func DefaultRules(provider string) Rules
func Compile(working, waiting, question []string) (Rules, error)
```

Built-in defaults, keyed by provider name:

| provider | working | waiting | question |
|---|---|---|---|
| `claude-code` | `\S+… \(\d+`, `⎿\s+Running…` | `^─{8,}\n❯` (input box under a rule, typed text or not) | `^❯ \d+\.`, `Esc to cancel`, `Esc to go back`, `\(y/n\)`, `\[Y/n\]`, `Do you want to proceed` |
| `codex` | `Working \(\d+`, `(?i)esc to interrupt` | `^›[^\n]*\n[^\n]*· /` (input box above the model/cwd status line; verified live) | `\(y/n\)`, `\[Y/n\]`, `Press Enter`, `^\s*[›>] \d+\.` |
| anything else | (none) | `^[❯›>$]\s*$` | `\(y/n\)`, `\[Y/n\]`, `Press Enter`, `Do you want to` |

Codex defaults are best-effort from its TUI's known strings, not from a
capture. Config overrides per provider (global `config.yaml`, raw YAML on
the settings page, no UI work):

```yaml
providers:
  kimi:
    command: kimi --model {model} {args} "{prompt}"
    screen_working: ['Thinking \(']
    screen_waiting: ['^>\s*$']
    screen_question: ['CONFIRM']
```

Any non-empty list replaces the whole rule set for that provider (all three
lists are then taken from config; an empty one means "no patterns of that
kind"). Rules compile once in `server.New`; an invalid pattern logs a
warning to stderr and that provider falls back to its defaults. Unknown
provider names use the generic row.

`StepDuration(lines)` parses the first `… (…` timer it finds
(`(27s ·`, `(7m 5s ·`, `(1h 2m 3s ·`); 0 when absent.

## Surfaces

- **Run card** (`_runs.html`, running runs only): a badge after the agent
  name. Label: `working 7m` / `waiting 3m` / `needs input` / prefixed
  `stalled ·` when stalled. Class `state st-<state>` plus `stalled`;
  question and stalled use the attention color.
- **Screen page**: `screenFrame` gains `state` and `step` (seconds); the
  header shows `working · step 7m5s` etc. The amber tint now follows the
  server's rule: activity older than 120 s while working or unknown.
  `screenFeed.Subscribe` takes the provider's `Rules` so the feed can
  classify.
- **Channel**: the system messages above; they already raise the unread
  attention badge on other channels.

## Testing

- `internal/screen`: Strip on the committed Claude Code fixture; Tail; the
  0:7 and 0:8 captures as working/waiting fixtures verbatim; a synthetic
  dialog as question; scrollback phrase plus empty prompt → waiting;
  blank → unknown; StepDuration on the three timer shapes; DefaultRules
  for known and unknown names; Compile rejects a bad pattern; ToHTML tests
  move over unchanged.
- `internal/server`: `WatchScreens` called directly with a fake spawner:
  working→waiting posts exactly one system message and one `run` event; a
  repeat with the same screen posts nothing; an unknown screen changes
  nothing; a question screen notifies immediately; an old `Activity`
  while working posts the stalled message once; a provider override from
  config is honored; the runs panel renders the badge; the events stream
  carries `state`.
- Manual: restart serve, watch a working agent's card, let it finish, see
  the "finished its turn" message and the badge flip to waiting.

## Known limits (say so in the hand-over)

- Question detection is untested against a live dialog; the first real
  one is the test.
- Codex patterns are unverified.
- Rail badges show the step duration as of the last refresh, not live.

## Amendments after live use (2026-09-08, same day)

- **Labels.** The waiting state is labeled **idle** everywhere a human reads
  it ("idle 59s", "finished its turn — idle, waiting for input"); "waiting"
  read as busy. Internal state name stays `waiting`.
- **Run card.** The dot follows the agent state (grey until classified,
  green working, blue idle, amber question/stalled); the lifecycle status
  line reads "up since …" instead of "running". Timed labels carry
  `data-since` and the page ticks them every second; the rail re-fetches
  every 15 s.
- **Keypad.** `POST /ui/runs/{id}/keys` presses one of `1 2 3 Up Down
  Enter Escape` (allow-list, nothing else) via `Spawner.SendKeys`, then
  re-observes the screen after 300 ms. Shown on run cards in the question
  state and always on the screen page (`back=screen` redirects there).
- **Miniature.** In the question state the card shows the rendered screen
  from the capture that detected the dialog (`runState.Thumb`, no extra
  tmux calls) at a glance-size font, linking to the screen page's named
  tab (`target="screen:<tmux target>"`, one tab per window).
- **Stalled** requires a live pane (`!Dead`).
- **Verified live:** working, idle (including an unsubmitted message in the
  input box), and one real question dialog (Claude Code's trust-folder
  prompt, matched by "Esc to cancel"). The keypad has not yet answered a
  real dialog.

## Amendment 2026-09-09: cursor-style dialogs get one-click answers

Antigravity (and Kimi) dialogs are not numbered; they are a column of
choices with a cursor marker on the current one:

```
> Yes, I trust this folder
  No, exit
  ↑/↓ Navigate · enter Confirm
```

`screen.Options` only reads `1.`/`2.` lines, so the keypad fell back to
arrows and the user had to press ↓ and Enter by hand.

- `screen.CursorOptions(raw)` reads the *untrimmed* screen: the last line
  matching `^\s*[>❯›●◉▸▶]\s+\S` fixes the text column; the contiguous
  lines above and below whose text starts in that column are the other
  choices. Navigation hints (`↑ ↓`, navigate, confirm, esc) never count,
  the right-aligned model line falls out by indentation, a marker line
  with no siblings is an input prompt, not a dialog. Options carry
  `Key: pick:<i>`, `Pick: true`, and `Current` on the cursor's line.
  `screen.DialogOptions(raw, lines)` prefers numbered options and falls
  back to cursor ones; all three producers (frame, watcher, screen API)
  use it.
- The keypad renders a pick option as one button with the choice's text
  (`› ` marks where the cursor is now). `POST …/keys` with `key=pick:<i>`
  recaptures the screen at press time, finds the cursor, presses Down or
  Up the needed number of times 60 ms apart, then Enter. If the screen no
  longer shows a cursor dialog nothing is pressed and the redirect
  carries a warning. `pick:` accepts 0–8 only; everything else in the
  allow-list is unchanged.
