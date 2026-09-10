package server

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"erbrus/internal/integrate"
	"erbrus/internal/wt"
)

// targetOption is one entry of the forward dialog's target list: a channel
// ("c<id>", post only) or a running agent ("r<id>", post + deliver).
type targetOption struct {
	Value string
	Label string
}

type forwardPage struct {
	Source          msgView // source message excerpt
	SourceChannelID int64   // channel the source message lives in (for Cancel)
	Targets         []targetOption
	Error           string
}

// forwardTargets lists every channel except the source (post-only) plus
// every running agent anywhere — including the source channel: handing a
// memo to an agent sitting right here is the common case.
func (s *Server) forwardTargets(sourceChannel int64) ([]targetOption, error) {
	channels, err := s.allChannelOptions(sourceChannel)
	if err != nil {
		return nil, err
	}
	var opts []targetOption
	for _, c := range channels {
		opts = append(opts, targetOption{Value: fmt.Sprintf("c%d", c.ID), Label: c.Label + " (post only)"})
	}
	running, err := s.st.RunningRuns()
	if err != nil {
		return nil, err
	}
	for _, r := range running {
		if s.isToolRun(r) {
			continue // a shell takes no forwarded text
		}
		ch, ok, err := s.st.ChannelByID(r.ChannelID)
		if err != nil || !ok {
			continue
		}
		p, ok, err := s.st.ProjectByID(ch.ProjectID)
		if err != nil || !ok {
			continue
		}
		opts = append(opts, targetOption{Value: fmt.Sprintf("r%d", r.ID),
			Label: fmt.Sprintf("%s / # %s / → %s", p.Name, ch.Name, r.AgentName)})
	}
	return opts, nil
}

// buildForwardPage assembles the forward dialog for source message msgID.
func (s *Server) buildForwardPage(msgID int64) (forwardPage, int, string) {
	msg, ok, err := s.st.MessageByID(msgID)
	if err != nil {
		return forwardPage{}, http.StatusInternalServerError, err.Error()
	}
	if !ok {
		return forwardPage{}, http.StatusNotFound, "message not found"
	}
	arts, err := s.st.ArtifactsByMessage(msg.ID)
	if err != nil {
		return forwardPage{}, http.StatusInternalServerError, err.Error()
	}

	targets, err := s.forwardTargets(msg.ChannelID)
	if err != nil {
		return forwardPage{}, http.StatusInternalServerError, err.Error()
	}

	return forwardPage{
		Source: msgView{
			ID:        msg.ID,
			Kind:      msg.Kind,
			Author:    msg.AuthorName,
			Body:      truncateRunes(msg.Body, 200),
			IsReport:  msg.Kind == "report",
			IsSystem:  msg.Kind == "system",
			Artifacts: arts,
		},
		SourceChannelID: msg.ChannelID,
		Targets:         targets,
	}, 0, ""
}

func (s *Server) handleUIForward(w http.ResponseWriter, r *http.Request) {
	msgID, _ := strconv.ParseInt(r.URL.Query().Get("message"), 10, 64)
	page, status, errMsg := s.buildForwardPage(msgID)
	if status != 0 {
		httpError(w, status, errMsg)
		return
	}
	s.render(w, "forward", page)
}

func (s *Server) handleUIForwardPost(w http.ResponseWriter, r *http.Request) {
	msgID, _ := strconv.ParseInt(r.FormValue("message_id"), 10, 64)
	target := r.FormValue("target")
	if target == "" || (target[0] != 'c' && target[0] != 'r') {
		s.renderForwardError(w, msgID, "target must be c<channel id> or r<run id>")
		return
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(strings.TrimPrefix(target, "c"), "r"), 10, 64)
	if err != nil {
		s.renderForwardError(w, msgID, "target must be c<channel id> or r<run id>")
		return
	}

	var chID int64
	warning, queued := "", ""
	if target[0] == 'c' {
		chID = id
		if _, status, errMsg := s.forwardCore(msgID, chID); status != 0 {
			s.renderForwardError(w, msgID, errMsg)
			return
		}
	} else {
		var status int
		var errMsg string
		chID, warning, queued, status, errMsg = s.forwardToAgentCore(msgID, id)
		if status != 0 {
			s.renderForwardError(w, msgID, errMsg)
			return
		}
	}
	dest := fmt.Sprintf("/ui/channels/%d", chID)
	if warning != "" {
		dest += "?warning=" + url.QueryEscape(warning)
		if queued != "" {
			dest += "&queued=" + url.QueryEscape(queued)
		}
	}
	http.Redirect(w, r, dest, http.StatusFound)
}

// forwardToAgentCore forwards msgID to a RUNNING agent: the copy lands in
// the agent's channel via forwardCore (provenance, SSE), then the full
// context block — origin coordinates, body, artifact paths, reply routing
// back to the origin channel — is typed into the agent's terminal.
// Delivery failure is a warning; the copy is already posted. queued names
// the agent when the text was queued instead of typed (see sendToRun).
func (s *Server) forwardToAgentCore(msgID, runID int64) (chID int64, warning, queued string, status int, errMsg string) {
	run, ok, err := s.st.RunByID(runID)
	if err != nil {
		return 0, "", "", http.StatusInternalServerError, err.Error()
	}
	if !ok {
		return 0, "", "", http.StatusNotFound, "agent run not found"
	}
	if run.Status != "starting" && run.Status != "running" {
		return 0, "", "", http.StatusUnprocessableEntity, "agent already finished"
	}

	src, ok, err := s.st.MessageByID(msgID)
	if err != nil {
		return 0, "", "", http.StatusInternalServerError, err.Error()
	}
	if !ok {
		return 0, "", "", http.StatusNotFound, "message not found"
	}

	if _, status, errMsg := s.forwardCore(msgID, run.ChannelID); status != 0 {
		return 0, "", "", status, errMsg
	}

	// Origin coordinates for the context block; lookups degrade to empty
	// strings rather than failing a forward that is already posted.
	var projName, chanName, branch, worktree string
	if ch, ok, err := s.st.ChannelByID(src.ChannelID); err == nil && ok {
		chanName, branch, worktree = ch.Name, wt.ShortBranch(ch.Branch), ch.WorktreePath
		if p, ok, err := s.st.ProjectByID(ch.ProjectID); err == nil && ok {
			projName = p.Name
		}
	}
	arts, _ := s.st.ArtifactsByMessage(src.ID)
	paths := make([]string, 0, len(arts))
	for _, a := range arts {
		paths = append(paths, a.Path)
	}
	text := integrate.ForwardToAgent(s.erbrusBin, src.ChannelID, projName, chanName,
		branch, worktree, src.AuthorName, src.CreatedAt.Format("2006-01-02 15:04:05"), src.Body, paths)
	warning, q := s.sendToRun(run, text)
	if q {
		queued = run.AgentName
	}
	return run.ChannelID, warning, queued, 0, ""
}

// renderForwardError re-renders the forward dialog at 422 with errMsg.
func (s *Server) renderForwardError(w http.ResponseWriter, msgID int64, errMsg string) {
	page, status, buildErr := s.buildForwardPage(msgID)
	if status != 0 {
		httpError(w, status, buildErr)
		return
	}
	page.Error = errMsg
	tmpl, ok := s.pages["forward"]
	if !ok {
		httpError(w, http.StatusInternalServerError, "unknown page forward")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnprocessableEntity)
	_ = tmpl.ExecuteTemplate(w, "layout.html", page)
}
