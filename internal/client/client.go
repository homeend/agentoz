// Package client talks to a running erbrus server over its HTTP API. Both
// the CLI subcommands and spawned agents (via env vars) use it.
package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
)

type Artifact struct {
	ID       int64  `json:"id"`
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
}

type Message struct {
	ID              int64      `json:"id"`
	ChannelID       int64      `json:"channel_id"`
	Kind            string     `json:"kind"`
	AuthorKind      string     `json:"author_kind"`
	AuthorName      string     `json:"author_name"`
	OriginMessageID int64      `json:"origin_message_id,omitempty"`
	Body            string     `json:"body"`
	CreatedAt       string     `json:"created_at"`
	Artifacts       []Artifact `json:"artifacts,omitempty"`
}

type Channel struct {
	ID           int64  `json:"id"`
	ProjectID    int64  `json:"project_id"`
	Name         string `json:"name"`
	WorktreePath string `json:"worktree_path"`
	Branch       string `json:"branch"`
}

type Project struct {
	ID       int64     `json:"id"`
	Name     string    `json:"name"`
	RepoPath string    `json:"repo_path"`
	Channels []Channel `json:"channels"`
	Created  bool      `json:"created"`
	Warning  string    `json:"warning,omitempty"`
}

type Client struct {
	Base  string
	Token string
	HTTP  *http.Client
}

func New(base, token string) *Client {
	return &Client{Base: base, Token: token, HTTP: http.DefaultClient}
}

func FromEnv() (*Client, int64, error) {
	base := os.Getenv("ERBRUS_URL")
	if base == "" {
		base = "http://127.0.0.1:7420"
	}
	ch, _ := strconv.ParseInt(os.Getenv("ERBRUS_CHANNEL"), 10, 64)
	return New(base, os.Getenv("ERBRUS_TOKEN")), ch, nil
}

func (c *Client) do(method, path string, contentType string, body io.Reader, out any) error {
	req, err := http.NewRequest(method, c.Base+path, body)
	if err != nil {
		return err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("erbrus server unreachable at %s (is `erbrus serve` running?): %w", c.Base, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		var e struct {
			Error string `json:"error"`
		}
		json.NewDecoder(resp.Body).Decode(&e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		return fmt.Errorf("%s %s: %s", method, path, e.Error)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) postJSON(path string, in, out any) error {
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return c.do("POST", path, "application/json", bytes.NewReader(b), out)
}

// Provider mirrors the server's provider JSON (GET/PUT /api/providers/{name}).
type Provider struct {
	Name           string   `json:"name"`
	Command        string   `json:"command"`
	DefaultModel   string   `json:"default_model"`
	Prompt         string   `json:"prompt"`
	ScreenWorking  []string `json:"screen_working"`
	ScreenWaiting  []string `json:"screen_waiting"`
	ScreenQuestion []string `json:"screen_question"`
	RulesSource    string   `json:"rules_source"`
}

func (c *Client) GetProvider(name string) (Provider, error) {
	var p Provider
	return p, c.do("GET", "/api/providers/"+name, "", nil, &p)
}

// PutProvider sends a partial update; keys absent from patch are kept.
func (c *Client) PutProvider(name string, patch map[string]any) (Provider, error) {
	var p Provider
	b, err := json.Marshal(patch)
	if err != nil {
		return p, err
	}
	return p, c.do("PUT", "/api/providers/"+name, "application/json", bytes.NewReader(b), &p)
}

type ScreenOption struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

// Screen is a run's terminal as the classifier sees it.
type Screen struct {
	State   string         `json:"state"`
	Lines   []string       `json:"lines"`
	Options []ScreenOption `json:"options"`
	Cols    int            `json:"cols"`
	Rows    int            `json:"rows"`
	Dead    bool           `json:"dead"`
}

func (c *Client) RunScreen(runID int64, lines int) (Screen, error) {
	var s Screen
	return s, c.do("GET", fmt.Sprintf("/api/runs/%d/screen?lines=%d", runID, lines), "", nil, &s)
}

type ScreenTestLine struct {
	Text  string `json:"text"`
	Match string `json:"match"`
}

type ScreenTest struct {
	State string           `json:"state"`
	Lines []ScreenTestLine `json:"lines"`
}

// ScreenTest classifies a run's screen with unsaved patterns.
func (c *Client) ScreenTest(provider string, runID int64, working, waiting, question []string) (ScreenTest, error) {
	var out ScreenTest
	return out, c.postJSON("/api/providers/"+provider+"/screen-test",
		map[string]any{"run": runID, "working": working, "waiting": waiting, "question": question}, &out)
}

// SendMessage posts body to a channel. format is "" (plain) or "md"
// (rendered as markdown in the UI).
func (c *Client) SendMessage(channelID int64, kind, format, body string, files []string) (Message, error) {
	var m Message
	path := fmt.Sprintf("/api/channels/%d/messages", channelID)
	if len(files) == 0 {
		err := c.postJSON(path, map[string]string{"kind": kind, "format": format, "body": body}, &m)
		return m, err
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	w.WriteField("kind", kind)
	w.WriteField("format", format)
	w.WriteField("body", body)
	for _, f := range files {
		fw, err := w.CreateFormFile("file", filepath.Base(f))
		if err != nil {
			return m, err
		}
		src, err := os.Open(f)
		if err != nil {
			return m, err
		}
		if _, err := io.Copy(fw, src); err != nil {
			src.Close()
			return m, err
		}
		src.Close()
	}
	w.Close()
	err := c.do("POST", path, w.FormDataContentType(), &buf, &m)
	return m, err
}

func (c *Client) ReadMessages(channelID, since int64, limit int) ([]Message, error) {
	var msgs []Message
	err := c.do("GET", fmt.Sprintf("/api/channels/%d/messages?since=%d&limit=%d", channelID, since, limit), "", nil, &msgs)
	return msgs, err
}

func (c *Client) AddProject(repoPath string) (Project, error) {
	var p Project
	err := c.postJSON("/api/projects", map[string]string{"repo_path": repoPath}, &p)
	return p, err
}

func (c *Client) ListProjects() ([]Project, error) {
	var ps []Project
	err := c.do("GET", "/api/projects", "", nil, &ps)
	return ps, err
}

// RunResult is a union of the server's runJSON (tmux path) and fgJSON (fg
// path) response shapes. Run is non-nil only on the fg path, where the
// server nests the run under "run" alongside cmd_file/env.
type RunResult struct {
	ID         int64             `json:"id"`
	ChannelID  int64             `json:"channel_id"`
	Provider   string            `json:"provider"`
	AgentName  string            `json:"agent_name"`
	Status     string            `json:"status"`
	TmuxTarget string            `json:"tmux_target,omitempty"`
	Run        *RunResult        `json:"run,omitempty"`
	CmdFile    string            `json:"cmd_file,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
}

// SpawnRun starts an agent run via POST /api/runs. req is marshaled as-is,
// letting callers build the request incrementally (only set what applies).
func (c *Client) SpawnRun(req map[string]any) (RunResult, error) {
	var res RunResult
	err := c.postJSON("/api/runs", req, &res)
	return res, err
}

// ReportExit posts a run's exit status; used by the wrap mode.
func (c *Client) ReportExit(runID int64, code int) error {
	return c.postJSON(fmt.Sprintf("/api/runs/%d/exit", runID), map[string]int{"code": code}, nil)
}
