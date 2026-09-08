package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"runtime"
	"sync"
	"time"

	"erbrus/internal/screen"
	"erbrus/internal/spawn"
	"erbrus/internal/store"
)

// screenFrame is one rendered snapshot of a run's terminal as sent to the
// screen page (initial render and every SSE update).
type screenFrame struct {
	HTML     template.HTML   `json:"html,omitempty"` // omitted on ?html=0 streams
	Activity int64           `json:"activity"` // unix seconds; 0 when unknown
	Dead     bool            `json:"dead"`
	Cols     int             `json:"cols"`
	Rows     int             `json:"rows"`
	State    string          `json:"state"`             // screen.State; "" = unknown
	Step     int             `json:"step"`              // seconds the current step has run, 0 if unknown
	Options  []screen.Option `json:"options,omitempty"` // dialog choices (question state)
	Error    string          `json:"error,omitempty"`
}

// frameOf renders a capture (or its error) and returns the frame plus a
// fingerprint that changes iff the visible state changed. The HTML is not
// part of the fingerprint: hashing the raw screen is cheaper and equivalent.
func frameOf(sc spawn.Screen, err error, rules screen.Rules) (screenFrame, string) {
	if err != nil {
		msg := "screen unavailable: " + err.Error()
		return screenFrame{Error: msg}, "err:" + msg
	}
	var act int64
	if !sc.Activity.IsZero() {
		act = sc.Activity.Unix()
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d|%t|%d|%d|%s", act, sc.Dead, sc.Cols, sc.Rows, sc.Raw)))
	lines := screen.Tail(screen.Strip(sc.Raw), 15)
	st := screen.Classify(rules, lines)
	f := screenFrame{
		HTML: template.HTML(screen.ToHTML(sc.Raw)), Activity: act, Dead: sc.Dead, Cols: sc.Cols, Rows: sc.Rows,
		State: string(st), Step: int(screen.StepDuration(lines).Seconds()),
	}
	if st == screen.Question {
		f.Options = screen.Options(lines)
	}
	return f, hex.EncodeToString(sum[:])
}

// screenPollInterval paces the capture loop of a viewed run. 500ms is
// snappy enough to follow a TUI and cheap: one tmux exec per tick, only
// while someone has the page open.
var screenPollInterval = 500 * time.Millisecond

// screenFeed polls Capture for runs that have at least one viewer and fans
// frames out to them. Frames stay off the global Hub on purpose: they are
// kilobytes each, several per second, and the Hub drops on a full buffer —
// chat events must never lose to screen traffic.
type screenFeed struct {
	capture  func(spawn.Handle) (spawn.Screen, error)
	interval time.Duration // snapshot of screenPollInterval at construction
	mu       sync.Mutex
	runs     map[int64]*screenPoll
}

type screenPoll struct {
	handle spawn.Handle
	rules  screen.Rules
	subs   map[chan screenFrame]struct{}
	last   screenFrame
	lastFP string
	stop   chan struct{}
}

func newScreenFeed(capture func(spawn.Handle) (spawn.Screen, error)) *screenFeed {
	return &screenFeed{capture: capture, interval: screenPollInterval, runs: map[int64]*screenPoll{}}
}

// Subscribe returns a channel of frames for runID (current frame first,
// then one per change) and a cancel that also stops polling when the last
// viewer leaves.
func (f *screenFeed) Subscribe(runID int64, h spawn.Handle, rules screen.Rules) (<-chan screenFrame, func()) {
	ch := make(chan screenFrame, 4)
	f.mu.Lock()
	p, ok := f.runs[runID]
	if !ok {
		p = &screenPoll{handle: h, rules: rules, subs: map[chan screenFrame]struct{}{}, stop: make(chan struct{})}
		f.runs[runID] = p
		go f.loop(p)
	}
	p.subs[ch] = struct{}{}
	if p.lastFP != "" {
		ch <- p.last // buffer is empty: cannot block
	}
	f.mu.Unlock()

	var once sync.Once
	return ch, func() {
		once.Do(func() {
			f.mu.Lock()
			defer f.mu.Unlock()
			delete(p.subs, ch)
			if len(p.subs) == 0 {
				close(p.stop)
				if f.runs[runID] == p {
					delete(f.runs, runID)
				}
			}
		})
	}
}

func (f *screenFeed) loop(p *screenPoll) {
	f.tick(p)
	t := time.NewTicker(f.interval)
	defer t.Stop()
	for {
		select {
		case <-p.stop:
			return
		case <-t.C:
			f.tick(p)
		}
	}
}

// tick captures once and fans the frame out iff it differs from the last.
// Slow viewers drop frames (each frame is a full screen, so the next one
// heals them).
func (f *screenFeed) tick(p *screenPoll) {
	sc, err := f.capture(p.handle)
	f.mu.Lock()
	defer f.mu.Unlock()
	select {
	case <-p.stop:
		return
	default:
	}
	frame, fp := frameOf(sc, err, p.rules)
	if fp == p.lastFP {
		return
	}
	p.last, p.lastFP = frame, fp
	for ch := range p.subs {
		select {
		case ch <- frame:
		default:
		}
	}
}

type screenPage struct {
	Run     store.AgentRun
	Live    bool
	Channel store.Channel
	Project store.Project
	Frame   screenFrame
	// Note replaces the live view when there is nothing to poll.
	Note string
	// Warning: ?warning= from a redirect (e.g. a failed keypress).
	Warning string
	// Terminal: the page hosts the interactive terminal (live tmux run on
	// a Viewer spawner, on a host with ptys). The <pre> is the fallback.
	Terminal bool
}

func (s *Server) handleUIScreen(w http.ResponseWriter, r *http.Request) {
	run, ok, err := s.st.RunByID(chiInt64(r, "id"))
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		httpError(w, http.StatusNotFound, "run not found")
		return
	}
	ch, _, _ := s.st.ChannelByID(run.ChannelID)
	pr, _, _ := s.st.ProjectByID(ch.ProjectID)
	page := screenPage{Run: run, Live: run.Status == "starting" || run.Status == "running", Channel: ch, Project: pr}
	switch {
	case run.TmuxTarget == "":
		page.Note = "no tmux window for this run"
	case s.spawner == nil:
		page.Note = "no spawner configured"
	default:
		sc, err := s.spawner.Capture(spawn.Handle(run.TmuxTarget))
		page.Frame, _ = frameOf(sc, err, s.rulesFor(run.Provider))
	}
	if _, ok := s.spawner.(spawn.Viewer); ok && page.Live && page.Note == "" && runtime.GOOS != "windows" {
		page.Terminal = true
	}
	page.Warning = r.URL.Query().Get("warning")
	s.render(w, "screen", page)
}

// handleUIScreenEvents streams frames for one run to one viewer. Separate
// from /events so screen traffic never competes with chat events.
func (s *Server) handleUIScreenEvents(w http.ResponseWriter, r *http.Request) {
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
	fl, ok := w.(http.Flusher)
	if !ok {
		httpError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	frames, cancel := s.screens.Subscribe(run.ID, spawn.Handle(run.TmuxTarget), s.rulesFor(run.Provider))
	defer cancel()
	// ?html=0: the page shows the interactive terminal and only needs the
	// header/keypad fields, not a second copy of the screen twice a second.
	noHTML := r.URL.Query().Get("html") == "0"

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, ": connected\n\n")
	fl.Flush()

	ping := time.NewTicker(pingInterval)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			if _, err := fmt.Fprint(w, "event: ping\ndata: {}\n\n"); err != nil {
				return
			}
			fl.Flush()
		case f := <-frames:
			if noHTML {
				f.HTML = ""
			}
			b, err := json.Marshal(f)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "event: frame\ndata: %s\n\n", b); err != nil {
				return
			}
			fl.Flush()
		}
	}
}
