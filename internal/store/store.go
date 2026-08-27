// Package store is erbrus's SQLite persistence: runtime state and history
// only — settings live in YAML files, never here.
package store

import (
	"crypto/rand"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schema string

type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	// modernc sqlite: single writer; avoid SQLITE_BUSY from pooling.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

type Project struct {
	ID        int64
	Name      string
	RepoPath  string
	CreatedAt time.Time
}

type Channel struct {
	ID           int64
	ProjectID    int64
	Name         string
	WorktreePath string
	Branch       string
	Archived     bool
	CreatedAt    time.Time
}

type Message struct {
	ID              int64
	ChannelID       int64
	Kind            string
	AuthorKind      string
	AuthorName      string
	AgentRunID      int64 // 0 = none
	OriginMessageID int64 // 0 = none
	Body            string
	CreatedAt       time.Time
}

type Artifact struct {
	ID        int64
	MessageID int64
	Filename  string
	Path      string
	Size      int64
	CreatedAt time.Time
}

type AgentRun struct {
	ID              int64
	ChannelID       int64
	PresetName      string
	Provider        string
	AgentName       string
	Model           string
	ExtraArgs       string
	Prompt          string
	Workdir         string
	Status          string
	ExitCode        int64
	HasExit         bool
	Spawner         string
	TmuxTarget      string
	Token           string
	OriginMessageID int64
	CreatedAt       time.Time
	FinishedAt      time.Time
}

const timeFmt = "2006-01-02 15:04:05"

func parseTime(s string) time.Time {
	t, _ := time.Parse(timeFmt, s)
	return t
}

// nz maps 0 -> NULL for optional foreign keys.
func nz(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

func (s *Store) CreateProject(name, repoPath string) (Project, error) {
	res, err := s.db.Exec(`INSERT INTO projects (name, repo_path) VALUES (?, ?)`, name, repoPath)
	if err != nil {
		return Project{}, err
	}
	id, _ := res.LastInsertId()
	p, _, err := s.ProjectByID(id)
	return p, err
}

func (s *Store) scanProject(row *sql.Row) (Project, bool, error) {
	var p Project
	var ts string
	err := row.Scan(&p.ID, &p.Name, &p.RepoPath, &ts)
	if err == sql.ErrNoRows {
		return p, false, nil
	}
	if err != nil {
		return p, false, err
	}
	p.CreatedAt = parseTime(ts)
	return p, true, nil
}

func (s *Store) ProjectByID(id int64) (Project, bool, error) {
	return s.scanProject(s.db.QueryRow(`SELECT id, name, repo_path, created_at FROM projects WHERE id = ?`, id))
}

func (s *Store) ProjectByPath(repoPath string) (Project, bool, error) {
	return s.scanProject(s.db.QueryRow(`SELECT id, name, repo_path, created_at FROM projects WHERE repo_path = ?`, repoPath))
}

func (s *Store) Projects() ([]Project, error) {
	rows, err := s.db.Query(`SELECT id, name, repo_path, created_at FROM projects ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		var p Project
		var ts string
		if err := rows.Scan(&p.ID, &p.Name, &p.RepoPath, &ts); err != nil {
			return nil, err
		}
		p.CreatedAt = parseTime(ts)
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) CreateChannel(projectID int64, name, worktreePath, branch string) (Channel, error) {
	res, err := s.db.Exec(`INSERT INTO channels (project_id, name, worktree_path, branch) VALUES (?, ?, ?, ?)`,
		projectID, name, worktreePath, branch)
	if err != nil {
		return Channel{}, err
	}
	id, _ := res.LastInsertId()
	c, _, err := s.ChannelByID(id)
	return c, err
}

const chanCols = `id, project_id, name, worktree_path, branch, archived, created_at`

func scanChannel(sc interface{ Scan(...any) error }) (Channel, error) {
	var c Channel
	var ts string
	err := sc.Scan(&c.ID, &c.ProjectID, &c.Name, &c.WorktreePath, &c.Branch, &c.Archived, &ts)
	c.CreatedAt = parseTime(ts)
	return c, err
}

func (s *Store) ChannelByID(id int64) (Channel, bool, error) {
	c, err := scanChannel(s.db.QueryRow(`SELECT `+chanCols+` FROM channels WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return c, false, nil
	}
	return c, err == nil, err
}

func (s *Store) ChannelByName(projectID int64, name string) (Channel, bool, error) {
	c, err := scanChannel(s.db.QueryRow(`SELECT `+chanCols+` FROM channels WHERE project_id = ? AND name = ?`, projectID, name))
	if err == sql.ErrNoRows {
		return c, false, nil
	}
	return c, err == nil, err
}

func (s *Store) ChannelsByProject(projectID int64) ([]Channel, error) {
	rows, err := s.db.Query(`SELECT `+chanCols+` FROM channels WHERE project_id = ? ORDER BY id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Channel
	for rows.Next() {
		c, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) CreateMessage(m Message) (Message, error) {
	res, err := s.db.Exec(
		`INSERT INTO messages (channel_id, kind, author_kind, author_name, agent_run_id, origin_message_id, body)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		m.ChannelID, m.Kind, m.AuthorKind, m.AuthorName, nz(m.AgentRunID), nz(m.OriginMessageID), m.Body)
	if err != nil {
		return Message{}, err
	}
	id, _ := res.LastInsertId()
	got, _, err := s.MessageByID(id)
	return got, err
}

const msgCols = `id, channel_id, kind, author_kind, author_name,
	COALESCE(agent_run_id, 0), COALESCE(origin_message_id, 0), body, created_at`

func scanMessage(sc interface{ Scan(...any) error }) (Message, error) {
	var m Message
	var ts string
	err := sc.Scan(&m.ID, &m.ChannelID, &m.Kind, &m.AuthorKind, &m.AuthorName,
		&m.AgentRunID, &m.OriginMessageID, &m.Body, &ts)
	m.CreatedAt = parseTime(ts)
	return m, err
}

func (s *Store) MessageByID(id int64) (Message, bool, error) {
	m, err := scanMessage(s.db.QueryRow(`SELECT `+msgCols+` FROM messages WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return m, false, nil
	}
	return m, err == nil, err
}

func (s *Store) MessagesSince(channelID, sinceID int64, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.Query(`SELECT `+msgCols+` FROM messages
		WHERE channel_id = ? AND id > ? ORDER BY id LIMIT ?`, channelID, sinceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) AddArtifact(a Artifact) (Artifact, error) {
	res, err := s.db.Exec(`INSERT INTO artifacts (message_id, filename, path, size) VALUES (?, ?, ?, ?)`,
		a.MessageID, a.Filename, a.Path, a.Size)
	if err != nil {
		return Artifact{}, err
	}
	id, _ := res.LastInsertId()
	// Query back to populate ID and CreatedAt like other Create methods
	var art Artifact
	var ts string
	err = s.db.QueryRow(`SELECT id, message_id, filename, path, size, created_at FROM artifacts WHERE id = ?`, id).
		Scan(&art.ID, &art.MessageID, &art.Filename, &art.Path, &art.Size, &ts)
	if err != nil {
		return Artifact{}, err
	}
	art.CreatedAt = parseTime(ts)
	return art, nil
}

// ArtifactByID fetches one artifact row.
func (s *Store) ArtifactByID(id int64) (Artifact, bool, error) {
	var a Artifact
	var ts string
	err := s.db.QueryRow(`SELECT id, message_id, filename, path, size, created_at
		FROM artifacts WHERE id = ?`, id).
		Scan(&a.ID, &a.MessageID, &a.Filename, &a.Path, &a.Size, &ts)
	if err == sql.ErrNoRows {
		return a, false, nil
	}
	a.CreatedAt = parseTime(ts)
	return a, err == nil, err
}

func (s *Store) ArtifactsByMessage(messageID int64) ([]Artifact, error) {
	rows, err := s.db.Query(`SELECT id, message_id, filename, path, size, created_at
		FROM artifacts WHERE message_id = ? ORDER BY id`, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Artifact
	for rows.Next() {
		var a Artifact
		var ts string
		if err := rows.Scan(&a.ID, &a.MessageID, &a.Filename, &a.Path, &a.Size, &ts); err != nil {
			return nil, err
		}
		a.CreatedAt = parseTime(ts)
		out = append(out, a)
	}
	return out, rows.Err()
}

func newToken() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Store) CreateRun(r AgentRun) (AgentRun, error) {
	r.Token = newToken()
	res, err := s.db.Exec(
		`INSERT INTO agent_runs (channel_id, preset_name, provider, agent_name, model, extra_args,
			prompt, workdir, status, spawner, tmux_target, token, origin_message_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ChannelID, r.PresetName, r.Provider, r.AgentName, r.Model, r.ExtraArgs,
		r.Prompt, r.Workdir, r.Status, r.Spawner, r.TmuxTarget, r.Token, nz(r.OriginMessageID))
	if err != nil {
		return AgentRun{}, err
	}
	id, _ := res.LastInsertId()
	got, _, err := s.RunByID(id)
	return got, err
}

const runCols = `id, channel_id, preset_name, provider, agent_name, model, extra_args,
	prompt, workdir, status, exit_code, spawner, tmux_target, token,
	COALESCE(origin_message_id, 0), created_at, COALESCE(finished_at, '')`

func scanRun(sc interface{ Scan(...any) error }) (AgentRun, error) {
	var r AgentRun
	var exit sql.NullInt64
	var created, finished string
	err := sc.Scan(&r.ID, &r.ChannelID, &r.PresetName, &r.Provider, &r.AgentName, &r.Model,
		&r.ExtraArgs, &r.Prompt, &r.Workdir, &r.Status, &exit, &r.Spawner, &r.TmuxTarget,
		&r.Token, &r.OriginMessageID, &created, &finished)
	r.ExitCode, r.HasExit = exit.Int64, exit.Valid
	r.CreatedAt = parseTime(created)
	if finished != "" {
		r.FinishedAt = parseTime(finished)
	}
	return r, err
}

func (s *Store) RunByID(id int64) (AgentRun, bool, error) {
	r, err := scanRun(s.db.QueryRow(`SELECT `+runCols+` FROM agent_runs WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return r, false, nil
	}
	return r, err == nil, err
}

func (s *Store) RunByToken(token string) (AgentRun, bool, error) {
	r, err := scanRun(s.db.QueryRow(`SELECT `+runCols+` FROM agent_runs WHERE token = ?`, token))
	if err == sql.ErrNoRows {
		return r, false, nil
	}
	return r, err == nil, err
}

func (s *Store) runsWhere(where string, args ...any) ([]AgentRun, error) {
	rows, err := s.db.Query(`SELECT `+runCols+` FROM agent_runs `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AgentRun
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) RunsByChannel(channelID int64) ([]AgentRun, error) {
	return s.runsWhere(`WHERE channel_id = ? ORDER BY id`, channelID)
}

func (s *Store) RunningRuns() ([]AgentRun, error) {
	return s.runsWhere(`WHERE status IN ('starting','running') ORDER BY id`)
}

func (s *Store) FinishRun(id int64, status string, exitCode int64) error {
	_, err := s.db.Exec(`UPDATE agent_runs SET status = ?, exit_code = ?, finished_at = datetime('now') WHERE id = ?`,
		status, exitCode, id)
	return err
}

// StartRun marks a run running and records its tmux target.
func (s *Store) StartRun(id int64, tmuxTarget string) error {
	_, err := s.db.Exec(`UPDATE agent_runs SET status = 'running', tmux_target = ? WHERE id = ?`, tmuxTarget, id)
	return err
}
