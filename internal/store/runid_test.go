package store

import (
	"path/filepath"
	"testing"
)

// Run ids are never reused: deleting the newest run and spawning again
// must not hand the same id out (SQLite's plain INTEGER PRIMARY KEY
// would), and the sequence survives a reopen of an existing database.
func TestRunIDsAreNeverReused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "e.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := s.CreateProject("p", "/tmp/p")
	ch, _ := s.CreateChannel(p.ID, "general", "/tmp/p", "main")
	mk := func() AgentRun {
		r, err := s.CreateRun(AgentRun{ChannelID: ch.ID, Provider: "codex", AgentName: "a", Workdir: "/w", Status: "done", Spawner: "tmux"})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	a, b := mk(), mk()
	if err := s.DeleteRun(b.ID); err != nil {
		t.Fatal(err)
	}
	c := mk()
	if c.ID <= b.ID {
		t.Fatalf("id reused: a=%d b=%d (deleted) c=%d", a.ID, b.ID, c.ID)
	}
	if err := s.DeleteRun(c.ID); err != nil {
		t.Fatal(err)
	}
	s.Close()

	// Reopen: the migration must not reset the sequence below ids handed out.
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	d, err := s.CreateRun(AgentRun{ChannelID: ch.ID, Provider: "codex", AgentName: "a", Workdir: "/w", Status: "done", Spawner: "tmux"})
	if err != nil {
		t.Fatal(err)
	}
	if d.ID <= c.ID {
		t.Fatalf("id reused after reopen: c=%d d=%d", c.ID, d.ID)
	}
}
