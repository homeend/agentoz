package server

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"

	"erbrus/internal/config"
	"erbrus/internal/screen"
	"erbrus/internal/spawn"
)

// screenRuleView is one provider's effective screen rules as shown on the
// settings page: pattern lists one per line, plus where they come from.
type screenRuleView struct {
	Provider string
	Source   string // "config override" | "built-in (claude-code)" | "built-in (generic)"
	Working  string
	Waiting  string
	Question string
}

type runOption struct {
	ID    int64
	Label string
}

// screenTestResult is what "Test against a running agent" renders.
type screenTestResult struct {
	Provider string
	RunLabel string
	State    string // "" reads as unknown
	Lines    []testLine
	Error    string
}

type testLine struct {
	Text  string
	Match string // "working" | "waiting" | "question" | ""
}

func (s *Server) screenRuleViews() []screenRuleView {
	names := make([]string, 0, len(s.cfg.Providers))
	for n := range s.cfg.Providers {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]screenRuleView, 0, len(names))
	for _, n := range names {
		v := screenRuleView{Provider: n}
		s.rulesMu.RLock()
		r, override := s.rules[n]
		s.rulesMu.RUnlock()
		switch {
		case override:
			v.Source = "config override"
		case screen.HasDefaults(n):
			v.Source = "built-in (" + n + ")"
			r = screen.DefaultRules(n)
		default:
			v.Source = "built-in (generic)"
			r = screen.DefaultRules(n)
		}
		v.Working, v.Waiting, v.Question = screen.Patterns(r.Working), screen.Patterns(r.Waiting), screen.Patterns(r.Question)
		out = append(out, v)
	}
	return out
}

func (s *Server) runOptions() ([]runOption, error) {
	runs, err := s.st.RunningRuns()
	if err != nil {
		return nil, err
	}
	var out []runOption
	for _, r := range runs {
		if r.TmuxTarget == "" {
			continue
		}
		label := fmt.Sprintf("%s (%s, %s)", r.AgentName, r.Provider, r.TmuxTarget)
		if ch, ok, _ := s.st.ChannelByID(r.ChannelID); ok {
			label = fmt.Sprintf("%s in #%s (%s, %s)", r.AgentName, ch.Name, r.Provider, r.TmuxTarget)
		}
		out = append(out, runOption{ID: r.ID, Label: label})
	}
	return out, nil
}

func splitLines(v string) []string {
	var out []string
	for _, l := range strings.Split(v, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// handleUISettingsScreen serves both buttons of a provider's screen-rules
// form: action=test classifies a running agent's screen with the (unsaved)
// patterns and re-renders; action=save validates, rewrites config.yaml,
// and applies the rules to the running watcher immediately.
func (s *Server) handleUISettingsScreen(w http.ResponseWriter, r *http.Request) {
	provider := r.FormValue("provider")
	if _, ok := s.cfg.Providers[provider]; !ok {
		httpError(w, http.StatusBadRequest, "unknown provider")
		return
	}
	working, waiting, question := splitLines(r.FormValue("working")), splitLines(r.FormValue("waiting")), splitLines(r.FormValue("question"))
	rules, err := screen.Compile(working, waiting, question)
	if err != nil {
		s.renderScreenRules(w, http.StatusUnprocessableEntity, provider, r, &screenTestResult{Provider: provider, Error: err.Error()})
		return
	}

	if r.FormValue("action") == "test" {
		res := &screenTestResult{Provider: provider}
		runID := chiInt64Form(r, "run")
		run, ok, _ := s.st.RunByID(runID)
		switch {
		case !ok || run.TmuxTarget == "":
			res.Error = "pick a running agent"
		case s.spawner == nil:
			res.Error = "no spawner configured"
		default:
			res.RunLabel = fmt.Sprintf("%s (%s)", run.AgentName, run.TmuxTarget)
			sc, err := s.spawner.Capture(spawn.Handle(run.TmuxTarget))
			if err != nil {
				res.Error = err.Error()
				break
			}
			lines := screen.Tail(screen.Strip(sc.Raw), 15)
			res.State = string(screen.Classify(rules, lines))
			for _, l := range lines {
				res.Lines = append(res.Lines, testLine{Text: l, Match: screen.MatchKind(rules, l)})
			}
		}
		s.renderScreenRules(w, http.StatusOK, provider, r, res)
		return
	}

	// Save: rewrite config.yaml (comments kept), then apply live.
	if s.configPath == "" {
		httpError(w, http.StatusBadRequest, "global config path not configured")
		return
	}
	doc, err := os.ReadFile(s.configPath)
	if err != nil && !os.IsNotExist(err) {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out, err := config.SetProviderScreenRules(doc, provider, working, waiting, question)
	if err != nil {
		s.renderScreenRules(w, http.StatusUnprocessableEntity, provider, r, &screenTestResult{Provider: provider, Error: err.Error()})
		return
	}
	if err := writeFileAtomic(s.configPath, out); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	p := s.cfg.Providers[provider]
	p.ScreenWorking, p.ScreenWaiting, p.ScreenQuestion = working, waiting, question
	s.cfg.Providers[provider] = p
	if len(working)+len(waiting)+len(question) == 0 {
		s.setRules(provider, nil)
	} else {
		s.setRules(provider, &rules)
	}
	http.Redirect(w, r, "/ui/settings?notice="+url.QueryEscape("screen rules for "+provider+" saved and applied (no restart needed)"), http.StatusFound)
}

// renderScreenRules re-renders the settings page with the submitted (not
// yet saved) patterns in the provider's form and a test result or error.
func (s *Server) renderScreenRules(w http.ResponseWriter, status int, provider string, r *http.Request, res *screenTestResult) {
	page, err := s.settingsPageData()
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for i := range page.ScreenRules {
		if page.ScreenRules[i].Provider == provider {
			page.ScreenRules[i].Working = r.FormValue("working")
			page.ScreenRules[i].Waiting = r.FormValue("waiting")
			page.ScreenRules[i].Question = r.FormValue("question")
		}
	}
	page.ScreenTest = res
	tmpl, ok := s.pages["settings"]
	if !ok {
		httpError(w, http.StatusInternalServerError, "unknown page settings")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = tmpl.ExecuteTemplate(w, "layout.html", page)
}
