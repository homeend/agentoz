package server

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"erbrus/internal/config"
	"erbrus/internal/integrate"
	"erbrus/internal/preset"
	"erbrus/internal/provider"
	"erbrus/internal/spawn"
	"erbrus/internal/store"
)

type runRequest struct {
	ChannelID       int64  `json:"channel_id"`
	Preset          string `json:"preset"`
	Provider        string `json:"provider"`
	Name            string `json:"name"`
	Model           string `json:"model"`
	Args            string `json:"args"`
	Prompt          string `json:"prompt"`
	Workdir         string `json:"workdir"`
	Fg              bool   `json:"fg"`
	OriginMessageID int64  `json:"origin_message_id"`
}

type runJSON struct {
	ID         int64  `json:"id"`
	ChannelID  int64  `json:"channel_id"`
	Provider   string `json:"provider"`
	AgentName  string `json:"agent_name"`
	Status     string `json:"status"`
	TmuxTarget string `json:"tmux_target,omitempty"`
	ExitCode   *int64 `json:"exit_code,omitempty"`
}

type fgJSON struct {
	Run     runJSON           `json:"run"`
	CmdFile string            `json:"cmd_file"`
	Env     map[string]string `json:"env"`
}

func toRunJSON(r store.AgentRun) runJSON {
	rj := runJSON{ID: r.ID, ChannelID: r.ChannelID, Provider: r.Provider,
		AgentName: r.AgentName, Status: r.Status, TmuxTarget: r.TmuxTarget}
	if r.HasExit {
		v := r.ExitCode
		rj.ExitCode = &v
	}
	return rj
}

// system posts a system message to a channel and publishes it.
func (s *Server) system(channelID int64, body string) {
	m, err := s.st.CreateMessage(store.Message{ChannelID: channelID, Kind: "system", AuthorKind: "system", Body: body})
	if err == nil {
		s.hub.Publish("message", s.messageJSON(m))
	}
}

// firstNonEmpty returns the first non-empty value: explicit beats preset,
// preset beats zero.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// joinNonEmpty joins a and b with sep, skipping either side if empty.
func joinNonEmpty(a, b, sep string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	return a + sep + b
}

var modelRe = regexp.MustCompile(`^[A-Za-z0-9._:-]*$`)

// validModel reports whether model is safe to splice unquoted into a
// rendered shell command (provider templates place {model} bare).
func validModel(model string) bool {
	return modelRe.MatchString(model)
}

// shellMetaChars are bytes that must not appear in request/preset-supplied
// args, since they land unquoted inside the rendered command. Server-
// generated hook args are appended after this check and are trusted.
const shellMetaChars = ";|&$()`<>\n"

func validArgs(args string) bool {
	return !strings.ContainsAny(args, shellMetaChars)
}

func (s *Server) handleSpawnRun(w http.ResponseWriter, r *http.Request) {
	var req runRequest
	if err := decodeBody(r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}

	payload, status, errMsg := s.spawnRunCore(req)
	if status != 0 {
		httpError(w, status, errMsg)
		return
	}
	writeJSON(w, http.StatusCreated, payload)
}

