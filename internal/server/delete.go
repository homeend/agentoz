package server

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"

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

// handleUIDeleteBatch removes the checked chat entries. Only messages of
// this channel are honored — a stale form can't delete across channels.
func (s *Server) handleUIDeleteBatch(w http.ResponseWriter, r *http.Request) {
	chID := chiInt64(r, "id")
	if _, ok, err := s.st.ChannelByID(chID); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	} else if !ok {
		httpError(w, http.StatusNotFound, "channel not found")
		return
	}
	if err := r.ParseForm(); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	for _, v := range r.PostForm["msg"] {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			continue
		}
		m, ok, err := s.st.MessageByID(id)
		if err != nil || !ok || m.ChannelID != chID {
			continue
		}
		if err := s.st.DeleteMessage(id); err != nil {
			httpError(w, http.StatusInternalServerError, err.Error())
			return
		}
		_ = os.RemoveAll(filepath.Join(s.dataDir, "artifacts", fmt.Sprint(id)))
	}
	http.Redirect(w, r, fmt.Sprintf("/ui/channels/%d", chID), http.StatusFound)
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

// deleteChannelCore removes an ARCHIVED channel: its messages, artifacts,
// runs and their directories. Live channels are refused (409) — they
// belong to a worktree that still exists; archive by re-sync first.
func (s *Server) deleteChannelCore(id int64) (warning string, status int, errMsg string) {
	ch, ok, err := s.st.ChannelByID(id)
	if err != nil {
		return "", http.StatusInternalServerError, err.Error()
	}
	if !ok {
		return "", http.StatusNotFound, "channel not found"
	}
	if !ch.Archived {
		return "", http.StatusConflict, "only archived channels can be deleted"
	}
	warn := func(msg string) {
		if warning == "" {
			warning = msg
		} else {
			warning += "; " + msg
		}
	}
	runs, err := s.st.RunsByChannel(id)
	if err != nil {
		return "", http.StatusInternalServerError, err.Error()
	}
	for _, r := range runs {
		if (r.Status != "starting" && r.Status != "running") || r.TmuxTarget == "" || s.spawner == nil {
			continue
		}
		if err := s.spawner.Stop(spawn.Handle(r.TmuxTarget)); err != nil {
			warn(fmt.Sprintf("stop of %s reported: %s", r.AgentName, err))
		}
	}
	artifactMsgIDs, runIDs, err := s.st.ChannelCleanupIDs(id)
	if err != nil {
		return "", http.StatusInternalServerError, err.Error()
	}
	if err := s.st.DeleteChannel(id); err != nil {
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

// handleUIDeleteChannel: the rail's "Delete channel" on an archived
// channel. Lands on the projects page (the channel is gone).
func (s *Server) handleUIDeleteChannel(w http.ResponseWriter, r *http.Request) {
	ch, ok, _ := s.st.ChannelByID(chiInt64(r, "id"))
	warning, status, errMsg := s.deleteChannelCore(chiInt64(r, "id"))
	if status != 0 {
		httpError(w, status, errMsg)
		return
	}
	note := "deleted archived channel #" + ch.Name
	if ok && warning != "" {
		note += " · " + warning
	}
	http.Redirect(w, r, "/ui/projects?warning="+url.QueryEscape(note), http.StatusFound)
}
