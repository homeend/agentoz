package store

import (
	"path/filepath"
	"testing"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestProjectRoundtrip(t *testing.T) {
	s := open(t)
	p, err := s.CreateProject("webshop", "/code/webshop")
	if err != nil {
		t.Fatal(err)
	}
	if p.ID == 0 {
		t.Fatal("ID not assigned")
	}
	got, ok, err := s.ProjectByPath("/code/webshop")
	if err != nil || !ok || got.Name != "webshop" {
		t.Fatalf("ProjectByPath: %v %v %+v", err, ok, got)
	}
	_, ok, err = s.ProjectByPath("/nope")
	if err != nil || ok {
		t.Fatal("missing project should be ok=false, no error")
	}
	if _, err := s.CreateProject("webshop2", "/code/webshop"); err == nil {
		t.Fatal("duplicate repo_path must error")
	}
	all, err := s.Projects()
	if err != nil || len(all) != 1 {
		t.Fatalf("Projects: %v len=%d", err, len(all))
	}
}

func TestChannelRoundtrip(t *testing.T) {
	s := open(t)
	p, _ := s.CreateProject("x", "/x")
	c, err := s.CreateChannel(p.ID, "general", "/x", "main")
	if err != nil {
		t.Fatal(err)
	}
	got, ok, _ := s.ChannelByName(p.ID, "general")
	if !ok || got.ID != c.ID || got.Branch != "main" {
		t.Fatalf("ChannelByName: %+v", got)
	}
	list, _ := s.ChannelsByProject(p.ID)
	if len(list) != 1 {
		t.Fatalf("len = %d", len(list))
	}
	if _, err := s.CreateChannel(p.ID, "general", "", ""); err == nil {
		t.Fatal("duplicate channel name in project must error")
	}
}

func TestMessagesSinceAndArtifacts(t *testing.T) {
	s := open(t)
	p, _ := s.CreateProject("x", "/x")
	c, _ := s.CreateChannel(p.ID, "general", "", "")
	var last Message
	for _, body := range []string{"one", "two", "three"} {
		m, err := s.CreateMessage(Message{ChannelID: c.ID, Kind: "message", AuthorKind: "human", Body: body})
		if err != nil {
			t.Fatal(err)
		}
		last = m
	}
	a, err := s.AddArtifact(Artifact{MessageID: last.ID, Filename: "r.md", Path: "/data/r.md", Size: 5})
	if err != nil || a.ID == 0 {
		t.Fatalf("AddArtifact: %v", err)
	}
	if a.CreatedAt.IsZero() {
		t.Fatal("AddArtifact: CreatedAt not populated from DB")
	}
	msgs, err := s.MessagesSince(c.ID, 0, 100)
	if err != nil || len(msgs) != 3 {
		t.Fatalf("MessagesSince all: %v len=%d", err, len(msgs))
	}
	if msgs[0].Body != "one" {
		t.Error("messages must come back oldest-first")
	}
	msgs, _ = s.MessagesSince(c.ID, msgs[0].ID, 100)
	if len(msgs) != 2 || msgs[0].Body != "two" {
		t.Fatalf("since filter wrong: %+v", msgs)
	}
	msgs, _ = s.MessagesSince(c.ID, 0, 2)
	if len(msgs) != 2 {
		t.Fatalf("limit ignored: len=%d", len(msgs))
	}
	arts, _ := s.ArtifactsByMessage(last.ID)
	if len(arts) != 1 || arts[0].Filename != "r.md" {
		t.Fatalf("ArtifactsByMessage: %+v", arts)
	}
}

func TestRunLifecycle(t *testing.T) {
	s := open(t)
	p, _ := s.CreateProject("x", "/x")
	c, _ := s.CreateChannel(p.ID, "general", "", "")
	r, err := s.CreateRun(AgentRun{
		ChannelID: c.ID, Provider: "codex", AgentName: "impl",
		Workdir: "/x", Status: "starting", Spawner: "tmux",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Token) != 32 {
		t.Fatalf("token %q, want 32 hex chars", r.Token)
	}
	got, ok, _ := s.RunByToken(r.Token)
	if !ok || got.ID != r.ID {
		t.Fatal("RunByToken failed")
	}
	running, _ := s.RunningRuns()
	if len(running) != 1 {
		t.Fatalf("RunningRuns len = %d (starting counts as running)", len(running))
	}
	if err := s.FinishRun(r.ID, "done", 0); err != nil {
		t.Fatal(err)
	}
	got, _, _ = s.RunByID(r.ID)
	if got.Status != "done" || !got.HasExit || got.ExitCode != 0 || got.FinishedAt.IsZero() {
		t.Fatalf("after FinishRun: %+v", got)
	}
	running, _ = s.RunningRuns()
	if len(running) != 0 {
		t.Fatal("finished run still listed as running")
	}

	r2, err := s.CreateRun(AgentRun{
		ChannelID: c.ID, Provider: "codex", AgentName: "impl2",
		Workdir: "/x", Status: "starting", Spawner: "tmux",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.StartRun(r2.ID, "x:1"); err != nil {
		t.Fatal(err)
	}
	got2, _, _ := s.RunByID(r2.ID)
	if got2.Status != "running" || got2.TmuxTarget != "x:1" {
		t.Fatalf("after StartRun: %+v", got2)
	}
}

func TestArtifactByID(t *testing.T) {
	s := open(t)
	p, _ := s.CreateProject("x", "/x")
	c, _ := s.CreateChannel(p.ID, "general", "", "")
	m, _ := s.CreateMessage(Message{ChannelID: c.ID, Kind: "report", AuthorKind: "human", Body: "b"})
	a, _ := s.AddArtifact(Artifact{MessageID: m.ID, Filename: "f.md", Path: "/data/f.md", Size: 2})
	got, ok, err := s.ArtifactByID(a.ID)
	if err != nil || !ok || got.Filename != "f.md" {
		t.Fatalf("ArtifactByID: %v %v %+v", err, ok, got)
	}
	if _, ok, _ := s.ArtifactByID(999); ok {
		t.Fatal("unknown id must be ok=false")
	}
}
