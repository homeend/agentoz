// Package server is the erbrus HTTP API and (in Plan 3) web UI. Localhost
// only; agents authenticate with per-run bearer tokens, the human needs
// none.
package server

import (
	"encoding/json"
	"html/template"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"erbrus/internal/config"
	"erbrus/internal/spawn"
	"erbrus/internal/store"
	"erbrus/internal/web"
	"erbrus/internal/wt"
)

type Server struct {
	st      *store.Store
	cfg     config.Global
	run     wt.Runner
	dataDir string
	hub     *Hub
	pages   map[string]*template.Template

	spawner   spawn.Spawner
	erbrusBin string
	baseURL   string
}

func New(st *store.Store, cfg config.Global, run wt.Runner) *Server {
	return &Server{
		st:      st,
		cfg:     cfg,
		run:     run,
		dataDir: cfg.ResolvedDataDir(),
		hub:     NewHub(),
		pages:   web.Pages(template.FuncMap{"localtime": localTime}),
	}
}

// SetRuntime wires the spawn runtime (spawner, erbrus binary path, and the
// base URL agents call back to). Called by serve; nil-safe fields — a
// Server without SetRuntime simply has no spawner (tmux spawns 503).
func (s *Server) SetRuntime(sp spawn.Spawner, erbrusBin, baseURL string) {
	s.spawner, s.erbrusBin, s.baseURL = sp, erbrusBin, baseURL
}

func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Route("/api", func(r chi.Router) {
		r.Post("/projects", s.handleAddProject)
		r.Get("/projects", s.handleListProjects)
		r.Get("/channels/{id}/messages", s.handleGetMessages)
		r.Post("/channels/{id}/messages", s.handlePostMessage)
		r.Post("/messages/{id}/forward", s.handleForward)
		r.Post("/runs", s.handleSpawnRun)
		r.Post("/runs/{id}/exit", s.handleRunExit)
		r.Post("/runs/{id}/stop", s.handleRunStop)
		r.Get("/channels/{id}/runs", s.handleChannelRuns)
		r.Get("/artifacts/{id}", s.handleArtifactDownload)
	})
	r.Get("/events", s.handleEvents)
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ui/projects", http.StatusFound)
	})
	r.Handle("/static/*", http.StripPrefix("/static/", web.Static()))
	r.Get("/ui/projects", s.handleUIProjects)
	r.Post("/ui/projects", s.handleUIAddProject)
	r.Get("/ui/channels/{id}", s.handleUIChannel)
	r.Get("/ui/channels/{id}/stream", s.handleUIChannelStream)
	r.Get("/ui/channels/{id}/runs-panel", s.handleUIRunsPanel)
	r.Post("/ui/channels/{id}/messages", s.handleUIComposeMessage)
	r.Post("/ui/runs/{id}/stop", s.handleUIStopRun)
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
