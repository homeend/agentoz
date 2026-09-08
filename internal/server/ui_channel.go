package server

import (
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"erbrus/internal/config"
	"erbrus/internal/integrate"
	"erbrus/internal/preset"
	"erbrus/internal/spawn"
	"erbrus/internal/store"
)

type msgView struct {
	ID         int64
	Kind       string
	Author     string
	AuthorKind string // human | agent | system — picks the avatar color
	Body       string
	When       string // localtime-rendered
	IsReport   bool
	IsSystem   bool
	Artifacts  []store.Artifact
	Origin     string // "report from <project> / #<channel> by <author>", "" if not a forward or lookup failed
	Target     string // agent name this message was typed at, "" for plain messages
	// BodyHTML is set (and shown instead of Body) for format=md messages.
	BodyHTML template.HTML
	// InlineDocs are attached .md files rendered into the chat.
	InlineDocs []template.HTML
}

type runView struct {
	ID         int64
	AgentName  string
	Provider   string
	Status     string
	TmuxTarget string
	Running    bool
	Started    string // localtime spawn moment
	Finished   string // localtime finish moment, "" while running
	HasExit    bool
	ExitCode   int64
	// StateLabel/StateClass: the watcher's badge ("working 7m",
	// "needs input"); empty until the first classification.
	StateLabel string
	StateClass string
	// StateName/Stalled drive the card's dot color (green working, blue
	// waiting, amber question/stalled) so the process status "running"
	// never reads as "busy".
	StateName string
	Stalled   bool
}

type channelPage struct {
	Channel  store.Channel
	Project  store.Project
	Sidebar  []projectCard
	Messages []msgView
	Runs     []runView
	Presets  []string // merged preset names for THIS project, sorted
	// AttachCmds: one "tmux attach -t <session>" per session actually
	// holding this channel's listed runs; falls back to the project's
	// configured session when no run names one.
	AttachCmds []string
	Warning    string // ?warning= from a redirect (e.g. failed agent delivery)
	// Workdir: where agents spawned from this channel land (same rule as
	// spawnRunCore's default: worktree path, else repo path).
	// PathMissing: that directory does not exist right now.
	Workdir     string
	PathMissing bool
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

		mv := msgView{
			ID:         m.ID,
			Kind:       m.Kind,
			Author:     m.AuthorName,
			AuthorKind: m.AuthorKind,
			Body:       m.Body,
			When:       m.CreatedAt.Local().Format("Jan _2 15:04"),
			IsReport:   m.Kind == "report",
			IsSystem:   m.Kind == "system",
			Artifacts:  arts,
			Origin:     origin,
			Target:     m.TargetLabel,
		}
		if m.Format == "md" {
			mv.BodyHTML = renderMarkdown(m.Body)
		}
		for _, a := range arts {
			if !strings.HasSuffix(strings.ToLower(a.Filename), ".md") || a.Size > mdInlineCap {
				continue
			}
			if data, err := os.ReadFile(a.Path); err == nil {
				mv.InlineDocs = append(mv.InlineDocs, renderMarkdown(string(data)))
			}
		}
		msgViews = append(msgViews, mv)
	}

	runs, err := s.st.RunsByChannel(chID)
	if err != nil {
		return channelPage{}, http.StatusInternalServerError, err.Error()
	}
	// Active runs first, then only the newest few finished ones — the full
	// history stays in the DB, the rail is a status panel, not an archive.
	const recentFinished = 10
	toView := func(r store.AgentRun) runView {
		v := runView{
			ID:         r.ID,
			AgentName:  r.AgentName,
			Provider:   r.Provider,
			Status:     r.Status,
			TmuxTarget: r.TmuxTarget,
			Running:    r.Status == "starting" || r.Status == "running",
			Started:    r.CreatedAt.Local().Format("Jan _2 15:04"),
			HasExit:    r.HasExit,
			ExitCode:   r.ExitCode,
		}
		if !v.Running && !r.FinishedAt.IsZero() {
			v.Finished = r.FinishedAt.Local().Format("Jan _2 15:04")
		}
		if v.Running {
			if rs, ok := s.stateOf(r.ID); ok {
				v.StateLabel, v.StateClass = rs.badge(time.Now())
				v.StateName, v.Stalled = string(rs.State), rs.Stalled
			}
		}
		return v
	}
	var runViews []runView
	for _, r := range runs {
		if r.Status == "starting" || r.Status == "running" {
			runViews = append(runViews, toView(r))
		}
	}
	finished := 0
	for i := len(runs) - 1; i >= 0 && finished < recentFinished; i-- {
		if runs[i].Status == "starting" || runs[i].Status == "running" {
			continue
		}
		runViews = append(runViews, toView(runs[i]))
		finished++
	}

	repoCfg, _, _ := config.LoadRepo(project.RepoPath)
	merged := preset.Merge(s.cfg.Presets, repoCfg.Presets)
	presets := make([]string, 0, len(merged))
	for name := range merged {
		presets = append(presets, name)
	}
	sort.Strings(presets)

	var attachCmds []string
	seen := map[string]bool{}
	for _, v := range runViews {
		sess, _, ok := strings.Cut(v.TmuxTarget, ":")
		if !ok || sess == "" || seen[sess] {
			continue
		}
		seen[sess] = true
		attachCmds = append(attachCmds, "tmux attach -t "+sess)
	}
	if len(attachCmds) == 0 {
		session := repoCfg.Session
		if session == "" {
			session = strings.ReplaceAll(s.cfg.SessionPattern, "{project}", project.Name)
		}
		if repoCfg.AttachSession != "" {
			session = repoCfg.AttachSession
		}
		if session != "" {
			attachCmds = []string{"tmux attach -t " + session}
		}
	}

	return channelPage{
		Channel:     channel,
		Project:     project,
		Workdir:     firstNonEmpty(channel.WorktreePath, project.RepoPath),
		PathMissing: !dirExists(firstNonEmpty(channel.WorktreePath, project.RepoPath)),
		Sidebar:     sidebar.Projects,
		Messages:    msgViews,
		Runs:        runViews,
		Presets:     presets,
		AttachCmds:  attachCmds,
	}, 0, ""
}

