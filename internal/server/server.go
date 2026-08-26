// Package server is the erbrus HTTP API and (in Plan 3) web UI. Localhost
// only; agents authenticate with per-run bearer tokens, the human needs
// none.
package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"erbrus/internal/config"
	"erbrus/internal/store"
	"erbrus/internal/wt"
)

type Server struct {
	st      *store.Store
	cfg     config.Global
	run     wt.Runner
	dataDir string
	hub     *Hub
}

func New(st *store.Store, cfg config.Global, run wt.Runner) *Server {
	return &Server{st: st, cfg: cfg, run: run, dataDir: cfg.ResolvedDataDir(), hub: NewHub()}
}

func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Route("/api", func(r chi.Router) {
		r.Post("/projects", s.handleAddProject)
		r.Get("/projects", s.handleListProjects)
		r.Get("/channels/{id}/messages", s.handleGetMessages)
		r.Post("/channels/{id}/messages", s.handlePostMessage)
		r.Post("/messages/{id}/forward", s.handleForward)
	})
	r.Get("/events", s.handleEvents)
	return r
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// bearerRun resolves the Authorization header to a run. failStatus is 0 on
// success (including "no auth given", which means human), or an HTTP status
// to fail the request with: 500 on store error, 401 on invalid token.
func (s *Server) bearerRun(r *http.Request) (store.AgentRun, bool, int) {
	h := r.Header.Get("Authorization")
	if h == "" {
		return store.AgentRun{}, false, 0
	}
	token := strings.TrimPrefix(h, "Bearer ")
	run, ok, err := s.st.RunByToken(token)
	if err != nil {
		return store.AgentRun{}, false, http.StatusInternalServerError
	}
	if !ok {
		return store.AgentRun{}, false, http.StatusUnauthorized
	}
	return run, true, 0
}
