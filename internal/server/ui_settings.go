package server

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"gopkg.in/yaml.v3"

	"erbrus/internal/config"
)

// repoScaffoldYAML mirrors the starter template `erbrus init` writes
// (internal/cli/initcmd.go's scaffoldYAML). Duplicated here on purpose —
// the server package must not import the cli package.
const repoScaffoldYAML = `# erbrus per-repo settings. Everything is optional; global config fills gaps.
# session: erbrus-myproject
# attach_session: ""
# channel_per_worktree: true
# presets:
#   claude:
#     provider: claude-code
`

// globalScaffoldYAML is shown when no global config file exists yet. Mirrors
// the example from docs/superpowers/specs/2026-08-26-erbrus-design.md.
const globalScaffoldYAML = `# erbrus global settings. Everything is optional; built-in defaults fill gaps.
# port: 7420
# data_dir: ~/.local/share/erbrus      # optional override
# terminal: tmux                        # default spawner
# session_pattern: "erbrus-{project}"
# wt_bin: wt                            # path to wt binary; auto-detected on PATH
#
# providers:
#   claude-code:
#     command: 'claude --model {model} {args} "{prompt}"'
#     default_model: fable-5
#   codex:
#     command: 'codex {args} "{prompt}"'
#   junie:
#     command: 'junie {args} "{prompt}"'
#   kimi:
#     command: 'kimi --model {model} {args} "{prompt}"'
#
# presets:
#   claude:
#     provider: claude-code
#     model: fable-5
#   claude-f:
#     provider: claude-code
#   codex:
#     provider: codex
#     prompt: "just some text to have string here"
#   kimi:
#     provider: kimi
#     model: k3
#     args: "--max-turns 30"
`

// writeFileAtomic writes data to path via a temp file in the same directory
// plus rename, so readers never observe a partial write.
func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".erbrus-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

type repoSettingsSection struct {
	ProjectID   int64
	ProjectName string
	Path        string
	Content     string
}

type settingsPage struct {
	ConfigPath     string
	GlobalReadOnly bool
	GlobalContent  string
	Projects       []repoSettingsSection
	Error          string
	Notice         string
	// Screen rules: one editable card per configured provider, plus the
	// running agents a rule set can be tested against.
	ScreenRules []screenRuleView
	RunOptions  []runOption
	ScreenTest  *screenTestResult
}

// readOrScaffold returns the raw file contents at path, or starter if the
// file does not exist.
func readOrScaffold(path, starter string) (string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return starter, nil
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (s *Server) settingsPageData() (settingsPage, error) {
	page := settingsPage{ConfigPath: s.configPath}
	if s.configPath == "" {
		page.GlobalReadOnly = true
	} else {
		content, err := readOrScaffold(s.configPath, globalScaffoldYAML)
		if err != nil {
			return settingsPage{}, err
		}
		page.GlobalContent = content
	}

	page.ScreenRules = s.screenRuleViews()
	page.RunOptions, _ = s.runOptions()

	projects, err := s.st.Projects()
	if err != nil {
		return settingsPage{}, err
	}
	for _, p := range projects {
		path := config.RepoConfigPath(p.RepoPath)
		content, err := readOrScaffold(path, repoScaffoldYAML)
		if err != nil {
			return settingsPage{}, err
		}
		page.Projects = append(page.Projects, repoSettingsSection{
			ProjectID:   p.ID,
			ProjectName: p.Name,
			Path:        path,
			Content:     content,
		})
	}
	return page, nil
}

func (s *Server) handleUISettings(w http.ResponseWriter, r *http.Request) {
	page, err := s.settingsPageData()
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	page.Notice = r.URL.Query().Get("notice")
	s.render(w, "settings", page)
}

// renderSettingsError re-renders the settings page at 422, preserving the
// submitted (invalid) content in place of the section that failed to
// validate, plus the error message.
func (s *Server) renderSettingsError(w http.ResponseWriter, errMsg string, global bool, projectID int64, submitted string) {
	page, err := s.settingsPageData()
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	page.Error = errMsg
	if global {
		page.GlobalContent = submitted
	} else {
		for i := range page.Projects {
			if page.Projects[i].ProjectID == projectID {
				page.Projects[i].Content = submitted
			}
		}
	}
	tmpl, ok := s.pages["settings"]
	if !ok {
		httpError(w, http.StatusInternalServerError, "unknown page settings")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnprocessableEntity)
	_ = tmpl.ExecuteTemplate(w, "layout.html", page)
}

func (s *Server) handleUISettingsGlobal(w http.ResponseWriter, r *http.Request) {
	content := r.FormValue("content")
	if s.configPath == "" {
		httpError(w, http.StatusBadRequest, "global config path not configured")
		return
	}

	g := config.Defaults()
	if err := yaml.Unmarshal([]byte(content), &g); err != nil {
		s.renderSettingsError(w, fmt.Sprintf("yaml parse error: %v", err), true, 0, content)
		return
	}

	if err := writeFileAtomic(s.configPath, []byte(content)); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	http.Redirect(w, r, "/ui/settings", http.StatusFound)
}

func (s *Server) handleUISettingsRepo(w http.ResponseWriter, r *http.Request) {
	projectID, err := strconv.ParseInt(r.URL.Query().Get("project"), 10, 64)
	if err != nil {
		httpError(w, http.StatusBadRequest, "invalid project id")
		return
	}
	p, ok, err := s.st.ProjectByID(projectID)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		httpError(w, http.StatusNotFound, "project not found")
		return
	}

	content := r.FormValue("content")
	var repo config.Repo
	if err := yaml.Unmarshal([]byte(content), &repo); err != nil {
		s.renderSettingsError(w, fmt.Sprintf("yaml parse error: %v", err), false, projectID, content)
		return
	}

	path := config.RepoConfigPath(p.RepoPath)
	if err := writeFileAtomic(path, []byte(content)); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	http.Redirect(w, r, "/ui/settings", http.StatusFound)
}
