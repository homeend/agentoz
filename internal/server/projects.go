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
	Archived     bool   `json:"archived,omitempty"`
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
	return channelJSON{ID: c.ID, ProjectID: c.ProjectID, Name: c.Name, WorktreePath: c.WorktreePath, Branch: wt.ShortBranch(c.Branch), Archived: c.Archived}
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
	_, warning := s.syncChannels(p)
	return warning
}

// syncChannels reconciles p's channels with its worktrees on disk:
//   - a worktree channel whose directory is gone is archived (tmux would
//     silently start agents in $HOME otherwise — observed live 2026-09-08);
//   - an archived channel whose directory is back is restored;
//   - a listed worktree with a known channel name but a new path repoints
//     the channel;
//   - new worktrees get channels (the original ensureChannels behavior).
//
// "general" (path == repo) is never archived; the project card flags a
// missing repo instead. summary reads like "archived: feat; created:
// hotfix" ("" when nothing changed); warning collects detection errors.
func (s *Server) syncChannels(p store.Project) (summary, warning string) {
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
	var archived, restored, created, moved []string

	// Pass 1: disk truth for existing worktree channels.
	existing, err := s.st.ChannelsByProject(p.ID)
	if err != nil {
		addWarn("channel listing failed: " + err.Error())
		return "", warning
	}
	// A missing repo root (unmounted drive, e.g. /mnt/t before WSL mounts
	// it) is not "every worktree was removed": never archive in that case.
	// The project card already flags the missing repo. Restores still run.
	repoPresent := dirExists(p.RepoPath)
	byName := map[string]store.Channel{}
	for _, c := range existing {
		byName[c.Name] = c
		if c.WorktreePath == "" || c.WorktreePath == p.RepoPath {
			continue
		}
		exists := dirExists(c.WorktreePath)
		switch {
		case !exists && !c.Archived && repoPresent:
			if err := s.st.SetChannelArchived(c.ID, true); err != nil {
				addWarn(fmt.Sprintf("archive %s: %s", c.Name, err))
				continue
			}
			c.Archived = true
			archived = append(archived, c.Name)
		case exists && c.Archived:
			if err := s.st.SetChannelArchived(c.ID, false); err != nil {
				addWarn(fmt.Sprintf("restore %s: %s", c.Name, err))
				continue
			}
			c.Archived = false
			restored = append(restored, c.Name)
		}
		byName[c.Name] = c
	}

	// Pass 2: what the worktree tool lists.
	ensure := func(name, path, branch string) {
		c, ok := byName[name]
		if !ok {
			nc, err := s.st.CreateChannel(p.ID, name, path, branch)
			if err != nil {
				addWarn(fmt.Sprintf("failed to create channel %s: %s", name, err))
				return
			}
			if path == p.RepoPath {
				return
			}
			// git still lists it but the directory is gone (prunable):
			// start it archived rather than live-then-archived next sync.
			if repoPresent && !dirExists(path) {
				if err := s.st.SetChannelArchived(nc.ID, true); err == nil {
					archived = append(archived, name)
					return
				}
			}
			created = append(created, name)
			return
		}
		if c.WorktreePath == p.RepoPath || path == c.WorktreePath {
			return
		}
		// Same branch name, new location: follow it.
		if err := s.st.SetChannelWorktree(c.ID, path, branch); err != nil {
			addWarn(fmt.Sprintf("repoint %s: %s", name, err))
			return
		}
		moved = append(moved, name)
		if c.Archived && dirExists(path) {
			if err := s.st.SetChannelArchived(c.ID, false); err == nil {
				restored = append(restored, name)
			}
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

	var parts []string
	for _, kv := range []struct {
		k string
		v []string
	}{{"archived", archived}, {"restored", restored}, {"moved", moved}, {"created", created}} {
		if len(kv.v) > 0 {
			parts = append(parts, kv.k+": "+strings.Join(kv.v, ", "))
		}
	}
	return strings.Join(parts, "; "), warning
}

func dirExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// SyncAllChannels runs syncChannels for every project; used at startup so
// channels for removed worktrees are archived before anyone spawns into
// them. Returns one line per project that changed or warned.
func (s *Server) SyncAllChannels() []string {
	projects, err := s.st.Projects()
	if err != nil {
		return []string{"sync: " + err.Error()}
	}
	var out []string
	for _, p := range projects {
		summary, warning := s.syncChannels(p)
		if summary != "" {
			out = append(out, p.Name+": "+summary)
		}
		if warning != "" {
			out = append(out, p.Name+": "+warning)
		}
	}
	return out
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