// spawnRunCore: everything handleSpawnRun does after decoding. payload is
// runJSON (tmux) or fgJSON (fg) on success with status 0.
func (s *Server) spawnRunCore(req runRequest) (payload any, status int, errMsg string) {
	// Step 1: channel + project lookup.
	channel, ok, err := s.st.ChannelByID(req.ChannelID)
	if err != nil {
		return nil, http.StatusInternalServerError, err.Error()
	}
	if !ok {
		return nil, http.StatusNotFound, "channel not found"
	}
	project, ok, err := s.st.ProjectByID(channel.ProjectID)
	if err != nil {
		return nil, http.StatusInternalServerError, err.Error()
	}
	if !ok {
		return nil, http.StatusNotFound, "project not found"
	}

	// Step 2: repo config, preset overlay, provider resolution.
	repoCfg, _, err := config.LoadRepo(project.RepoPath)
	if err != nil {
		return nil, http.StatusBadRequest, "repo config invalid: " + err.Error()
	}
	presets := preset.Merge(s.cfg.Presets, repoCfg.Presets)
	var presetCfg config.Preset
	if req.Preset != "" {
		presetCfg, err = preset.Resolve(req.Preset, presets)
		if err != nil {
			return nil, http.StatusBadRequest, err.Error()
		}
	}
	providerName := firstNonEmpty(req.Provider, presetCfg.Provider)
	if providerName == "" {
		return nil, http.StatusBadRequest, "provider is required"
	}
	if _, ok := s.cfg.Providers[providerName]; !ok {
		return nil, http.StatusBadRequest, fmt.Sprintf("unknown provider %q", providerName)
	}
	model := firstNonEmpty(req.Model, presetCfg.Model)
	argsStr := firstNonEmpty(req.Args, presetCfg.Args)
	promptText := firstNonEmpty(req.Prompt, presetCfg.Prompt)
	agentName := firstNonEmpty(req.Name, presetCfg.Name, req.Preset, providerName)

	// model/args (the overlaid, request-or-preset values, BEFORE server-
	// generated hook args are appended) land unquoted in the rendered
	// command — reject shell metacharacters before anything is created.
	if !validModel(model) {
		return nil, http.StatusBadRequest, "invalid model"
	}
	if !validArgs(argsStr) {
		return nil, http.StatusBadRequest, "args contains shell metacharacters"
	}

	// Step 3: workdir default.
	workdir := firstNonEmpty(req.Workdir, channel.WorktreePath, project.RepoPath)

	// Hoisted ahead of CreateRun: the spawner-nil check (tmux path only)
	// and the origin-message lookup. Neither must leave an orphaned
	// "starting" run row behind on failure.
	if !req.Fg && s.spawner == nil {
		return nil, http.StatusServiceUnavailable, "no spawner configured"
	}
	var handoffContext string
	var originMsg store.Message
	haveOrigin := req.OriginMessageID != 0
	if haveOrigin {
		msg, ok, err := s.st.MessageByID(req.OriginMessageID)
		if err != nil {
			return nil, http.StatusInternalServerError, err.Error()
		}
		if !ok {
			return nil, http.StatusNotFound, "origin message not found"
		}
		originChannel, _, err := s.st.ChannelByID(msg.ChannelID)
		if err != nil {
			return nil, http.StatusInternalServerError, err.Error()
		}
		originProject, ok, err := s.st.ProjectByID(originChannel.ProjectID)
		if err != nil {
			return nil, http.StatusInternalServerError, err.Error()
		}
		if !ok {
			return nil, http.StatusNotFound, "origin project not found"
		}
		artifacts, err := s.st.ArtifactsByMessage(msg.ID)
		if err != nil {
			return nil, http.StatusInternalServerError, err.Error()
		}
		var artifactPaths []string
		for _, a := range artifacts {
			artifactPaths = append(artifactPaths, a.Path)
		}
		handoffContext = integrate.HandoffContext(originProject.Name, originChannel.Name, msg.AuthorName,
			msg.CreatedAt.Format("2006-01-02 15:04:05"), msg.Body, artifactPaths)
		originMsg = msg
	}

	// Step 4: create the run row.
	spawnerKind := "tmux"
	if req.Fg {
		spawnerKind = "fg"
	}
	run, err := s.st.CreateRun(store.AgentRun{
		ChannelID: req.ChannelID, PresetName: req.Preset, Provider: providerName,
		AgentName: agentName, Model: model, ExtraArgs: argsStr, Prompt: promptText,
		Workdir: workdir, Status: "starting", Spawner: spawnerKind,
		OriginMessageID: req.OriginMessageID,
	})
	if err != nil {
		return nil, http.StatusInternalServerError, err.Error()
	}

	// Step 5: run dir.
	runDir := filepath.Join(s.dataDir, "runs", fmt.Sprint(run.ID))
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return nil, http.StatusInternalServerError, err.Error()
	}

	// Step 6: provider hook + args.
	hookArgs, _ := integrate.ProviderHook(providerName, runDir, s.erbrusBin)
	fullArgs := joinNonEmpty(argsStr, hookArgs, " ")

	// Step 7: assemble prompt, render command.
	preamble := integrate.Preamble(s.erbrusBin, agentName, channel.Name)
	fullPrompt := integrate.AssemblePrompt(preamble, promptText, handoffContext)
	command, err := provider.Registry(s.cfg.Providers).Render(providerName, model, fullArgs, fullPrompt)
	if err != nil {
		return nil, http.StatusInternalServerError, err.Error()
	}

	// Step 8: write cmd.sh.
	cmdPath := filepath.Join(runDir, "cmd.sh")
	cmdContent := fmt.Sprintf("#!/bin/sh\nexec %s\n", command)
	if err := os.WriteFile(cmdPath, []byte(cmdContent), 0o755); err != nil {
		return nil, http.StatusInternalServerError, err.Error()
	}

	// Step 9: env.
	env := map[string]string{
		"ERBRUS_URL":     s.baseURL,
		"ERBRUS_TOKEN":   run.Token,
		"ERBRUS_CHANNEL": fmt.Sprint(req.ChannelID),
		"ERBRUS_RUN_ID":  fmt.Sprint(run.ID),
	}

	// announceHandoff posts the origin-channel note only once the run
	// actually exists and (on the tmux path) actually spawned — a failed
	// spawn must not announce a handoff that never happened.
	announceHandoff := func() {
		if haveOrigin {
			s.system(originMsg.ChannelID, fmt.Sprintf("report handed off to #%s (run %d)", channel.Name, run.ID))
		}
	}

	// Step 10: fg path — no spawner needed, run stays "starting".
	if req.Fg {
		announceHandoff()
		return fgJSON{Run: toRunJSON(run), CmdFile: cmdPath, Env: env}, 0, ""
	}

	// Step 11: tmux path (spawner-nil already checked above).
	session := repoCfg.Session
	if session == "" {
		session = strings.ReplaceAll(s.cfg.SessionPattern, "{project}", project.Name)
	}
	spec := spawn.RunSpec{
		Session:       session,
		AttachSession: repoCfg.AttachSession,
		WindowName:    agentName,
		Workdir:       workdir,
		Env:           env,
		Command:       []string{s.erbrusBin, "wrap", cmdPath},
	}
	handle, err := s.spawner.Spawn(spec)
	if err != nil {
		s.st.FinishRun(run.ID, "failed", -1)
		s.system(req.ChannelID, fmt.Sprintf("spawn failed: %s", err))
		return nil, http.StatusInternalServerError, err.Error()
	}
	if err := s.st.StartRun(run.ID, string(handle)); err != nil {
		return nil, http.StatusInternalServerError, err.Error()
	}
	run.Status = "running"
	run.TmuxTarget = string(handle)
	s.system(req.ChannelID, fmt.Sprintf("%s spawned in tmux %s", agentName, handle))
	announceHandoff()
	rj := toRunJSON(run)
	s.hub.Publish("run", rj)
	return rj, 0, ""
}

