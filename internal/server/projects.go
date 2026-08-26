package server

import (
	"fmt"
	"net/http"
	"path/filepath"

	"erbrus/internal/config"
	"erbrus/internal/store"
	"erbrus/internal/wt"
)

type channelJSON struct {
	ID           int64  `json:"id"`
	ProjectID    int64  `json:"project_id"`
	Name         string `json:"name"`
	WorktreePath string `json:"worktree_path"`
	Branch       string `json:"branch"`
}

type projectJSON struct {
	ID       int64         `json:"id"`
	Name     string        `json:"name"`
	RepoPath string        `json:"repo_path"`
	Channels []channelJSON `json:"channels"`
	Created  bool          `json:"created"`
	Warning  string        `json:"warning,omitempty"`
}

func toChannelJSON(c store.Channel) channelJSON {
	return channelJSON{ID: c.ID, ProjectID: c.ProjectID, Name: c.Name, WorktreePath: c.WorktreePath, Branch: wt.ShortBranch(c.Branch)}
}

func (s *Server) projectJSON(p store.Project, created bool, warning string) (projectJSON, error) {
	chans, err := s.st.ChannelsByProject(p.ID)
	if err != nil {
		return projectJSON{}, err
	}
	pj := projectJSON{ID: p.ID, Name: p.Name, RepoPath: p.RepoPath, Created: created, Warning: warning, Channels: []channelJSON{}}
	for _, c := range chans {
		pj.Channels = append(pj.Channels, toChannelJSON(c))
	}
	return pj, nil
}

func (s *Server) handleAddProject(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RepoPath string `json:"repo_path"`
	}
	if err := decodeBody(r, &req); err != nil || req.RepoPath == "" {
		httpError(w, http.StatusBadRequest, "repo_path is required")
		return
	}
	root, err := filepath.Abs(config.ExpandHome(req.RepoPath))
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}

	if p, ok, err := s.st.ProjectByPath(root); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	} else if ok {
		pj, err := s.projectJSON(p, false, "")
		if err != nil {
			httpError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, pj)
		return
	}

	warning := ""
	wts, err := wt.List(s.run, s.cfg.WtBin, root)
	if err != nil {
		warning = "worktree detection failed: " + err.Error()
	}

	p, err := s.st.CreateProject(filepath.Base(root), root)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}

	repoCfg, _ := config.LoadRepo(root)
	perWorktree := repoCfg.ChannelPerWorktree == nil || *repoCfg.ChannelPerWorktree

	if _, err := s.st.CreateChannel(p.ID, "general", root, mainBranch(wts)); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if perWorktree {
		for _, w2 := range wts {
			if w2.IsMain {
				continue
			}
			name := wt.ShortBranch(w2.Branch)
			if name == "" {
				name = filepath.Base(w2.Path)
			}
			if _, err := s.st.CreateChannel(p.ID, name, w2.Path, w2.Branch); err != nil {
				msg := fmt.Sprintf("failed to create channel %s: %s", name, err.Error())
				if warning == "" {
					warning = msg
				} else {
					warning = warning + "; " + msg
				}
			}
		}
	}

	pj, err := s.projectJSON(p, true, warning)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, pj)
}

func mainBranch(wts []wt.Worktree) string {
	for _, w := range wts {
		if w.IsMain {
			return w.Branch
		}
	}
	return ""
}

func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.st.Projects()
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := []projectJSON{}
	for _, p := range projects {
		pj, err := s.projectJSON(p, false, "")
		if err != nil {
			httpError(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, pj)
	}
	writeJSON(w, http.StatusOK, out)
}
