package server

import (
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"erbrus/internal/store"
)

type artifactJSON struct {
	ID       int64  `json:"id"`
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
}

type messageJSON struct {
	ID              int64          `json:"id"`
	ChannelID       int64          `json:"channel_id"`
	Kind            string         `json:"kind"`
	AuthorKind      string         `json:"author_kind"`
	AuthorName      string         `json:"author_name"`
	OriginMessageID int64          `json:"origin_message_id,omitempty"`
	Body            string         `json:"body"`
	CreatedAt       string         `json:"created_at"`
	Artifacts       []artifactJSON `json:"artifacts,omitempty"`
}

func (s *Server) messageJSON(m store.Message) messageJSON {
	mj := messageJSON{
		ID: m.ID, ChannelID: m.ChannelID, Kind: m.Kind, AuthorKind: m.AuthorKind,
		AuthorName: m.AuthorName, OriginMessageID: m.OriginMessageID, Body: m.Body,
		CreatedAt: m.CreatedAt.Format("2006-01-02 15:04:05"),
	}
	arts, _ := s.st.ArtifactsByMessage(m.ID)
	for _, a := range arts {
		mj.Artifacts = append(mj.Artifacts, artifactJSON{ID: a.ID, Filename: a.Filename, Size: a.Size})
	}
	return mj
}

func decodeBody(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}

func chiInt64(r *http.Request, name string) int64 {
	n, _ := strconv.ParseInt(chi.URLParam(r, name), 10, 64)
	return n
}

func (s *Server) handleGetMessages(w http.ResponseWriter, r *http.Request) {
	chID := chiInt64(r, "id")
	if _, ok, _ := s.st.ChannelByID(chID); !ok {
		httpError(w, http.StatusNotFound, "channel not found")
		return
	}
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	msgs, err := s.st.MessagesSince(chID, since, limit)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := []messageJSON{}
	for _, m := range msgs {
		out = append(out, s.messageJSON(m))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handlePostMessage(w http.ResponseWriter, r *http.Request) {
	chID := chiInt64(r, "id")
	if _, ok, _ := s.st.ChannelByID(chID); !ok {
		httpError(w, http.StatusNotFound, "channel not found")
		return
	}

	run, isAgent, authOK := s.bearerRun(r)
	if !authOK {
		httpError(w, http.StatusUnauthorized, "invalid run token")
		return
	}

	var kind, body string
	var files []*multipart.FileHeader
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		if err := r.ParseMultipartForm(64 << 20); err != nil {
			httpError(w, http.StatusBadRequest, err.Error())
			return
		}
		kind = r.FormValue("kind")
		body = r.FormValue("body")
		if r.MultipartForm != nil {
			files = r.MultipartForm.File["file"]
		}
	} else {
		var req struct{ Kind, Body string }
		if err := decodeBody(r, &req); err != nil {
			httpError(w, http.StatusBadRequest, err.Error())
			return
		}
		kind, body = req.Kind, req.Body
	}
	if kind == "" {
		kind = "message"
	}
	if kind != "message" && kind != "report" && kind != "system" {
		httpError(w, http.StatusBadRequest, "kind must be message|report|system")
		return
	}

	m := store.Message{ChannelID: chID, Kind: kind, Body: body}
	if isAgent {
		m.AuthorKind, m.AuthorName, m.AgentRunID = "agent", run.AgentName, run.ID
	} else {
		m.AuthorKind, m.AuthorName = "human", "you"
	}
	if kind == "system" {
		m.AuthorKind = "system"
	}

	saved, err := s.st.CreateMessage(m)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, f := range files {
		if err := s.saveArtifact(saved.ID, f); err != nil {
			httpError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusCreated, s.messageJSON(saved))
}

func (s *Server) saveArtifact(messageID int64, fh *multipart.FileHeader) error {
	dir := filepath.Join(s.dataDir, "artifacts", fmt.Sprint(messageID))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	name := filepath.Base(fh.Filename)
	dst := filepath.Join(dir, name)
	src, err := fh.Open()
	if err != nil {
		return err
	}
	defer src.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	n, err := io.Copy(out, src)
	if err != nil {
		return err
	}
	_, err = s.st.AddArtifact(store.Artifact{MessageID: messageID, Filename: name, Path: dst, Size: n})
	return err
}
