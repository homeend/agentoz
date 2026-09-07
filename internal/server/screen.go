package server

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html/template"
	"sync"
	"time"

	"erbrus/internal/spawn"
)

// screenFrame is one rendered snapshot of a run's terminal as sent to the
// screen page (initial render and every SSE update).
type screenFrame struct {
	HTML     template.HTML `json:"html"`
	Activity int64         `json:"activity"` // unix seconds; 0 when unknown
	Dead     bool          `json:"dead"`
	Cols     int           `json:"cols"`
	Rows     int           `json:"rows"`
	Error    string        `json:"error,omitempty"`
}

// frameOf renders a capture (or its error) and returns the frame plus a
// fingerprint that changes iff the visible state changed. The HTML is not
// part of the fingerprint: hashing the raw screen is cheaper and equivalent.
func frameOf(sc spawn.Screen, err error) (screenFrame, string) {
	if err != nil {
		msg := "screen unavailable: " + err.Error()
		return screenFrame{Error: msg}, "err:" + msg
	}
	var act int64
	if !sc.Activity.IsZero() {
		act = sc.Activity.Unix()
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d|%t|%d|%d|%s", act, sc.Dead, sc.Cols, sc.Rows, sc.Raw)))
	return screenFrame{
		HTML: ansiToHTML(sc.Raw), Activity: act, Dead: sc.Dead, Cols: sc.Cols, Rows: sc.Rows,
	}, hex.EncodeToString(sum[:])
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
func (f *screenFeed) Subscribe(runID int64, h spawn.Handle) (<-chan screenFrame, func()) {
	ch := make(chan screenFrame, 4)
	f.mu.Lock()
	p, ok := f.runs[runID]
	if !ok {
		p = &screenPoll{handle: h, subs: map[chan screenFrame]struct{}{}, stop: make(chan struct{})}
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
	frame, fp := frameOf(sc, err)
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