// Reconcile marks tmux runs whose window no longer exists as failed, with a
// system message. fg runs are left alone (their wrapper may still report).
// Called by serve after SetRuntime; no-op when spawner is nil.
func (s *Server) Reconcile() error {
	if s.spawner == nil {
		return nil
	}
	runs, err := s.st.RunningRuns()
	if err != nil {
		return err
	}
	for _, r := range runs {
		if r.Spawner != "tmux" || r.TmuxTarget == "" {
			continue
		}
		ok, err := s.spawner.Alive(spawn.Handle(r.TmuxTarget))
		if err != nil || ok {
			continue
		}
		if err := s.st.FinishRun(r.ID, "failed", -1); err != nil {
			return err
		}
		s.system(r.ChannelID, fmt.Sprintf("%s orphaned (tmux window %s gone) — marked failed", r.AgentName, r.TmuxTarget))
	}
	return nil
}

func (s *Server) handleRunExit(w http.ResponseWriter, r *http.Request) {
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
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if token == "" || token != run.Token {
		httpError(w, http.StatusUnauthorized, "token does not match run")
		return
	}
	if run.Status != "starting" && run.Status != "running" {
		httpError(w, http.StatusConflict, "run already finished")
		return
	}
	var req struct {
		Code int64 `json:"code"`
	}
	if err := decodeBody(r, &req); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	status := "done"
	if req.Code != 0 {
		status = "failed"
	}
	if err := s.st.FinishRun(run.ID, status, req.Code); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.system(run.ChannelID, fmt.Sprintf("%s finished · exit %d", run.AgentName, req.Code))
	got, _, _ := s.st.RunByID(run.ID)
	s.hub.Publish("run", toRunJSON(got))
	writeJSON(w, http.StatusOK, toRunJSON(got))
}

func (s *Server) handleRunStop(w http.ResponseWriter, r *http.Request) {
	id := chiInt64(r, "id")
	rj, status, errMsg := s.stopRunCore(id)
	if status != 0 {
		httpError(w, status, errMsg)
		return
	}
	writeJSON(w, http.StatusOK, rj)
}

// stopRunCore: everything handleRunStop does after the id parse.
func (s *Server) stopRunCore(id int64) (rj runJSON, status int, errMsg string) {
	run, ok, err := s.st.RunByID(id)
	if err != nil {
		return runJSON{}, http.StatusInternalServerError, err.Error()
	}
	if !ok {
		return runJSON{}, http.StatusNotFound, "run not found"
	}
	if run.Status != "starting" && run.Status != "running" {
		return runJSON{}, http.StatusConflict, "run already finished"
	}
	if run.TmuxTarget != "" && s.spawner != nil {
		if err := s.spawner.Stop(spawn.Handle(run.TmuxTarget)); err != nil {
			s.system(run.ChannelID, fmt.Sprintf("stop of %s reported: %s", run.AgentName, err))
		}
	}
	if err := s.st.FinishRun(run.ID, "stopped", -1); err != nil {
		return runJSON{}, http.StatusInternalServerError, err.Error()
	}
	s.system(run.ChannelID, fmt.Sprintf("%s stopped by user", run.AgentName))
	got, _, _ := s.st.RunByID(run.ID)
	s.hub.Publish("run", toRunJSON(got))
	return toRunJSON(got), 0, ""
}

func (s *Server) handleChannelRuns(w http.ResponseWriter, r *http.Request) {
	chID := chiInt64(r, "id")
	if _, ok, err := s.st.ChannelByID(chID); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	} else if !ok {
		httpError(w, http.StatusNotFound, "channel not found")
		return
	}
	runs, err := s.st.RunsByChannel(chID)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := []runJSON{}
	for _, run := range runs {
		out = append(out, toRunJSON(run))
	}
	writeJSON(w, http.StatusOK, out)
}
