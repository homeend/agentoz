package store

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// TestMigrateSpawnerCheckRebuildsOldAgentRuns builds a database with the
// pre-wezterm agent_runs (CHECK (spawner IN ('tmux','fg'))) — as if
// created by an erbrus binary from before this task — and verifies Open
// rebuilds it in place: the old run survives, and a wezterm run can now
// be created. A second Open on the now-migrated file must be a no-op.
func TestMigrateSpawnerCheckRebuildsOldAgentRuns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	// The current schema, then swap agent_runs for the old CHECK — this
	// keeps projects/channels/messages identical to schema.sql without
	// duplicating their DDL in the test.
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	oldDDL, err := agentRunsDDL()
	if err != nil {
		t.Fatal(err)
	}
	oldDDL = strings.Replace(oldDDL, "'tmux','wezterm','fg'", "'tmux','fg'", 1)
	if !strings.Contains(oldDDL, "'tmux','fg'") {
		t.Fatalf("agentRunsDDL() did not contain the expected CHECK list: %s", oldDDL)
	}
	// run_ids is itself a migrate() addition (see TestRunIDsAreNeverReused);
	// an old binary that ever spawned a run would already have it.
	for _, q := range []string{`DROP TABLE agent_runs`, oldDDL, `CREATE TABLE run_ids (id INTEGER PRIMARY KEY AUTOINCREMENT)`} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}

	old := &Store{db: db}
	p, err := old.CreateProject("p", "/tmp/p")
	if err != nil {
		t.Fatal(err)
	}
	ch, err := old.CreateChannel(p.ID, "general", "/tmp/p", "main")
	if err != nil {
		t.Fatal(err)
	}
	oldRun, err := old.CreateRun(AgentRun{ChannelID: ch.ID, Provider: "codex", AgentName: "a", Workdir: "/w", Status: "done", Spawner: "tmux"})
	if err != nil {
		t.Fatal(err)
	}
	// Sanity: the old CHECK really does reject wezterm before migration.
	if _, err := old.CreateRun(AgentRun{ChannelID: ch.ID, Provider: "codex", AgentName: "b", Workdir: "/w", Status: "done", Spawner: "wezterm"}); err == nil {
		t.Fatal("old CHECK should have rejected spawner=wezterm")
	}
	db.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if got, ok, err := s.RunByID(oldRun.ID); err != nil || !ok || got.ID != oldRun.ID {
		t.Fatalf("old run not readable after migration: %+v %v %v", got, ok, err)
	}
	if _, err := s.CreateRun(AgentRun{ChannelID: ch.ID, Provider: "codex", AgentName: "c", Workdir: "/w", Status: "done", Spawner: "wezterm"}); err != nil {
		t.Fatalf("CreateRun(wezterm) after migration: %v", err)
	}
	s.Close()

	// Reopening the now-migrated file is a no-op: no error, the run
	// created above is still there.
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if got, ok, err := s2.RunByID(oldRun.ID); err != nil || !ok || got.ID != oldRun.ID {
		t.Fatalf("old run lost on second open: %+v %v %v", got, ok, err)
	}
}

// TestOpenFreshDatabaseTwiceIsFine: a brand-new database (created with
// today's schema.sql, CHECK already includes wezterm) never triggers the
// rebuild, opened once or twice.
func TestOpenFreshDatabaseTwiceIsFine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fresh.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	p, err := s2.CreateProject("p", "/tmp/p")
	if err != nil {
		t.Fatal(err)
	}
	ch, err := s2.CreateChannel(p.ID, "general", "/tmp/p", "main")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s2.CreateRun(AgentRun{ChannelID: ch.ID, Provider: "codex", AgentName: "a", Workdir: "/w", Status: "done", Spawner: "wezterm"}); err != nil {
		t.Fatalf("CreateRun(wezterm) on a fresh database: %v", err)
	}
}
