package server

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"sort"

	"github.com/go-chi/chi/v5"

	"erbrus/internal/tools"
)

// Tools: programs the human opens in a channel's directory from the rail
// (spec docs/superpowers/specs/2026-09-10-tools-design.md). Terminal tools
// become tool runs in tmux and open in the web terminal; GUI tools are
// started detached and only a start error is reported.

type toolView struct {
	Name     string
	Terminal bool
	// Target: the tool row needs a browser-tab target (tool-run terminals
	// only). A driver that opens terminals itself (WezTerm) needs none —
	// its window shows up on its own, not as a tab in this page.
	Target bool
}

// driverOpensTerminals reports whether the driver opens terminal tools in
// a visible window of its own (WezTerm) rather than a tmux/browser-terminal
// tool run.
func (s *Server) driverOpensTerminals() bool {
	if s.driver == nil {
		return false
	}
	_, ok := s.driver.OpenTerminal("", nil)
	return ok
}

// toolViews lists the configured tools for the rail, sorted by name.
func (s *Server) toolViews() []toolView {
	driverOpens := s.driverOpensTerminals()
	var out []toolView
	for name, t := range s.toolsSnapshot() {
		out = append(out, toolView{Name: name, Terminal: t.Terminal, Target: t.Terminal && !driverOpens})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (s *Server) toolVars(dir string) tools.Vars {
	return tools.Vars{Dir: dir, GgBin: s.ggBin(), Distro: os.Getenv("WSL_DISTRO_NAME"), GOOS: runtime.GOOS}
}

// handleUIChannelTool opens tools.<name> in the channel's directory.
func (s *Server) handleUIChannelTool(w http.ResponseWriter, r *http.Request) {
	chID := chiInt64(r, "id")
	name := chi.URLParam(r, "name")
	channel, ok, err := s.st.ChannelByID(chID)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		httpError(w, http.StatusNotFound, "channel not found")
		return
	}
	if channel.Archived {
		httpError(w, http.StatusUnprocessableEntity, "archived channel: its directory is gone")
		return
	}
	tl, ok := s.toolCfg(name)
	if !ok {
		httpError(w, http.StatusNotFound, "unknown tool")
		return
	}
	back := fmt.Sprintf("/ui/channels/%d", chID)
	warn := func(msg string) {
		http.Redirect(w, r, back+"?warning="+url.QueryEscape(name+": "+msg), http.StatusFound)
	}
	project, _, err := s.st.ProjectByID(channel.ProjectID)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	dir := firstNonEmpty(channel.WorktreePath, project.RepoPath)

	if tl.Terminal {
		// A driver that opens terminals itself (WezTerm) launches the
		// tool detached in its own window, before ever considering a
		// tmux/browser-terminal tool run.
		if s.driver != nil {
			words, err := tools.Split(tl.Command)
			if err != nil {
				warn(err.Error())
				return
			}
			argv, err := tools.RenderArgv(name, words, s.toolVars(dir))
			if err != nil {
				warn(err.Error())
				return
			}
			if launch, ok := s.driver.OpenTerminal(dir, argv); ok {
				if s.launcher == nil {
					warn("no launcher configured")
					return
				}
				if err := s.launcher.Start(dir, launch); err != nil {
					warn(err.Error())
					return
				}
				http.Redirect(w, r, back, http.StatusFound)
				return
			}
		}
		if s.spawner == nil {
			warn("terminal tools need tmux (not available here)")
			return
		}
		payload, status, errMsg := s.spawnRunCore(runRequest{ChannelID: chID, Tool: name})
		if status != 0 {
			warn(errMsg)
			return
		}
		rj, _ := payload.(runJSON)
		http.Redirect(w, r, fmt.Sprintf("/ui/runs/%d/screen", rj.ID), http.StatusFound)
		return
	}

	// GUI tool: split first, render second (a directory with a space or a
	// UNC backslash path lands in one argument untouched), start detached.
	words, err := tools.Split(tl.Command)
	if err != nil {
		warn(err.Error())
		return
	}
	argv, err := tools.RenderArgv(name, words, s.toolVars(dir))
	if err != nil {
		warn(err.Error())
		return
	}
	if s.launcher == nil {
		warn("no launcher configured")
		return
	}
	if err := s.launcher.Start(dir, argv); err != nil {
		warn(err.Error())
		return
	}
	http.Redirect(w, r, back, http.StatusFound)
}