func (s *Server) handleUIChannel(w http.ResponseWriter, r *http.Request) {
	chID := chiInt64(r, "id")
	// Viewing a channel reads it; before the sidebar is built, so this
	// channel's own badge is already clear.
	if err := s.st.MarkChannelRead(chID); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	data, status, errMsg := s.buildChannelPage(chID)
	if status != 0 {
		httpError(w, status, errMsg)
		return
	}
	data.Warning = r.URL.Query().Get("warning")
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
	chID := chiInt64(r, "id")
	// The stream partial is fetched by the open channel view on every SSE
	// message event — messages arriving while the user watches never count
	// as unread.
	if err := s.st.MarkChannelRead(chID); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.renderChannelPartial(w, chID, "_messages.html")
}

func (s *Server) handleUIRunsPanel(w http.ResponseWriter, r *http.Request) {
	s.renderChannelPartial(w, chiInt64(r, "id"), "_runs.html")
}

// handleUIComposeMessage posts from the channel composer. target is either
// "memo" (or empty: just record the message in the channel) or "r<run id>":
// record it labeled with the agent's name AND type it into that agent's
// terminal. Delivery failure is a warning — the channel history always
// keeps the message.
func (s *Server) handleUIComposeMessage(w http.ResponseWriter, r *http.Request) {
	chID := chiInt64(r, "id")
	if _, ok, err := s.st.ChannelByID(chID); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	} else if !ok {
		httpError(w, http.StatusNotFound, "channel not found")
		return
	}

	msg := store.Message{ChannelID: chID, Kind: "message", AuthorKind: "human",
		AuthorName: "you", Body: r.FormValue("body")}

	var run store.AgentRun
	if target := r.FormValue("target"); target != "" && target != "memo" {
		runID, perr := strconv.ParseInt(strings.TrimPrefix(target, "r"), 10, 64)
		if !strings.HasPrefix(target, "r") || perr != nil {
			httpError(w, http.StatusUnprocessableEntity, "target must be memo or r<run id>")
			return
		}
		var ok bool
		var err error
		run, ok, err = s.st.RunByID(runID)
		if err != nil {
			httpError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !ok || run.ChannelID != chID {
			httpError(w, http.StatusUnprocessableEntity, "agent is not in this channel")
			return
		}
		if run.Status != "starting" && run.Status != "running" {
			httpError(w, http.StatusUnprocessableEntity, "agent already finished")
			return
		}
		msg.TargetLabel = run.AgentName
	}

	saved, err := s.st.CreateMessage(msg)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// One's own message is never unread — mark here, not only via the
	// redirect's page render, so a future fetch-based composer stays correct.
	if err := s.st.MarkChannelRead(chID); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.hub.Publish("message", s.messageJSON(saved))

	warning := ""
	if msg.TargetLabel != "" {
		warning = s.sendToRun(run, msg.Body+integrate.ChatReplySuffix(s.erbrusBin))
	}
	target := fmt.Sprintf("/ui/channels/%d", chID)
	if warning != "" {
		target += "?warning=" + url.QueryEscape(warning)
	}
	http.Redirect(w, r, target, http.StatusFound)
}

// sendToRun types text into a run's terminal; returns a warning string ("" on
// success) instead of an error — the caller has already recorded the message.
func (s *Server) sendToRun(run store.AgentRun, text string) string {
	if s.spawner == nil || run.TmuxTarget == "" {
		return fmt.Sprintf("%s has no reachable terminal — posted to channel only", run.AgentName)
	}
	if err := s.spawner.Send(spawn.Handle(run.TmuxTarget), text); err != nil {
		return fmt.Sprintf("delivery to %s failed: %s — posted to channel only", run.AgentName, err)
	}
	return ""
}

func (s *Server) handleUIStopRun(w http.ResponseWriter, r *http.Request) {
	id := chiInt64(r, "id")
	if _, status, errMsg := s.stopRunCore(id); status != 0 {
		httpError(w, status, errMsg)
		return
	}
	http.Redirect(w, r, "/ui/channels/"+r.FormValue("channel"), http.StatusFound)
}
