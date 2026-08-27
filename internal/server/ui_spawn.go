package server

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"

	"erbrus/internal/config"
	"erbrus/internal/preset"
	"erbrus/internal/store"
)

type channelOption struct {
	ID    int64
	Label string // "project / # channel"
}

type spawnPage struct {
	Channel  store.Channel   // dialog's default target
	Project  store.Project   // default target's project (whose presets are listed)
	Channels []channelOption // ALL channels across ALL projects, default first
	Presets  []string        // merged for the DEFAULT project (pinned limitation, help text says so)
	Preset   string          // preselected via ?preset=
	Origin   *msgView        // context banner when ?origin= given
	OriginID int64
	Error    string
	Form     map[string]string // echo-back on error
}

// truncateRunes cuts s to at most max runes, leaving it untouched if it
// already fits.
func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

// allChannelOptions enumerates every channel across every project as
// channelOption{ID, "project / # channel"}, in project/channel order,
// omitting excludeChannel (pass 0 to exclude nothing).
func (s *Server) allChannelOptions(excludeChannel int64) ([]channelOption, error) {
	projects, err := s.st.Projects()
	if err != nil {
		return nil, err
	}
	var opts []channelOption
	for _, p := range projects {
		chans, err := s.st.ChannelsByProject(p.ID)
		if err != nil {
			return nil, err
		}
		for _, c := range chans {
			if c.ID == excludeChannel {
				continue
			}
			opts = append(opts, channelOption{ID: c.ID, Label: p.Name + " / # " + c.Name})
		}
	}
	return opts, nil
}

// buildSpawnPage assembles the spawn dialog for default target channel
// chID, with the optional preselected preset name and origin message id.
func (s *Server) buildSpawnPage(chID int64, presetName string, originID int64) (spawnPage, int, string) {
	channel, ok, err := s.st.ChannelByID(chID)
	if err != nil {
		return spawnPage{}, http.StatusInternalServerError, err.Error()
	}
	if !ok {
		return spawnPage{}, http.StatusNotFound, "channel not found"
	}
	project, ok, err := s.st.ProjectByID(channel.ProjectID)
	if err != nil {
		return spawnPage{}, http.StatusInternalServerError, err.Error()
	}
	if !ok {
		return spawnPage{}, http.StatusNotFound, "project not found"
	}

	allOpts, err := s.allChannelOptions(0)
	if err != nil {
		return spawnPage{}, http.StatusInternalServerError, err.Error()
	}
	var defaultOpt *channelOption
	var rest []channelOption
	for _, opt := range allOpts {
		if opt.ID == chID {
			o := opt
			defaultOpt = &o
			continue
		}
		rest = append(rest, opt)
	}
	channels := make([]channelOption, 0, len(rest)+1)
	if defaultOpt != nil {
		channels = append(channels, *defaultOpt)
	}
	channels = append(channels, rest...)

	repoCfg, _, err := config.LoadRepo(project.RepoPath)
	if err != nil {
		return spawnPage{}, http.StatusInternalServerError, err.Error()
	}
	merged := preset.Merge(s.cfg.Presets, repoCfg.Presets)
	presets := make([]string, 0, len(merged))
	for name := range merged {
		presets = append(presets, name)
	}
	sort.Strings(presets)

	page := spawnPage{
		Channel:  channel,
		Project:  project,
		Channels: channels,
		Presets:  presets,
		Preset:   presetName,
		OriginID: originID,
	}

	if originID != 0 {
		msg, ok, err := s.st.MessageByID(originID)
		if err != nil {
			return spawnPage{}, http.StatusInternalServerError, err.Error()
		}
		if ok {
			arts, err := s.st.ArtifactsByMessage(msg.ID)
			if err != nil {
				return spawnPage{}, http.StatusInternalServerError, err.Error()
			}
			page.Origin = &msgView{
				ID:        msg.ID,
				Kind:      msg.Kind,
				Author:    msg.AuthorName,
				Body:      truncateRunes(msg.Body, 200),
				IsReport:  msg.Kind == "report",
				IsSystem:  msg.Kind == "system",
				Artifacts: arts,
			}
		}
	}

	return page, 0, ""
}

func (s *Server) handleUISpawn(w http.ResponseWriter, r *http.Request) {
	chID, _ := strconv.ParseInt(r.URL.Query().Get("channel"), 10, 64)
	presetName := r.URL.Query().Get("preset")
	originID, _ := strconv.ParseInt(r.URL.Query().Get("origin"), 10, 64)
	page, status, errMsg := s.buildSpawnPage(chID, presetName, originID)
	if status != 0 {
		httpError(w, status, errMsg)
		return
	}
	s.render(w, "spawn", page)
}

func (s *Server) handleUISpawnPost(w http.ResponseWriter, r *http.Request) {
	targetID, _ := strconv.ParseInt(r.FormValue("target_channel"), 10, 64)
	originID, _ := strconv.ParseInt(r.FormValue("origin_message_id"), 10, 64)
	req := runRequest{
		ChannelID:       targetID,
		Preset:          r.FormValue("preset"),
		Provider:        r.FormValue("provider"),
		Name:            r.FormValue("name"),
		Model:           r.FormValue("model"),
		Args:            r.FormValue("args"),
		Prompt:          r.FormValue("prompt"),
		Workdir:         r.FormValue("workdir"),
		Fg:              false,
		OriginMessageID: originID,
	}

	if _, status, errMsg := s.spawnRunCore(req); status != 0 {
		s.renderSpawnError(w, targetID, req, errMsg)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/ui/channels/%d", targetID), http.StatusFound)
}

// renderSpawnError re-renders the spawn dialog at 422 with errMsg and the
// submitted form values echoed back.
func (s *Server) renderSpawnError(w http.ResponseWriter, targetID int64, req runRequest, errMsg string) {
	page, status, buildErr := s.buildSpawnPage(targetID, req.Preset, req.OriginMessageID)
	if status != 0 {
		httpError(w, status, buildErr)
		return
	}
	page.Error = errMsg
	page.Form = map[string]string{
		"target_channel": fmt.Sprint(targetID),
		"preset":         req.Preset,
		"provider":       req.Provider,
		"name":           req.Name,
		"model":          req.Model,
		"args":           req.Args,
		"prompt":         req.Prompt,
		"workdir":        req.Workdir,
	}
	tmpl, ok := s.pages["spawn"]
	if !ok {
		httpError(w, http.StatusInternalServerError, "unknown page spawn")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnprocessableEntity)
	_ = tmpl.ExecuteTemplate(w, "layout.html", page)
}
