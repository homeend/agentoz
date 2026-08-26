package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
)

type sseEvent struct {
	Event string
	Data  []byte
}

type Hub struct {
	mu   sync.Mutex
	subs map[chan sseEvent]struct{}
}

func NewHub() *Hub { return &Hub{subs: map[chan sseEvent]struct{}{}} }

func (h *Hub) Publish(event string, data any) {
	b, err := json.Marshal(data)
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- sseEvent{Event: event, Data: b}:
		default: // slow subscriber: drop rather than block the server
		}
	}
}

func (h *Hub) Subscribe() (<-chan sseEvent, func()) {
	ch := make(chan sseEvent, 16)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs, ch)
		h.mu.Unlock()
	}
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		httpError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	ch, cancel := s.hub.Subscribe()
	defer cancel()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, ": connected\n\n")
	fl.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-ch:
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Event, ev.Data)
			fl.Flush()
		}
	}
}
