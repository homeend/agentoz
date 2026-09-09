package server

import (
	"fmt"
	"strings"
	"time"

	"erbrus/internal/config"
	"erbrus/internal/screen"
	"erbrus/internal/spawn"
	"erbrus/internal/store"
)

// Paste delivery pacing. Package vars so tests can shorten them.
var (
	pastePoll = 500 * time.Millisecond
	// pasteDeadline: how long a screen may stay unclassified before the
	// prompt is given up (a dialog extends it). Junie on a slow start (update
	// extraction, auth, logo) took over a minute to show its box — seen live
	// 2026-09-09 as "no input box within 1m, screen state unknown".
	pasteDeadline = 5 * time.Minute
	pasteMax      = 30 * time.Minute // hard cap, dialog or not
	pasteSettle   = 2 * time.Second  // no rules: paste once the screen sat still this long
)

// pasteMode: the prompt is typed into the terminal instead of passed on
// the command line — explicitly (prompt: paste) or because the template
// has nowhere to put it (Kimi Code's interactive mode takes none).
func pasteMode(p config.Provider) bool {
	switch p.Prompt {
	case "paste":
		return true
	case "arg":
		return false
	}
	return !strings.Contains(p.Command, "{prompt}")
}

// delivery describes one text waiting for an agent's input box.
type delivery struct {
	what string // "prompt" | "message" — for the channel notes
	// acceptWorking: a busy agent is fine too (chat: Claude Code and
	// Antigravity queue typed input while they work). The spawn prompt
	// waits for the input box proper.
	acceptWorking bool
}

var (
	promptDelivery = delivery{what: "prompt"}
	chatDelivery   = delivery{what: "message", acceptWorking: true}
)

// deliverPrompt waits for the agent's input box and pastes prompt once.
// Runs in its own goroutine right after a tmux spawn.
func (s *Server) deliverPrompt(run store.AgentRun, prompt string) {
	s.deliverWhenReady(run, prompt, promptDelivery)
}

// bootGrace: a run younger than this is still starting — banner, sign-in
// spinner, trust check, then the preamble as its first turn. Text typed
// then can be lost, and a "working" match means the sign-in spinner as
// easily as real work (seen live 2026-09-09: "⣾ Signing in..." matched
// Antigravity's working rule, the queue released, the message vanished).
// Until the grace is over only the input box counts as ready.
var bootGrace = 30 * time.Second

// readyFor: can text be typed into run, whose screen is in state st?
func (d delivery) readyFor(run store.AgentRun, st screen.State) bool {
	if st == screen.Waiting {
		return true
	}
	return d.acceptWorking && st == screen.Working && time.Since(run.CreatedAt) >= bootGrace
}

// mustQueue says why a chat message cannot be typed into run right now
// ("" when it can): a dialog is up, or the agent is still starting. A
// screen the rules cannot read on a run past its boot is typed into
// directly, as before — queuing every message to a rule-less provider
// would only add noise.
func (s *Server) mustQueue(run store.AgentRun) string {
	sc, err := s.spawner.Capture(spawn.Handle(run.TmuxTarget))
	if err != nil {
		return ""
	}
	young := time.Since(run.CreatedAt) < bootGrace
	if strings.TrimSpace(screen.Strip(sc.Raw)) == "" {
		// A blank pane on a fresh run: the CLI has not drawn yet; text
		// typed now lands in whatever it shows first (its trust dialog).
		if young {
			return "is still starting"
		}
		return ""
	}
	st := screen.Classify(s.rulesFor(run.Provider), screen.Tail(screen.Strip(sc.Raw), 15))
	switch {
	case chatDelivery.readyFor(run, st):
		return ""
	case st == screen.Question:
		return "is showing a dialog (answer it on its card)"
	case young:
		return "is still starting"
	}
	return ""
}

// deliverWhenReady polls the screen until the agent can take text, then
// delivers once and notes the outcome in the channel. A question on
// screen (a trust dialog, a permission prompt) is the human's to answer;
// pasting into it would answer the dialog with the text's Enter and lose
// the text (seen live 2026-09-09: a message sent 3 s after spawning
// Antigravity vanished into its trust dialog). Delivery waits instead.
func (s *Server) deliverWhenReady(run store.AgentRun, text string, d delivery) {
	// Pacing is read once: tests shorten the package vars per test.
	poll, wait, settle, hardMax := pastePoll, pasteDeadline, pasteSettle, pasteMax
	h := spawn.Handle(run.TmuxTarget)
	rules := s.rulesFor(run.Provider)
	// Only rules written for this provider (config or built-in) can be
	// trusted to say "waiting"; the generic fallback matches a bare prompt
	// glyph and nothing else, so for an unknown CLI a screen that stopped
	// changing is the best signal there is.
	p, _ := s.providerCfg(run.Provider)
	specific := screen.HasDefaults(run.Provider) || len(p.ScreenWorking)+len(p.ScreenWaiting)+len(p.ScreenQuestion) > 0
	canSeeWaiting := specific && len(rules.Waiting) > 0
	start := time.Now()
	deadline := start.Add(wait)
	var lastRaw string
	var stableSince time.Time
	fail := func(reason string) {
		if d.what == "prompt" {
			s.system(run.ChannelID, fmt.Sprintf("%s: prompt NOT delivered (%s) — paste it yourself", run.AgentName, reason))
			return
		}
		s.system(run.ChannelID, fmt.Sprintf("%s: queued message NOT delivered (%s) — resend it when the agent is idle", run.AgentName, reason))
	}
	for {
		time.Sleep(poll)
		if cur, ok, _ := s.st.RunByID(run.ID); !ok || (cur.Status != "running" && cur.Status != "starting") {
			return
		}
		sc, err := s.spawner.Capture(h)
		if err != nil {
			fail("window gone")
			return
		}
		lines := screen.Tail(screen.Strip(sc.Raw), 15)
		st := screen.Classify(rules, lines)
		now := time.Now()
		ready := d.readyFor(run, st)
		switch {
		case st == screen.Question:
			deadline = now.Add(wait)
		case !canSeeWaiting && st == screen.Unknown && len(lines) > 0:
			if sc.Raw != lastRaw {
				lastRaw, stableSince = sc.Raw, now
			} else if now.Sub(stableSince) >= settle {
				ready = true
			}
		}
		if ready {
			if err := s.deliver(run, text); err != nil {
				fail(err.Error())
				return
			}
			if d.what == "prompt" {
				s.system(run.ChannelID, fmt.Sprintf("%s: prompt delivered", run.AgentName))
			} else {
				s.system(run.ChannelID, fmt.Sprintf("%s: queued message delivered", run.AgentName))
			}
			return
		}
		if now.After(deadline) || now.Sub(start) > hardMax {
			label := string(st)
			if label == "" {
				label = "unknown"
			}
			fail("no input box within " + shortDur(now.Sub(start)) + ", screen state " + label)
			return
		}
	}
}
