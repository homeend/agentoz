package server

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"

	"erbrus/internal/spawn"
	"erbrus/internal/store"
)

// deleteProjectCore stops the project's active agents (best-effort), deletes
// the project and all dependent rows in one transaction, then removes the
// now-unreachable artifact and run directories under dataDir (best-effort;
// failures land in warning). status is 0 on success.
func (s *Server) deleteProjectCore(id int64) (warning string, status int, errMsg string) {
	_, ok, err := s.st.ProjectByID(id)
	if err != nil {
		return "", http.StatusInternalServerError, err.Error()
	}
	if !ok {
		return "", http.StatusNotFound, "project not found"
	}

	warn := func(msg string) {
		if warning == "" {
			warning = msg
		} else {
			warning += "; " + msg
		}
	}

	active, err := s.st.ActiveRunsByProject(id)
	if err != nil {
		return "", http.StatusInternalServerError, err.Error()
	}
	for _, r := range active {
		if r.TmuxTarget == "" || s.spawner == nil {
			continue
		}
		if err := s.spawner.Stop(spawn.Handle(r.TmuxTarget)); err != nil {
			warn(fmt.Sprintf("stop of %s reported: %s", r.AgentName, err))
		}
	}

	// Collect before the delete; the rows are gone afterwards. Directories
	// are derived from IDs, never from the stored path column.
	artifactMsgIDs, runIDs, err := s.st.ProjectCleanupIDs(id)
	if err != nil {
		return "", http.StatusInternalServerError, err.Error()
	}

	if err := s.st.DeleteProject(id); err != nil {
		return "", http.StatusInternalServerError, err.Error()
	}

	for _, msgID := range artifactMsgIDs {
		if err := os.RemoveAll(filepath.Join(s.dataDir, "artifacts", fmt.Sprint(msgID))); err != nil {
			warn(fmt.Sprintf("artifact cleanup: %s", err))
		}
	}
	for _, runID := range runIDs {
		if err := os.RemoveAll(filepath.Join(s.dataDir, "runs", fmt.Sprint(runID))); err != nil {
			warn(fmt.Sprintf("run cleanup: %s", err))
		}
	}
	return warning, 0, ""
}

func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	id := chiInt64(r, "id")
	warning, status, errMsg := s.deleteProjectCore(id)
	if status != 0 {
		httpError(w, status, errMsg)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "warning": warning})
}

// handleUIDeleteRun removes one finished run from the rail's history. The
// messages the run posted stay in the channel; its runs/<id> dir goes too.
func (s *Server) handleUIDeleteRun(w http.ResponseWriter, r *http.Request) {
	id := chiInt64(r, "id")
	run, ok, err := s.st.RunByID(id)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		httpError(w, http.StatusNotFound, "run not found")
		return
	}
	if run.Status == "starting" || run.Status == "running" {
		httpError(w, http.StatusUnprocessableEntity, "agent still running — stop it first")
		return
	}
	if err := s.st.DeleteRun(id); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = os.RemoveAll(filepath.Join(s.dataDir, "runs", fmt.Sprint(id)))
	http.Redirect(w, r, fmt.Sprintf("/ui/channels/%d", run.ChannelID), http.StatusFound)
}

// handleUIDeleteMessage removes one chat entry (and its artifact files).
// Forwarded copies elsewhere survive with their provenance link nulled.
func (s *Server) handleUIDeleteMessage(w http.ResponseWriter, r *http.Request) {
	id := chiInt64(r, "id")
	m, ok, err := s.st.MessageByID(id)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		httpError(w, http.StatusNotFound, "message not found")
		return
	}
	if err := s.st.DeleteMessage(id); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = os.RemoveAll(filepath.Join(s.dataDir, "artifacts", fmt.Sprint(id)))
	http.Redirect(w, r, fmt.Sprintf("/ui/channels/%d", m.ChannelID), http.StatusFound)
}

// handleUIClearChannel wipes a channel's chat history (messages + artifact
// files). Runs and the channel itself stay.
func (s *Server) handleUIClearChannel(w http.ResponseWriter, r *http.Request) {
	id := chiInt64(r, "id")
	if _, ok, err := s.st.ChannelByID(id); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	} else if !ok {
		httpError(w, http.StatusNotFound, "channel not found")
		return
	}
	artifactMsgIDs, err := s.st.ChannelArtifactMessageIDs(id)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.st.ClearChannel(id); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, msgID := range artifactMsgIDs {
		_ = os.RemoveAll(filepath.Join(s.dataDir, "artifacts", fmt.Sprint(msgID)))
	}
	http.Redirect(w, r, fmt.Sprintf("/ui/channels/%d", id), http.StatusFound)
}

type deletePage struct {
	Project store.Project
	Stats   store.ProjectStatsRow
}

// handleUIDeleteConfirm renders the confirmation page: what the delete
// removes, with a highlighted warning when agents are still running.
func (s *Server) handleUIDeleteConfirm(w http.ResponseWriter, r *http.Request) {
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
	stats, err := s.st.ProjectStats(id)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.render(w, "project_delete", deletePage{Project: p, Stats: stats})
}

func (s *Server) handleUIDeleteProject(w http.ResponseWriter, r *http.Request) {
	id := chiInt64(r, "id")
	warning, status, errMsg := s.deleteProjectCore(id)
	if status != 0 {
		httpError(w, status, errMsg)
		return
	}
	target := "/ui/projects"
	if warning != "" {
		target += "?warning=" + url.QueryEscape(warning)
	}
	http.Redirect(w, r, target, http.StatusFound)
}
