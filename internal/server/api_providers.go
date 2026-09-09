package server

import (
	"encoding/json"
	"net/http"
	"os"
	"strconv"

	"github.com/go-chi/chi/v5"

	"erbrus/internal/config"
	"erbrus/internal/screen"
	"erbrus/internal/spawn"
)

// maxScreenLines caps ?lines= on the screen route: enough for a whole TUI
// screen, not a scrollback dump.
const maxScreenLines = 200

type screenJSON struct {
	State   string          `json:"state"`
	Lines   []string        `json:"lines"`
	Options []screen.Option `json:"options,omitempty"`
	Cols    int             `json:"cols"`
	Rows    int             `json:"rows"`
	Dead    bool            `json:"dead"`
}

// handleAPIRunScreen: the stripped tail of a run's screen plus its
// classification — what an agent needs to write screen rules for itself.
func (s *Server) handleAPIRunScreen(w http.ResponseWriter, r *http.Request) {
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
	n, _ := strconv.Atoi(r.URL.Query().Get("lines"))
	if n <= 0 {
		n = 15
	}
	if n > maxScreenLines {
		n = maxScreenLines
	}
	sc, err := s.spawner.Capture(spawn.Handle(run.TmuxTarget))
	if err != nil {
		httpError(w, http.StatusNotFound, "screen unavailable: "+err.Error())
		return
	}
	text := screen.Strip(sc.Raw)
	tail := screen.Tail(text, 15)
	st := screen.Classify(s.rulesFor(run.Provider), tail)
	out := screenJSON{State: string(st), Lines: screen.Tail(text, n), Cols: sc.Cols, Rows: sc.Rows, Dead: sc.Dead}
	if out.Lines == nil {
		out.Lines = []string{}
	}
	if st == screen.Question {
		out.Options = screen.DialogOptions(sc.Raw, tail)
	}
	writeJSON(w, http.StatusOK, out)
}

type screenTestReq struct {
	Run      int64    `json:"run"`
	Working  []string `json:"working"`
	Waiting  []string `json:"waiting"`
	Question []string `json:"question"`
}

type testLineJSON struct {
	Text  string `json:"text"`
	Match string `json:"match"`
}

// handleAPIScreenTest classifies a run's screen with UNSAVED patterns.
// The provider name need not exist yet: agents test before they save.
func (s *Server) handleAPIScreenTest(w http.ResponseWriter, r *http.Request) {
	var req screenTestReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "bad json: "+err.Error())
		return
	}
	rules, err := screen.Compile(req.Working, req.Waiting, req.Question)
	if err != nil {
		httpError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	run, ok, _ := s.st.RunByID(req.Run)
	if !ok || run.TmuxTarget == "" {
		httpError(w, http.StatusNotFound, "run has no tmux window")
		return
	}
	if s.spawner == nil {
		httpError(w, http.StatusServiceUnavailable, "no spawner configured")
		return
	}
	sc, err := s.spawner.Capture(spawn.Handle(run.TmuxTarget))
	if err != nil {
		httpError(w, http.StatusNotFound, "screen unavailable: "+err.Error())
		return
	}
	lines := screen.Tail(screen.Strip(sc.Raw), 15)
	out := struct {
		State string         `json:"state"`
		Lines []testLineJSON `json:"lines"`
	}{State: string(screen.Classify(rules, lines)), Lines: []testLineJSON{}}
	for _, l := range lines {
		out.Lines = append(out.Lines, testLineJSON{Text: l, Match: screen.MatchKind(rules, l)})
	}
	writeJSON(w, http.StatusOK, out)
}

type providerJSON struct {
	Name           string   `json:"name"`
	Command        string   `json:"command"`
	DefaultModel   string   `json:"default_model"`
	Prompt         string   `json:"prompt"`
	ScreenWorking  []string `json:"screen_working"`
	ScreenWaiting  []string `json:"screen_waiting"`
	ScreenQuestion []string `json:"screen_question"`
	RulesSource    string   `json:"rules_source"`
}

