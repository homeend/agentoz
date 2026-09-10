package server

import (
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"erbrus/internal/screen"
	"erbrus/internal/spawn"
	"erbrus/internal/store"
)

// runState is what the watcher last concluded about a running agent.
type runState struct {
	State   screen.State
	Since   time.Time
	Stalled bool
	StepFor time.Duration // current step per the spinner; 0 when unknown
	// Thumb is the rendered screen from the capture that showed the
	// dialog; set only in the question state so the rail can show a
	// "look here" miniature without extra tmux calls.
	Thumb template.HTML
	// Options: the dialog's numbered choices (question state only).
	Options []screen.Option
}

// stallAfter: no terminal output for this long while (apparently) working
// is reported as a stall. Waiting and question states are silent by
// nature and never stall.
var stallAfter = 120 * time.Second

// WatchScreens classifies every running tmux run once. Called from the
// reconcile loop right after Reconcile, so windows already gone are marked
// failed before we try to capture them.
func (s *Server) WatchScreens() error {
	if s.spawner == nil {
		return nil
	}
	runs, err := s.st.RunningRuns()
	if err != nil {
		return err
	}
	live := map[int64]bool{}
	now := time.Now()
	for _, r := range runs {
		if r.Spawner != "tmux" || r.TmuxTarget == "" {
			continue
		}
		if s.isToolRun(r) {
			continue // a shell has no working/waiting/question states
		}
		live[r.ID] = true
		sc, err := s.spawner.Capture(spawn.Handle(r.TmuxTarget))
		if err != nil {
			continue // window flapping; Reconcile handles the dead case
		}
		s.observe(r, sc, now)
	}
	s.stateMu.Lock()
	for id := range s.states {
		if !live[id] {
			delete(s.states, id)
		}
	}
	s.stateMu.Unlock()
	return nil
}

// observe folds one capture into the run's state, posting system messages
// on the transitions worth a human's attention and a run event whenever
// the rail's badge would change. Unknown screens change nothing: captures
// land mid-redraw often enough that acting on them would flap.
func (s *Server) observe(run store.AgentRun, sc spawn.Screen, now time.Time) {
	lines := screen.Tail(screen.Strip(sc.Raw), 15)
	st := screen.Classify(s.rulesFor(run.Provider), lines)

	s.stateMu.Lock()
	prev := s.states[run.ID]
	next := prev
	changed := false
	var notes []string
	switch st {
	case screen.Question:
		next.Thumb = template.HTML(screen.ToHTML(sc.Raw))
		next.Options = screen.DialogOptions(sc.Raw, lines)
	case screen.Unknown:
		// mid-redraw: keep whatever we had
	default:
		next.Thumb, next.Options = "", nil
	}
	if st != screen.Unknown && st != prev.State {
		next.State, next.Since = st, now
		changed = true
		switch {
		case st == screen.Question:
			notes = append(notes, fmt.Sprintf("%s is asking a question — needs your input", run.AgentName))
		case st == screen.Waiting && prev.State == screen.Working:
			notes = append(notes, fmt.Sprintf("%s finished its turn — idle, waiting for input", run.AgentName))
		}
	}
	next.StepFor = screen.StepDuration(lines)
	// A dead pane is silent by definition: "stalled" is for live processes.
	stalled := !sc.Dead && !sc.Activity.IsZero() && now.Sub(sc.Activity) > stallAfter &&
		(next.State == screen.Working || next.State == screen.Unknown)
	if stalled != prev.Stalled {
		changed = true
		if stalled {
			notes = append(notes, fmt.Sprintf("%s has printed nothing for %s — stalled?", run.AgentName, shortDur(now.Sub(sc.Activity))))
		}
	}
	next.Stalled = stalled
	s.states[run.ID] = next
	s.stateMu.Unlock()

	for _, n := range notes {
		s.system(run.ChannelID, n)
	}
	if changed {
		rj := toRunJSON(run)
		rj.State, rj.Stalled = string(next.State), next.Stalled
		s.hub.Publish("run", rj)
	}
}

func (s *Server) stateOf(runID int64) (runState, bool) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	rs, ok := s.states[runID]
	return rs, ok
}

