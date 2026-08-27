package store

import "testing"

func addMsg(t *testing.T, s *Store, chID int64, kind string) Message {
	t.Helper()
	ak := "human"
	if kind != "message" {
		ak = "agent"
	}
	if kind == "system" {
		ak = "system"
	}
	m, err := s.CreateMessage(Message{ChannelID: chID, Kind: kind, AuthorKind: ak, AuthorName: "x", Body: "b"})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestUnreadByChannel(t *testing.T) {
	s := open(t)
	p, _ := s.CreateProject("u", "/u")
	ch1, _ := s.CreateChannel(p.ID, "general", "/u", "main")
	ch2, _ := s.CreateChannel(p.ID, "feat", "/u", "feat")

	addMsg(t, s, ch1.ID, "message")
	addMsg(t, s, ch1.ID, "message")
	addMsg(t, s, ch2.ID, "message")
	addMsg(t, s, ch2.ID, "report")

	un, err := s.UnreadByChannel()
	if err != nil {
		t.Fatal(err)
	}
	if got := un[ch1.ID]; got.Count != 2 || got.Attention {
		t.Fatalf("ch1 = %+v, want {2 false}", got)
	}
	if got := un[ch2.ID]; got.Count != 2 || !got.Attention {
		t.Fatalf("ch2 = %+v, want {2 true}", got)
	}
}

func TestMarkChannelRead(t *testing.T) {
	s := open(t)
	p, _ := s.CreateProject("m", "/m")
	ch, _ := s.CreateChannel(p.ID, "general", "/m", "main")
	addMsg(t, s, ch.ID, "report")

	if err := s.MarkChannelRead(ch.ID); err != nil {
		t.Fatal(err)
	}
	un, err := s.UnreadByChannel()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := un[ch.ID]; ok {
		t.Fatalf("channel still unread after mark: %+v", un[ch.ID])
	}

	// New arrivals after the mark count again, and a system message flags
	// attention.
	addMsg(t, s, ch.ID, "system")
	un, _ = s.UnreadByChannel()
	if got := un[ch.ID]; got.Count != 1 || !got.Attention {
		t.Fatalf("after new system msg = %+v, want {1 true}", got)
	}
}

func TestMarkChannelReadEmptyChannel(t *testing.T) {
	s := open(t)
	p, _ := s.CreateProject("e", "/e")
	ch, _ := s.CreateChannel(p.ID, "general", "/e", "main")
	if err := s.MarkChannelRead(ch.ID); err != nil {
		t.Fatalf("mark on empty channel: %v", err)
	}
}

// TestUnreadMigrationOnExistingDB simulates a pre-badge database (channels
// table without last_read_message_id) and verifies Open migrates it.
func TestUnreadMigrationOnExistingDB(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/old.db"
	s1, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// Rebuild channels without the column, as v1 created it.
	stmts := []string{
		`DROP TABLE channels`,
		`CREATE TABLE channels (
		  id            INTEGER PRIMARY KEY,
		  project_id    INTEGER NOT NULL REFERENCES projects(id),
		  name          TEXT NOT NULL,
		  worktree_path TEXT NOT NULL DEFAULT '',
		  branch        TEXT NOT NULL DEFAULT '',
		  archived      INTEGER NOT NULL DEFAULT 0,
		  created_at    TEXT NOT NULL DEFAULT (datetime('now')),
		  UNIQUE(project_id, name)
		)`,
	}
	for _, q := range stmts {
		if _, err := s1.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	s1.Close()

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen with migration: %v", err)
	}
	defer s2.Close()
	p, err := s2.CreateProject("mig", "/mig")
	if err != nil {
		t.Fatal(err)
	}
	ch, err := s2.CreateChannel(p.ID, "general", "/mig", "main")
	if err != nil {
		t.Fatal(err)
	}
	if err := s2.MarkChannelRead(ch.ID); err != nil {
		t.Fatalf("MarkChannelRead on migrated db: %v", err)
	}
}

func TestMessageTargetLabel(t *testing.T) {
	s := open(t)
	p, _ := s.CreateProject("tl", "/tl")
	ch, _ := s.CreateChannel(p.ID, "general", "/tl", "main")
	m, err := s.CreateMessage(Message{ChannelID: ch.ID, Kind: "message",
		AuthorKind: "human", AuthorName: "you", TargetLabel: "claude", Body: "do it"})
	if err != nil {
		t.Fatal(err)
	}
	got, ok, _ := s.MessageByID(m.ID)
	if !ok || got.TargetLabel != "claude" {
		t.Fatalf("TargetLabel = %q, want claude", got.TargetLabel)
	}
	plain := addMsg(t, s, ch.ID, "message")
	got2, _, _ := s.MessageByID(plain.ID)
	if got2.TargetLabel != "" {
		t.Fatalf("plain message TargetLabel = %q, want empty", got2.TargetLabel)
	}
}

func TestActiveRunCountByChannel(t *testing.T) {
	s := open(t)
	p, _ := s.CreateProject("ac", "/ac")
	ch1, _ := s.CreateChannel(p.ID, "general", "/ac", "main")
	ch2, _ := s.CreateChannel(p.ID, "feat", "/ac", "feat")
	mk := func(chID int64, status string) AgentRun {
		r, err := s.CreateRun(AgentRun{ChannelID: chID, Provider: "codex",
			AgentName: "a", Workdir: "/ac", Status: status, Spawner: "tmux"})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	mk(ch1.ID, "running")
	mk(ch1.ID, "starting")
	mk(ch1.ID, "done")
	mk(ch2.ID, "failed")

	got, err := s.ActiveRunCountByChannel()
	if err != nil {
		t.Fatal(err)
	}
	if got[ch1.ID] != 2 {
		t.Fatalf("ch1 = %d, want 2", got[ch1.ID])
	}
	if _, ok := got[ch2.ID]; ok {
		t.Fatalf("ch2 should be absent, got %d", got[ch2.ID])
	}
}

func TestDeleteRun(t *testing.T) {
	s := open(t)
	p, _ := s.CreateProject("dr", "/dr")
	ch, _ := s.CreateChannel(p.ID, "general", "/dr", "main")
	run, err := s.CreateRun(AgentRun{ChannelID: ch.ID, Provider: "codex",
		AgentName: "a", Workdir: "/dr", Status: "done", Spawner: "tmux"})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := s.CreateMessage(Message{ChannelID: ch.ID, Kind: "report",
		AuthorKind: "agent", AuthorName: "a", AgentRunID: run.ID, Body: "done"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteRun(run.ID); err != nil {
		t.Fatalf("DeleteRun: %v", err)
	}
	if _, ok, _ := s.RunByID(run.ID); ok {
		t.Fatal("run row survived")
	}
	got, ok, _ := s.MessageByID(msg.ID)
	if !ok || got.AgentRunID != 0 {
		t.Fatalf("message must survive with nulled run ref: ok=%v run=%d", ok, got.AgentRunID)
	}
}

func TestDeleteMessage(t *testing.T) {
	s := open(t)
	p, _ := s.CreateProject("dm", "/dm")
	ch, _ := s.CreateChannel(p.ID, "general", "/dm", "main")
	ch2, _ := s.CreateChannel(p.ID, "feat", "/dm", "feat")
	src, err := s.CreateMessage(Message{ChannelID: ch.ID, Kind: "report",
		AuthorKind: "agent", AuthorName: "a", Body: "orig"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddArtifact(Artifact{MessageID: src.ID, Filename: "f", Path: "/x/f"}); err != nil {
		t.Fatal(err)
	}
	fwd, err := s.CreateMessage(Message{ChannelID: ch2.ID, Kind: "report",
		AuthorKind: "agent", AuthorName: "a", OriginMessageID: src.ID, Body: "orig"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.CreateRun(AgentRun{ChannelID: ch2.ID, Provider: "codex",
		AgentName: "b", Workdir: "/dm", Status: "done", Spawner: "tmux", OriginMessageID: src.ID})
	if err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteMessage(src.ID); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	if _, ok, _ := s.MessageByID(src.ID); ok {
		t.Fatal("message survived")
	}
	if arts, _ := s.ArtifactsByMessage(src.ID); len(arts) != 0 {
		t.Fatal("artifact rows survived")
	}
	if m, ok, _ := s.MessageByID(fwd.ID); !ok || m.OriginMessageID != 0 {
		t.Fatalf("forwarded copy: ok=%v origin=%d, want kept with nulled origin", ok, m.OriginMessageID)
	}
	if r, ok, _ := s.RunByID(run.ID); !ok || r.OriginMessageID != 0 {
		t.Fatalf("handoff run: ok=%v origin=%d, want kept with nulled origin", ok, r.OriginMessageID)
	}
}

func TestClearChannel(t *testing.T) {
	s := open(t)
	p, _ := s.CreateProject("cc", "/cc")
	ch, _ := s.CreateChannel(p.ID, "general", "/cc", "main")
	ch2, _ := s.CreateChannel(p.ID, "feat", "/cc", "feat")
	src, _ := s.CreateMessage(Message{ChannelID: ch.ID, Kind: "report",
		AuthorKind: "agent", AuthorName: "a", Body: "one"})
	if _, err := s.AddArtifact(Artifact{MessageID: src.ID, Filename: "f", Path: "/x/f"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateMessage(Message{ChannelID: ch.ID, Kind: "message",
		AuthorKind: "human", AuthorName: "you", Body: "two"}); err != nil {
		t.Fatal(err)
	}
	fwd, _ := s.CreateMessage(Message{ChannelID: ch2.ID, Kind: "report",
		AuthorKind: "agent", AuthorName: "a", OriginMessageID: src.ID, Body: "one"})

	ids, err := s.ChannelArtifactMessageIDs(ch.ID)
	if err != nil || len(ids) != 1 || ids[0] != src.ID {
		t.Fatalf("artifact msg ids = %v (%v)", ids, err)
	}
	if err := s.ClearChannel(ch.ID); err != nil {
		t.Fatalf("ClearChannel: %v", err)
	}
	if msgs, _ := s.MessagesSince(ch.ID, 0, 100); len(msgs) != 0 {
		t.Fatalf("messages remain: %d", len(msgs))
	}
	if m, ok, _ := s.MessageByID(fwd.ID); !ok || m.OriginMessageID != 0 {
		t.Fatalf("forwarded copy in other channel: ok=%v origin=%d", ok, m.OriginMessageID)
	}
}
