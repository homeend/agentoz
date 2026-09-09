package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"erbrus/internal/spawn"
	"erbrus/internal/termview"
)

// maxTerminals caps concurrent browser terminals; each is a pty plus a
// tmux client. termSizePoll is how often the window size is re-read so
// the browser follows the user's real terminal.
var (
	maxTerminals int32 = 8
	termSizePoll       = 2 * time.Second
)

// termSize is the one control message; it goes as a text frame, output
// goes as binary frames, so the client tells them apart by type.
type termSize struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

func (s *Server) openTerminals() int { return int(atomic.LoadInt32(&s.terminals)) }

// closeReason fits the 123-byte limit of a websocket close frame.
func closeReason(err error) string {
	r := err.Error()
	if len(r) > 120 {
		r = r[:120]
	}
	return r
}

// handleUITerminal bridges one websocket to a pty running the spawner's
// view client for the run's window. Everything a terminal would send goes
// through untouched; the only framing is the size message.
func (s *Server) handleUITerminal(w http.ResponseWriter, r *http.Request) {
	run, ok, err := s.st.RunByID(chiInt64(r, "id"))
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok || run.TmuxTarget == "" || (run.Status != "starting" && run.Status != "running") {
		httpError(w, http.StatusNotFound, "run has no live tmux window")
		return
	}
	viewer, ok := s.spawner.(spawn.Viewer)
	if !ok {
		httpError(w, http.StatusNotImplemented, "terminal not supported for this spawner")
		return
	}
	if atomic.AddInt32(&s.terminals, 1) > maxTerminals {
		atomic.AddInt32(&s.terminals, -1)
		httpError(w, http.StatusServiceUnavailable, "too many open terminals")
		return
	}
	defer atomic.AddInt32(&s.terminals, -1)

	// Accept's default origin policy: Origin must match Host; no Origin
	// is fine. Same rule as originGuard.
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer c.CloseNow()

	h := spawn.Handle(run.TmuxTarget)
	cols, rows, err := viewer.Size(h)
	if err != nil {
		c.Close(websocket.StatusInternalError, closeReason(err))
		return
	}
	view := spawn.ViewName(run.ID)
	argv, err := viewer.ViewCommand(h, view)
	if err != nil {
		c.Close(websocket.StatusInternalError, closeReason(err))
		return
	}
	term, err := termview.Start(argv, cols, rows, termview.Env(os.Environ()))
	if err != nil {
		c.Close(websocket.StatusInternalError, closeReason(err))
		return
	}
	defer term.Close()
	// The view owns only this window (see spawn.ViewCommand), so it ends
	// by itself when the window dies; the size poll below is the fallback.

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	sendSize := func(cols, rows int) error {
		b, _ := json.Marshal(termSize{cols, rows})
		return c.Write(ctx, websocket.MessageText, b)
	}
	if err := sendSize(cols, rows); err != nil {
		return
	}

	// Three pumps; the first to finish ends the session. done is buffered
	// so the others can report after the handler stopped listening.
	done := make(chan string, 3)
	go func() { // pty -> browser
		buf := make([]byte, 32<<10)
		for {
			n, err := term.Read(buf)
			if n > 0 {
				if err := c.Write(ctx, websocket.MessageBinary, buf[:n]); err != nil {
					done <- ""
					return
				}
			}
			if err != nil {
				done <- "window gone"
				return
			}
		}
	}()
	go func() { // browser -> pty
		for {
			_, data, err := c.Read(ctx)
			if err != nil {
				done <- ""
				return
			}
			if _, err := term.Write(data); err != nil {
				done <- "window gone"
				return
			}
		}
	}()
	go func() { // follow the user's terminal size
		t := time.NewTicker(termSizePoll)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			nc, nr, err := viewer.Size(h)
			if err != nil {
				done <- "window gone"
				return
			}
			if nc != cols || nr != rows {
				cols, rows = nc, nr
				term.Resize(nc, nr)
				if err := sendSize(nc, nr); err != nil {
					done <- ""
					return
				}
			}
		}
	}()
	reason := <-done
	cancel()
	term.Close() // ends the pty pump's Read
	c.Close(websocket.StatusNormalClosure, reason)
}
