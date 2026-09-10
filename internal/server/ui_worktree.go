package server

import (
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	"erbrus/internal/gg"
	"erbrus/internal/store"
)

// Worktree creation from the sidebar "+" (docs/superpowers/specs/
// 2026-09-10-worktree-create-design.md): gg does the git work on the happy
// path; a failure shows gg's line and offers a shell in the main repo.

type branchOption struct {
	Name       string
	CheckedOut string // directory basename when checked out somewhere
}

type worktreePage struct {
	Project   store.Project
	MainID    int64 // the project's main channel (Cancel link, shell button)
	Branches  []branchOption
	Current   string // HEAD branch of the main worktree (default base)
	Mode      string // "existing"|"new" (echo; default "new")
	Form      map[string]string
	Error     string
	ListError bool // branch listing failed: form hidden, terminal button shown
}

// branchNameRe: no whitespace, no leading dash (git would read a flag).
var branchNameRe = regexp.MustCompile(`^[^\s\-][^\s]*$`)

// projectAndMain resolves a project and its main channel (the one on the
// repo root itself).
func (s *Server) projectAndMain(id int64) (store.Project, int64, int, string) {
	p, ok, err := s.st.ProjectByID(id)
	if err != nil {
		return p, 0, http.StatusInternalServerError, err.Error()
	}
	if !ok {
		return p, 0, http.StatusNotFound, "project not found"
	}
	chans, err := s.st.ChannelsByProject(p.ID)
	if err != nil {
		return p, 0, http.StatusInternalServerError, err.Error()
	}
	for _, c := range chans {
		if c.WorktreePath == "" || c.WorktreePath == p.RepoPath {
			return p, c.ID, 0, ""
		}
	}
	return p, 0, http.StatusNotFound, "project has no main channel"
}

// buildWorktreePage lists branches through gg; a listing failure becomes
// the page's error with the form hidden (ListError).
func (s *Server) buildWorktreePage(p store.Project, mainID int64) worktreePage {
	page := worktreePage{Project: p, MainID: mainID, Mode: "new", Form: map[string]string{}}
	bin := s.ggBin()
	branches, err := gg.Branches(gg.Runner(s.run), bin, p.RepoPath)
	if err != nil {
		page.Error, page.ListError = err.Error(), true
		return page
	}
	wts, err := gg.Worktrees(gg.Runner(s.run), bin, p.RepoPath)
	if err != nil {
		page.Error, page.ListError = err.Error(), true
		return page
	}
	where := map[string]string{}
	for _, w := range wts {
		where[w.Branch] = filepath.Base(w.Path)
	}
	for _, b := range branches {
		page.Branches = append(page.Branches, branchOption{Name: b.Name, CheckedOut: where[b.Name]})
		if b.Current {
			page.Current = b.Name
		}
	}
	return page
}

// renderWorktree writes the page with an explicit status (422 on errors).
func (s *Server) renderWorktree(w http.ResponseWriter, status int, page worktreePage) {
	tmpl, ok := s.pages["worktree"]
	if !ok {
		httpError(w, http.StatusInternalServerError, "unknown page worktree")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = tmpl.ExecuteTemplate(w, "layout.html", page)
}

func (s *Server) handleUIWorktree(w http.ResponseWriter, r *http.Request) {
	p, mainID, status, errMsg := s.projectAndMain(chiInt64(r, "id"))
	if status != 0 {
		httpError(w, status, errMsg)
		return
	}
	s.renderWorktree(w, http.StatusOK, s.buildWorktreePage(p, mainID))
}

func (s *Server) handleUIWorktreePost(w http.ResponseWriter, r *http.Request) {
	p, mainID, status, errMsg := s.projectAndMain(chiInt64(r, "id"))
	if status != 0 {
		httpError(w, status, errMsg)
		return
	}
	mode := r.FormValue("mode")
	form := map[string]string{"name": r.FormValue("name"), "base": r.FormValue("base"), "branch": r.FormValue("branch")}
	fail := func(msg string) {
		page := s.buildWorktreePage(p, mainID)
		// The real failure outranks a listing error; the form stays hidden
		// when the lists are unavailable either way.
		page.Mode, page.Form, page.Error = mode, form, msg
		s.renderWorktree(w, http.StatusUnprocessableEntity, page)
	}
	var created gg.Created
	var err error
	bin := s.ggBin()
	switch mode {
	case "new":
		name, base := strings.TrimSpace(form["name"]), strings.TrimSpace(form["base"])
		if !branchNameRe.MatchString(name) || strings.Contains(name, "..") {
			fail("branch name is required and may not contain whitespace")
			return
		}
		if base == "" {
			fail("base branch is required")
			return
		}
		created, err = gg.AddFrom(gg.Runner(s.run), bin, p.RepoPath, base, name)
	case "existing":
		branch := strings.TrimSpace(form["branch"])
		if branch == "" {
			fail("pick a branch")
			return
		}
		if !branchNameRe.MatchString(branch) || strings.Contains(branch, "..") {
			fail("that is not a branch name") // "--help" must never reach gg as a flag
			return
		}
		created, err = gg.AddForBranch(gg.Runner(s.run), bin, p.RepoPath, branch)
	default:
		fail("mode must be new or existing")
		return
	}
	if err != nil {
		fail(err.Error())
		return
	}
	// The worktree exists now; erbrus never removes it. The sync creates
	// its channel (named by the branch's short name, as for any worktree).
	_, warning := s.syncChannels(p)
	if chans, lerr := s.st.ChannelsByProject(p.ID); lerr == nil {
		for _, c := range chans {
			if c.WorktreePath == created.Path {
				http.Redirect(w, r, fmt.Sprintf("/ui/channels/%d", c.ID), http.StatusFound)
				return
			}
		}
	}
	note := fmt.Sprintf("worktree created at %s, but no channel appeared for it", created.Path)
	if warning != "" {
		note += ": " + warning
	}
	http.Redirect(w, r, "/ui/projects?warning="+url.QueryEscape(note), http.StatusFound)
}