func providerToJSON(name string, p config.Provider) providerJSON {
	src := "config override"
	if len(p.ScreenWorking)+len(p.ScreenWaiting)+len(p.ScreenQuestion) == 0 {
		src = "built-in (generic)"
		if screen.HasDefaults(name) {
			src = "built-in (" + name + ")"
		}
	}
	nz := func(l []string) []string {
		if l == nil {
			return []string{}
		}
		return l
	}
	return providerJSON{Name: name, Command: p.Command, DefaultModel: p.DefaultModel, Prompt: p.Prompt,
		ScreenWorking: nz(p.ScreenWorking), ScreenWaiting: nz(p.ScreenWaiting), ScreenQuestion: nz(p.ScreenQuestion), RulesSource: src}
}

func (s *Server) handleAPIGetProvider(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	s.rulesMu.RLock()
	p, ok := s.cfg.Providers[name]
	s.rulesMu.RUnlock()
	if !ok {
		httpError(w, http.StatusNotFound, "unknown provider")
		return
	}
	writeJSON(w, http.StatusOK, providerToJSON(name, p))
}

type providerPatchJSON struct {
	Command        *string   `json:"command"`
	DefaultModel   *string   `json:"default_model"`
	Prompt         *string   `json:"prompt"`
	ScreenWorking  *[]string `json:"screen_working"`
	ScreenWaiting  *[]string `json:"screen_waiting"`
	ScreenQuestion *[]string `json:"screen_question"`
}

// handleAPIPutProvider creates or updates a provider: validates, rewrites
// config.yaml (comments kept), then applies to the running server.
func (s *Server) handleAPIPutProvider(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	var req providerPatchJSON
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "bad json: "+err.Error())
		return
	}
	if req.Prompt != nil && *req.Prompt != "" && *req.Prompt != "arg" && *req.Prompt != "paste" {
		httpError(w, http.StatusUnprocessableEntity, `prompt must be "arg" or "paste"`)
		return
	}
	s.rulesMu.RLock()
	next := s.cfg.Providers[name] // zero value when new
	s.rulesMu.RUnlock()
	if req.Command != nil {
		next.Command = *req.Command
	}
	if req.DefaultModel != nil {
		next.DefaultModel = *req.DefaultModel
	}
	if req.Prompt != nil {
		next.Prompt = *req.Prompt
	}
	if req.ScreenWorking != nil {
		next.ScreenWorking = *req.ScreenWorking
	}
	if req.ScreenWaiting != nil {
		next.ScreenWaiting = *req.ScreenWaiting
	}
	if req.ScreenQuestion != nil {
		next.ScreenQuestion = *req.ScreenQuestion
	}
	if next.Command == "" {
		httpError(w, http.StatusUnprocessableEntity, "command is required")
		return
	}
	rules, err := screen.Compile(next.ScreenWorking, next.ScreenWaiting, next.ScreenQuestion)
	if err != nil {
		httpError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if s.configPath == "" {
		httpError(w, http.StatusBadRequest, "global config path not configured")
		return
	}
	doc, err := os.ReadFile(s.configPath)
	if err != nil && !os.IsNotExist(err) {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out, err := config.SetProvider(doc, name, config.ProviderPatch{
		Command: req.Command, DefaultModel: req.DefaultModel, Prompt: req.Prompt,
		Working: req.ScreenWorking, Waiting: req.ScreenWaiting, Question: req.ScreenQuestion,
	})
	if err != nil {
		httpError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if err := writeFileAtomic(s.configPath, out); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.rulesMu.Lock()
	if s.cfg.Providers == nil {
		s.cfg.Providers = map[string]config.Provider{}
	}
	s.cfg.Providers[name] = next
	s.rulesMu.Unlock()
	if len(next.ScreenWorking)+len(next.ScreenWaiting)+len(next.ScreenQuestion) == 0 {
		s.setRules(name, nil)
	} else {
		s.setRules(name, &rules)
	}
	writeJSON(w, http.StatusOK, providerToJSON(name, next))
}
