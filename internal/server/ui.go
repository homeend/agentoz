package server

import (
	"net/http"
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
}

type projectsPage struct {
	Projects []projectCard
	Error    string
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
		page.Projects = append(page.Projects, projectCard{Project: p, Channels: chans})
	}
	return page, nil
}

func (s *Server) handleUIProjects(w http.ResponseWriter, r *http.Request) {
	data, err := s.projectsPageData("")
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.render(w, "projects", data)
}

func (s *Server) handleUIAddProject(w http.ResponseWriter, r *http.Request) {
	repoPath := r.FormValue("repo_path")
	if repoPath == "" {
		s.renderProjectsError(w, "repo_path is required")
		return
	}
	if _, _, _, status, errMsg := s.addProjectCore(repoPath); status != 0 {
		s.renderProjectsError(w, errMsg)
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
