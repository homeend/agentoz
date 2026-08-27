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
