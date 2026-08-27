package server

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"erbrus/internal/config"
	"erbrus/internal/preset"
	"erbrus/internal/store"
)

type msgView struct {
	ID        int64
	Kind      string
	Author    string
	Body      string
	When      string // localtime-rendered
	IsReport  bool
	IsSystem  bool
	Artifacts []store.Artifact
	Origin    string // "report from <project> / #<channel> by <author>", "" if not a forward or lookup failed
}

type runView struct {
	ID         int64
	AgentName  string
	Provider   string
	Status     string
	TmuxTarget string
	Running    bool
}

type channelPage struct {
	Channel   store.Channel
	Project   store.Project
	Sidebar   []projectCard
	Messages  []msgView
	Runs      []runView
	Presets   []string // merged preset names for THIS project, sorted
	AttachCmd string   // "tmux attach -t <session>" for this project, "" if none derivable
}

// buildChannelPage assembles a channelPage for chID: channel + project
// lookup (404 on either miss), sidebar via projectsPageData, messages with
// artifacts, runs, merged presets, and the derived attach command.
func (s *Server) buildChannelPage(chID int64) (channelPage, int, string) {
	channel, ok, err := s.st.ChannelByID(chID)
	if err != nil {
		return channelPage{}, http.StatusInternalServerError, err.Error()
	}
	if !ok {
		return channelPage{}, http.StatusNotFound, "channel not found"
	}
	project, ok, err := s.st.ProjectByID(channel.ProjectID)
	if err != nil {
		return channelPage{}, http.StatusInternalServerError, err.Error()
	}
	if !ok {
		return channelPage{}, http.StatusNotFound, "project not found"
	}
	sidebar, err := s.projectsPageData("")
	if err != nil {
		return channelPage{}, http.StatusInternalServerError, err.Error()
	}

	msgs, err := s.st.MessagesLatest(chID, 500)
	if err != nil {
		return channelPage{}, http.StatusInternalServerError, err.Error()
	}
	msgViews := make([]msgView, 0, len(msgs))
	for _, m := range msgs {
		arts, err := s.st.ArtifactsByMessage(m.ID)
		if err != nil {
			return channelPage{}, http.StatusInternalServerError, err.Error()
		}

		var origin string
		if m.OriginMessageID != 0 {
			if src, ok, err := s.st.MessageByID(m.OriginMessageID); err == nil && ok {
				if srcChan, ok, err := s.st.ChannelByID(src.ChannelID); err == nil && ok {
					if srcProj, ok, err := s.st.ProjectByID(srcChan.ProjectID); err == nil && ok {
						origin = fmt.Sprintf("%s from %s / #%s by %s", src.Kind, srcProj.Name, srcChan.Name, src.AuthorName)
					}
				}
			}
			if len(arts) == 0 {
				if originArts, err := s.st.ArtifactsByMessage(m.OriginMessageID); err == nil {
					arts = originArts
				}
			}
		}

		msgViews = append(msgViews, msgView{
			ID:        m.ID,
			Kind:      m.Kind,
			Author:    m.AuthorName,
			Body:      m.Body,
			When:      m.CreatedAt.Local().Format("Jan _2 15:04"),
			IsReport:  m.Kind == "report",
			IsSystem:  m.Kind == "system",
			Artifacts: arts,
			Origin:    origin,
		})
	}

	runs, err := s.st.RunsByChannel(chID)
	if err != nil {
		return channelPage{}, http.StatusInternalServerError, err.Error()
	}
	runViews := make([]runView, 0, len(runs))
	for _, r := range runs {
		runViews = append(runViews, runView{
			ID:         r.ID,
			AgentName:  r.AgentName,
			Provider:   r.Provider,
			Status:     r.Status,
			TmuxTarget: r.TmuxTarget,
			Running:    r.Status == "starting" || r.Status == "running",
		})
	}

	repoCfg, _, _ := config.LoadRepo(project.RepoPath)
	merged := preset.Merge(s.cfg.Presets, repoCfg.Presets)
	presets := make([]string, 0, len(merged))
	for name := range merged {
		presets = append(presets, name)
	}
	sort.Strings(presets)

	session := repoCfg.Session
	if session == "" {
		session = strings.ReplaceAll(s.cfg.SessionPattern, "{project}", project.Name)
	}
	if repoCfg.AttachSession != "" {
		session = repoCfg.AttachSession
	}
	attachCmd := ""
	if session != "" {
		attachCmd = "tmux attach -t " + session
	}

	return channelPage{
		Channel:   channel,
		Project:   project,
		Sidebar:   sidebar.Projects,
		Messages:  msgViews,
		Runs:      runViews,
		Presets:   presets,
		AttachCmd: attachCmd,
	}, 0, ""
}

func (s *Server) handleUIChannel(w http.ResponseWriter, r *http.Request) {
	chID := chiInt64(r, "id")
	data, status, errMsg := s.buildChannelPage(chID)
	if status != 0 {
		httpError(w, status, errMsg)
		return
	}
	s.render(w, "channel", data)
}

// renderChannelPartial executes name (either "_messages.html" or
// "_runs.html") from the channel page's template set, with no layout
// wrapper — the innerHTML of #messages / #runs, for SSE-triggered refresh.
func (s *Server) renderChannelPartial(w http.ResponseWriter, chID int64, name string) {
	data, status, errMsg := s.buildChannelPage(chID)
	if status != 0 {
		httpError(w, status, errMsg)
		return
	}
	tmpl, ok := s.pages["channel"]
	if !ok {
		httpError(w, http.StatusInternalServerError, "unknown page channel")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = tmpl.ExecuteTemplate(w, name, data)
}

func (s *Server) handleUIChannelStream(w http.ResponseWriter, r *http.Request) {
	s.renderChannelPartial(w, chiInt64(r, "id"), "_messages.html")
}

func (s *Server) handleUIRunsPanel(w http.ResponseWriter, r *http.Request) {
	s.renderChannelPartial(w, chiInt64(r, "id"), "_runs.html")
}

func (s *Server) handleUIComposeMessage(w http.ResponseWriter, r *http.Request) {
	chID := chiInt64(r, "id")
	if _, ok, err := s.st.ChannelByID(chID); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	} else if !ok {
		httpError(w, http.StatusNotFound, "channel not found")
		return
	}

	kind := r.FormValue("kind")
	if kind == "" {
		kind = "message"
	}
	if kind != "message" && kind != "report" {
		httpError(w, http.StatusUnprocessableEntity, "kind must be message|report")
		return
	}

	saved, err := s.st.CreateMessage(store.Message{
		ChannelID: chID, Kind: kind, AuthorKind: "human", AuthorName: "you", Body: r.FormValue("body"),
	})
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.hub.Publish("message", s.messageJSON(saved))
	http.Redirect(w, r, fmt.Sprintf("/ui/channels/%d", chID), http.StatusFound)
}

func (s *Server) handleUIStopRun(w http.ResponseWriter, r *http.Request) {
	id := chiInt64(r, "id")
	if _, status, errMsg := s.stopRunCore(id); status != 0 {
		httpError(w, status, errMsg)
		return
	}
	http.Redirect(w, r, "/ui/channels/"+r.FormValue("channel"), http.StatusFound)
}
