package server

import (
	"fmt"
	"net/http"
	"strconv"
)

type forwardPage struct {
	Source          msgView         // source message excerpt
	SourceChannelID int64           // channel the source message lives in (for Cancel)
	Channels        []channelOption // all channels, source's own channel excluded
	Error           string
}

// buildForwardPage assembles the forward dialog for source message msgID:
// its excerpt plus every channel except the one it lives in.
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

	channels, err := s.allChannelOptions(msg.ChannelID)
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
		Channels:        channels,
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
	targetID, _ := strconv.ParseInt(r.FormValue("target_channel"), 10, 64)

	if _, status, errMsg := s.forwardCore(msgID, targetID); status != 0 {
		s.renderForwardError(w, msgID, errMsg)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/ui/channels/%d", targetID), http.StatusFound)
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
