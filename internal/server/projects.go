package server

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

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

	p, warning, created, status, errMsg := s.addProjectCore(req.RepoPath)
	if status != 0 {
		httpError(w, status, errMsg)
		return
	}

	pj, err := s.projectJSON(p, created, warning)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, pj)
}

// addProjectCore: everything handleAddProject does after decoding —
// absolutize+ExpandHome, lookup (500 on store error), create with the
// isNameCollision suffix loop, ensureChannels. created reports whether a
// new row was made; warning is ensureChannels' warning string.
func (s *Server) addProjectCore(repoPath string) (p store.Project, warning string, created bool, status int, errMsg string) {
	root, err := filepath.Abs(config.ExpandHome(config.TranslateUserPath(repoPath)))
	if err != nil {
		return store.Project{}, "", false, http.StatusBadRequest, err.Error()
	}

	if existing, ok, err := s.st.ProjectByPath(root); err != nil {
		return store.Project{}, "", false, http.StatusInternalServerError, err.Error()
	} else if ok {
		warning := s.ensureChannels(existing)
		return existing, warning, false, 0, ""
	}

	// A nonexistent directory is a hard error (nothing could ever run
	// there); a directory that merely isn't a git repo stays allowed —
	// detection failure is a warning and agents can still spawn in it.
	if fi, serr := os.Stat(root); serr != nil || !fi.IsDir() {
		return store.Project{}, "", false, http.StatusBadRequest,
			"path does not exist or is not a directory: " + root
	}

	base := filepath.Base(root)
	name := base
	for i := 2; ; i++ {
		var cerr error
		p, cerr = s.st.CreateProject(name, root)
		if cerr == nil {
			break
		}
		if !isNameCollision(cerr) || i > 9 {
			return store.Project{}, "", false, http.StatusInternalServerError, cerr.Error()
		}
		name = fmt.Sprintf("%s-%d", base, i)
	}

	warning = s.ensureChannels(p)
	return p, warning, true, 0, ""
}

// isNameCollision reports whether err is the projects.name UNIQUE
// constraint violation (as opposed to any other store error).
func isNameCollision(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed: projects.name")
}

// ensureChannels runs worktree detection for p and creates any missing
// channels. Returns a warning string ("" when clean).
func (s *Server) ensureChannels(p store.Project) string {
	warning := ""
	wts, err := wt.List(s.run, s.cfg.WtBin, p.RepoPath)
	if err != nil {
		// err already reads "worktree detection failed (wt and git): ..." —
		// don't double the prefix.
		warning = err.Error()
	}
	repoCfg, _, _ := config.LoadRepo(p.RepoPath)
	perWorktree := repoCfg.ChannelPerWorktree == nil || *repoCfg.ChannelPerWorktree

	addWarn := func(msg string) {
		if warning == "" {
			warning = msg
		} else {
			warning += "; " + msg
		}
	}
	ensure := func(name, path, branch string) {
		_, ok, err := s.st.ChannelByName(p.ID, name)
		if err != nil {
			addWarn(fmt.Sprintf("channel lookup %s failed: %s", name, err))
			return
		}
		if ok {
			return
		}
		if _, err := s.st.CreateChannel(p.ID, name, path, branch); err != nil {
			addWarn(fmt.Sprintf("failed to create channel %s: %s", name, err))
		}
	}
	ensure("general", p.RepoPath, mainBranch(wts))
	if perWorktree {
		for _, w2 := range wts {
			if w2.IsMain {
				continue
			}
			name := wt.ShortBranch(w2.Branch)
			if name == "" {
				name = filepath.Base(w2.Path)
			}
			ensure(name, w2.Path, w2.Branch)
		}
	}
	return warning
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
