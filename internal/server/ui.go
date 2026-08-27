package server

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"erbrus/internal/store"
)

// localTime renders a DB UTC timestamp string in the server's local zone.
func localTime(dbTime string) string {
	t, err := time.ParseInLocation("2006-01-02 15:04:05", dbTime, time.UTC)
	if err != nil {
		return dbTime
	}
	return t.Local().Format("Jan _2 15:04")
}

func (s *Server) render(w http.ResponseWriter, page string, data any) {
	tmpl, ok := s.pages[page]
	if !ok {
		httpError(w, http.StatusInternalServerError, "unknown page "+page)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, "layout.html", data); err != nil {
		// headers are gone; best effort
		_ = err
	}
}

type projectCard struct {
	Project  store.Project
	Channels []store.Channel
	// PathMissing: the repo path no longer exists on disk. NoGit: the path
	// exists but holds no git repo — agents can still spawn there, and the
	// card offers an "Init git" button.
	PathMissing bool
	NoGit       bool
}

type projectsPage struct {
	Projects []projectCard
	Error    string
	Warning  string
}

func (s *Server) projectsPageData(errMsg string) (projectsPage, error) {
	list, err := s.st.Projects()
	if err != nil {
		return projectsPage{}, err
	}
	page := projectsPage{Error: errMsg}
	for _, p := range list {
		chans, err := s.st.ChannelsByProject(p.ID)
		if err != nil {
			return projectsPage{}, err
		}
		card := projectCard{Project: p, Channels: chans}
		if fi, err := os.Stat(p.RepoPath); err != nil || !fi.IsDir() {
			card.PathMissing = true
		} else if _, err := os.Stat(filepath.Join(p.RepoPath, ".git")); err != nil {
			// .git is a dir in a main checkout and a file in a linked
			// worktree; either satisfies the Stat.
			card.NoGit = true
		}
		page.Projects = append(page.Projects, card)
	}
	return page, nil
}

// handleUIGitInit runs `git init -b main` in a project's directory and
// refreshes its channels. Shown on cards whose path holds no git repo.
func (s *Server) handleUIGitInit(w http.ResponseWriter, r *http.Request) {
	id := chiInt64(r, "id")
	p, ok, err := s.st.ProjectByID(id)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		httpError(w, http.StatusNotFound, "project not found")
		return
	}
	if _, err := s.run(p.RepoPath, "git", "init", "-b", "main"); err != nil {
		http.Redirect(w, r, "/ui/projects?warning="+url.QueryEscape("git init failed: "+err.Error()), http.StatusFound)
		return
	}
	target := "/ui/projects"
	if warning := s.ensureChannels(p); warning != "" {
		target += "?warning=" + url.QueryEscape(warning)
	}
	http.Redirect(w, r, target, http.StatusFound)
}

func (s *Server) handleUIProjects(w http.ResponseWriter, r *http.Request) {
	data, err := s.projectsPageData("")
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	data.Warning = r.URL.Query().Get("warning")
	s.render(w, "projects", data)
}

func (s *Server) handleUIAddProject(w http.ResponseWriter, r *http.Request) {
	repoPath := r.FormValue("repo_path")
	if repoPath == "" {
		s.renderProjectsError(w, "repo_path is required")
		return
	}
	_, warning, _, status, errMsg := s.addProjectCore(repoPath)
	if status != 0 {
		s.renderProjectsError(w, errMsg)
		return
	}
	if warning != "" {
		http.Redirect(w, r, "/ui/projects?warning="+url.QueryEscape(warning), http.StatusFound)
		return
	}
	http.Redirect(w, r, "/ui/projects", http.StatusFound)
}

func (s *Server) renderProjectsError(w http.ResponseWriter, msg string) {
	data, err := s.projectsPageData(msg)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	tmpl, ok := s.pages["projects"]
	if !ok {
		httpError(w, http.StatusInternalServerError, "unknown page projects")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnprocessableEntity)
	_ = tmpl.ExecuteTemplate(w, "layout.html", data)
}
