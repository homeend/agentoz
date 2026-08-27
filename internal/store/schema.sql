CREATE TABLE IF NOT EXISTS projects (
  id         INTEGER PRIMARY KEY,
  name       TEXT NOT NULL UNIQUE,
  repo_path  TEXT NOT NULL UNIQUE,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS channels (
  id            INTEGER PRIMARY KEY,
  project_id    INTEGER NOT NULL REFERENCES projects(id),
  name          TEXT NOT NULL,
  worktree_path TEXT NOT NULL DEFAULT '',
  branch        TEXT NOT NULL DEFAULT '',
  archived      INTEGER NOT NULL DEFAULT 0,
  last_read_message_id INTEGER NOT NULL DEFAULT 0,
  created_at    TEXT NOT NULL DEFAULT (datetime('now')),
  UNIQUE(project_id, name)
);
CREATE TABLE IF NOT EXISTS messages (
  id                INTEGER PRIMARY KEY,
  channel_id        INTEGER NOT NULL REFERENCES channels(id),
  kind              TEXT NOT NULL CHECK (kind IN ('message','report','system')),
  author_kind       TEXT NOT NULL CHECK (author_kind IN ('agent','human','system')),
  author_name       TEXT NOT NULL DEFAULT '',
  agent_run_id      INTEGER REFERENCES agent_runs(id),
  origin_message_id INTEGER REFERENCES messages(id),
  body              TEXT NOT NULL,
  created_at        TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_messages_channel ON messages(channel_id, id);
CREATE TABLE IF NOT EXISTS artifacts (
  id         INTEGER PRIMARY KEY,
  message_id INTEGER NOT NULL REFERENCES messages(id),
  filename   TEXT NOT NULL,
  path       TEXT NOT NULL,
  size       INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS agent_runs (
  id                INTEGER PRIMARY KEY,
  channel_id        INTEGER NOT NULL REFERENCES channels(id),
  preset_name       TEXT NOT NULL DEFAULT '',
  provider          TEXT NOT NULL,
  agent_name        TEXT NOT NULL,
  model             TEXT NOT NULL DEFAULT '',
  extra_args        TEXT NOT NULL DEFAULT '',
  prompt            TEXT NOT NULL DEFAULT '',
  workdir           TEXT NOT NULL,
  status            TEXT NOT NULL CHECK (status IN ('starting','running','done','failed','stopped')),
  exit_code         INTEGER,
  spawner           TEXT NOT NULL CHECK (spawner IN ('tmux','fg')),
  tmux_target       TEXT NOT NULL DEFAULT '',
  token             TEXT NOT NULL UNIQUE,
  origin_message_id INTEGER REFERENCES messages(id),
  created_at        TEXT NOT NULL DEFAULT (datetime('now')),
  finished_at       TEXT
);
