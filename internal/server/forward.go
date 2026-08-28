package server

import (
	"net/http"

	"erbrus/internal/store"
)

func (s *Server) handleForward(w http.ResponseWriter, r *http.Request) {
	msgID := chiInt64(r, "id")
	var req struct {
		ChannelID int64 `json:"channel_id"`
	}
	if err := decodeBody(r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	mj, status, errMsg := s.forwardCore(msgID, req.ChannelID)
	if status != 0 {
		httpError(w, status, errMsg)
		return
	}
	writeJSON(w, http.StatusCreated, mj)
}

// forwardCore: everything handleForward does after decoding.
func (s *Server) forwardCore(msgID, channelID int64) (mj messageJSON, status int, errMsg string) {
	src, ok, err := s.st.MessageByID(msgID)
	if err != nil {
		return messageJSON{}, http.StatusInternalServerError, err.Error()
	}
	if !ok {
		return messageJSON{}, http.StatusNotFound, "message not found"
	}
	if _, ok, err := s.st.ChannelByID(channelID); err != nil {
		return messageJSON{}, http.StatusInternalServerError, err.Error()
	} else if !ok {
		return messageJSON{}, http.StatusNotFound, "target channel not found"
	}
	cp, err := s.st.CreateMessage(store.Message{
		ChannelID:       channelID,
		Kind:            src.Kind,
		AuthorKind:      src.AuthorKind,
		AuthorName:      src.AuthorName,
		OriginMessageID: src.ID,
		Format:          src.Format,
		Body:            src.Body,
	})
	if err != nil {
		return messageJSON{}, http.StatusInternalServerError, err.Error()
	}
	mj = s.messageJSON(cp)
	// Artifacts live with the origin; surface them on the copy.
	origin := s.messageJSON(src)
	mj.Artifacts = origin.Artifacts
	s.hub.Publish("message", mj)
	return mj, 0, ""
}