// shortDur: "5s", "3m", "1h12m" — badge-sized.
func shortDur(d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

// badge renders the rail label + CSS class for a run state. timed
// reports whether the label ends in a "since this state began" duration
// the page should keep ticking client-side (the server only re-renders the
// rail on transitions).
func (rs runState) badge(now time.Time) (label, class string, timed bool) {
	switch rs.State {
	case screen.Working:
		label, timed = "working", true
	case screen.Waiting:
		// "idle", not "waiting": readers took "waiting 59s" for busy.
		label, timed = "idle", true
	case screen.Question:
		label = "needs input"
	default:
		if !rs.Stalled {
			return "", "", false
		}
		label = "no output"
	}
	class = "state st-" + string(rs.State)
	if rs.Stalled {
		label = "stalled · " + label
		class += " stalled"
	}
	return label, class, timed
}

// keypadKeys are the only keys the UI may press in an agent's window:
// enough to answer any dialog (pick an option, move, confirm, cancel),
// nothing that could interrupt or type into the agent. Everything else is
// the chat composer's job.
var keypadKeys = []string{"Up", "Down", "Enter", "Escape"}

// allowedKey: a single digit (numbered option), "pick:<i>" (cursor-style
// option i, walked with the arrows then Enter), or one of keypadKeys.
func allowedKey(k string) bool {
	if len(k) == 1 && k[0] >= '1' && k[0] <= '9' {
		return true
	}
	if _, ok := pickIndex(k); ok {
		return true
	}
	for _, a := range keypadKeys {
		if a == k {
			return true
		}
	}
	return false
}

func pickIndex(k string) (int, bool) {
	rest, ok := strings.CutPrefix(k, "pick:")
	if !ok || len(rest) != 1 || rest[0] < '0' || rest[0] > '8' {
		return 0, false
	}
	return int(rest[0] - '0'), true
}

// keyGap paces the arrow presses of a pick so a TUI sees them one by one.
var keyGap = 60 * time.Millisecond

// pressPick answers a cursor-style dialog: recapture (the dialog may have
// moved since the button was drawn), find the cursor, walk it to option
// idx with Up/Down, confirm with Enter. An error means nothing was pressed.
func (s *Server) pressPick(h spawn.Handle, idx int) error {
	sc, err := s.spawner.Capture(h)
	if err != nil {
		return err
	}
	opts := screen.CursorOptions(sc.Raw)
	cur := -1
	for i, o := range opts {
		if o.Current {
			cur = i
		}
	}
	if cur < 0 || idx >= len(opts) {
		return fmt.Errorf("the dialog changed; pick again")
	}
	step, n := "Down", idx-cur
	if n < 0 {
		step, n = "Up", -n
	}
	for i := 0; i < n; i++ {
		if err := s.spawner.SendKeys(h, step); err != nil {
			return err
		}
		time.Sleep(keyGap)
	}
	return s.spawner.SendKeys(h, "Enter")
}

// handleUIRunKeys presses one allow-listed key in the run's window, then
// re-observes the screen right away so the badge does not wait for the
// next 10s tick. Redirects to the channel (form field "channel") or, with
// back=screen, to the run's screen page.
func (s *Server) handleUIRunKeys(w http.ResponseWriter, r *http.Request) {
	run, ok, err := s.st.RunByID(chiInt64(r, "id"))
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok || run.TmuxTarget == "" {
		httpError(w, http.StatusNotFound, "run has no tmux window")
		return
	}
	if s.spawner == nil {
		httpError(w, http.StatusServiceUnavailable, "no spawner configured")
		return
	}
	key := r.FormValue("key")
	if !allowedKey(key) {
		httpError(w, http.StatusBadRequest, "key not allowed")
		return
	}
	target := "/ui/channels/" + r.FormValue("channel")
	if r.FormValue("back") == "screen" {
		target = fmt.Sprintf("/ui/runs/%d/screen", run.ID)
	}
	h := spawn.Handle(run.TmuxTarget)
	if idx, ok := pickIndex(key); ok {
		err = s.pressPick(h, idx)
	} else {
		err = s.spawner.SendKeys(h, key)
	}
	if err != nil {
		http.Redirect(w, r, target+"?warning="+url.QueryEscape(err.Error()), http.StatusFound)
		return
	}
	time.Sleep(keySettle)
	if sc, err := s.spawner.Capture(spawn.Handle(run.TmuxTarget)); err == nil {
		s.observe(run, sc, time.Now())
	}
	http.Redirect(w, r, target, http.StatusFound)
}

// keySettle: how long a TUI needs to repaint after a keypress before the
// re-observe capture is worth taking.
var keySettle = 300 * time.Millisecond
