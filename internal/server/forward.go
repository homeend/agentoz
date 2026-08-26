package server

import (
	"net/http"

	"erbrus/internal/store"
)

func (s *Server) handleForward(w http.ResponseWriter, r *http.Request) {
	msgID := chiInt64(r, "id")
	src, ok, err := s.st.MessageByID(msgID)
	if err != nil || !ok {
		httpError(w, http.StatusNotFound, "message not found")
		return
	}
	var req struct {
		ChannelID int64 `json:"channel_id"`
	}
	if err := decodeBody(r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, ok, _ := s.st.ChannelByID(req.ChannelID); !ok {
		httpError(w, http.StatusNotFound, "target channel not found")
		return
	}
	cp, err := s.st.CreateMessage(store.Message{
		ChannelID:       req.ChannelID,
		Kind:            src.Kind,
		AuthorKind:      src.AuthorKind,
		AuthorName:      src.AuthorName,
		OriginMessageID: src.ID,
		Body:            src.Body,
	})
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	mj := s.messageJSON(cp)
	// Artifacts live with the origin; surface them on the copy.
	origin := s.messageJSON(src)
	mj.Artifacts = origin.Artifacts
	s.hub.Publish("message", mj)
	writeJSON(w, http.StatusCreated, mj)
}
