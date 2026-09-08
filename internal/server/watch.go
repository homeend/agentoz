package server

import (
	"fmt"
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
	if st != screen.Unknown && st != prev.State {
		next.State, next.Since = st, now
		changed = true
		switch {
		case st == screen.Question:
			notes = append(notes, fmt.Sprintf("%s is asking a question — needs your input", run.AgentName))
		case st == screen.Waiting && prev.State == screen.Working:
			notes = append(notes, fmt.Sprintf("%s finished its turn — waiting for input", run.AgentName))
		}
	}
	next.StepFor = screen.StepDuration(lines)
	stalled := !sc.Activity.IsZero() && now.Sub(sc.Activity) > stallAfter &&
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
		s.hub.Publish("run", toRunJSON(run))
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

// badge renders the rail label + CSS class for a run state.
func (rs runState) badge(now time.Time) (label, class string) {
	// Durations are "since this state began", computed at render time:
	// the spinner's own step timer would freeze between rail refreshes.
	switch rs.State {
	case screen.Working:
		label = "working " + shortDur(now.Sub(rs.Since))
	case screen.Waiting:
		label = "waiting " + shortDur(now.Sub(rs.Since))
	case screen.Question:
		label = "needs input"
	default:
		if !rs.Stalled {
			return "", ""
		}
		label = "no output"
	}
	class = "state st-" + string(rs.State)
	if rs.Stalled {
		label = "stalled · " + label
		class += " stalled"
	}
	return label, class
}
