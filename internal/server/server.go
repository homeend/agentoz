// Package server is the erbrus HTTP API and (in Plan 3) web UI. Localhost
// only; agents authenticate with per-run bearer tokens, the human needs
// none.
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"

	"erbrus/internal/config"
	"erbrus/internal/screen"
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
	screens *screenFeed
	pages   map[string]*template.Template

	// rules: per-provider screen classification overrides from config,
	// compiled once. states: what the watcher last saw per running run.
	rules   map[string]screen.Rules
	stateMu sync.Mutex
	states  map[int64]runState

	spawner   spawn.Spawner
	erbrusBin string
	baseURL   string

	configPath string
}

func New(st *store.Store, cfg config.Global, run wt.Runner) *Server {
	s := &Server{
		st:      st,
		cfg:     cfg,
		run:     run,
		dataDir: cfg.ResolvedDataDir(),
		hub:     NewHub(),
		pages:   web.Pages(template.FuncMap{"localtime": localTime, "initial": initial}),
	}
	// Resolved per call: SetRuntime wires the spawner after New.
	s.screens = newScreenFeed(func(h spawn.Handle) (spawn.Screen, error) {
		if s.spawner == nil {
			return spawn.Screen{}, errors.New("no spawner configured")
		}
		return s.spawner.Capture(h)
	})
	s.states = map[int64]runState{}
	s.rules = map[string]screen.Rules{}
	for name, p := range cfg.Providers {
		if len(p.ScreenWorking)+len(p.ScreenWaiting)+len(p.ScreenQuestion) == 0 {
			continue
		}
		r, err := screen.Compile(p.ScreenWorking, p.ScreenWaiting, p.ScreenQuestion)
		if err != nil {
			fmt.Fprintf(os.Stderr, "erbrus: provider %s: %v (using built-in screen rules)\n", name, err)
			continue
		}
		s.rules[name] = r
	}
	return s
}

// rulesFor: config override if present, else built-ins for the name.
func (s *Server) rulesFor(provider string) screen.Rules {
	if r, ok := s.rules[provider]; ok {
		return r
	}
	return screen.DefaultRules(provider)
}

// SetRuntime wires the spawn runtime (spawner, erbrus binary path, and the
// base URL agents call back to). Called by serve; nil-safe fields — a
// Server without SetRuntime simply has no spawner (tmux spawns 503).
func (s *Server) SetRuntime(sp spawn.Spawner, erbrusBin, baseURL string) {
	s.spawner, s.erbrusBin, s.baseURL = sp, erbrusBin, baseURL
}

// SetConfigPath wires the global config.yaml path (from cli's configPath())
// so the settings page can show and edit it. Unset, the global section
// renders read-only with a note.
func (s *Server) SetConfigPath(p string) {
	s.configPath = p
}

// originGuard rejects state-changing requests whose Origin header names a
// non-local origin. Requests with NO Origin header (curl, agents, same-site
// form posts from older browsers) are always allowed.
func originGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if o := r.Header.Get("Origin"); o != "" {
				u, err := url.Parse(o)
				if err != nil || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1") {
					httpError(w, http.StatusForbidden, "cross-origin request rejected")
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(originGuard)
	r.Route("/api", func(r chi.Router) {
		r.Post("/projects", s.handleAddProject)
		r.Get("/projects", s.handleListProjects)
		r.Delete("/projects/{id}", s.handleDeleteProject)
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
	r.Post("/ui/projects/{id}/git-init", s.handleUIGitInit)
	r.Post("/ui/projects/{id}/sync", s.handleUISync)
	r.Get("/ui/projects/{id}/delete", s.handleUIDeleteConfirm)
	r.Post("/ui/projects/{id}/delete", s.handleUIDeleteProject)
	r.Get("/ui/channels/{id}", s.handleUIChannel)
	r.Get("/ui/channels/{id}/stream", s.handleUIChannelStream)
	r.Get("/ui/channels/{id}/runs-panel", s.handleUIRunsPanel)
	r.Get("/ui/runs/{id}/screen", s.handleUIScreen)
	r.Get("/ui/runs/{id}/screen/events", s.handleUIScreenEvents)
	r.Post("/ui/channels/{id}/messages", s.handleUIComposeMessage)
	r.Post("/ui/runs/{id}/stop", s.handleUIStopRun)
	r.Post("/ui/runs/{id}/keys", s.handleUIRunKeys)
	r.Post("/ui/runs/{id}/delete", s.handleUIDeleteRun)
	r.Post("/ui/messages/{id}/delete", s.handleUIDeleteMessage)
	r.Post("/ui/channels/{id}/clear", s.handleUIClearChannel)
	r.Post("/ui/channels/{id}/messages/delete-batch", s.handleUIDeleteBatch)
	r.Get("/ui/spawn", s.handleUISpawn)
	r.Post("/ui/spawn", s.handleUISpawnPost)
	r.Get("/ui/forward", s.handleUIForward)
	r.Post("/ui/forward", s.handleUIForwardPost)
	r.Get("/ui/settings", s.handleUISettings)
	r.Post("/ui/settings/global", s.handleUISettingsGlobal)
	r.Post("/ui/settings/repo", s.handleUISettingsRepo)
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
