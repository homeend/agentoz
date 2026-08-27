package store

import "testing"

// seedProject builds a project with one channel, a run, a report message
// carrying an artifact, and an internal forward of that report (copy in a
// second channel of the same project). Returns the pieces tests reference.
func seedProject(t *testing.T, s *Store, name, path string) (p Project, ch Channel, run AgentRun, report Message) {
	t.Helper()
	p, err := s.CreateProject(name, path)
	if err != nil {
		t.Fatal(err)
	}
	ch, err = s.CreateChannel(p.ID, "general", path, "main")
	if err != nil {
		t.Fatal(err)
	}
	run, err = s.CreateRun(AgentRun{ChannelID: ch.ID, Provider: "claude-code",
		AgentName: "a", Workdir: path, Status: "running", Spawner: "tmux"})
	if err != nil {
		t.Fatal(err)
	}
	report, err = s.CreateMessage(Message{ChannelID: ch.ID, Kind: "report",
		AuthorKind: "agent", AuthorName: "a", AgentRunID: run.ID, Body: "done"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddArtifact(Artifact{MessageID: report.ID, Filename: "out.txt", Path: "/data/out.txt"}); err != nil {
		t.Fatal(err)
	}
	// Internal forward: copy of the report into a second channel of the
	// same project — its origin_message_id points inside the deletion set.
	ch2, err := s.CreateChannel(p.ID, "feature", path, "feat")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateMessage(Message{ChannelID: ch2.ID, Kind: "report",
		AuthorKind: "agent", AuthorName: "a", OriginMessageID: report.ID, Body: "done"}); err != nil {
		t.Fatal(err)
	}
	return p, ch, run, report
}

func TestDeleteProjectCascades(t *testing.T) {
	s := open(t)
	p, ch, run, report := seedProject(t, s, "gone", "/gone")

	// Internal handoff: a run in the same project spawned from the report.
	// Messages are deleted before agent_runs, so an un-nulled internal
	// run->message backlink would trip the FK mid-transaction.
	ch2, _, err := s.ChannelByName(p.ID, "feature")
	if err != nil {
		t.Fatal(err)
	}
	handoff, err := s.CreateRun(AgentRun{ChannelID: ch2.ID, Provider: "codex",
		AgentName: "b", Workdir: "/gone", Status: "done", Spawner: "tmux", OriginMessageID: report.ID})
	if err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteProject(p.ID); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	if _, ok, _ := s.ProjectByID(p.ID); ok {
		t.Fatal("project still present")
	}
	if chans, _ := s.ChannelsByProject(p.ID); len(chans) != 0 {
		t.Fatalf("channels remain: %d", len(chans))
	}
	if msgs, _ := s.MessagesSince(ch.ID, 0, 100); len(msgs) != 0 {
		t.Fatalf("messages remain: %d", len(msgs))
	}
	if runs, _ := s.RunsByChannel(ch.ID); len(runs) != 0 {
		t.Fatalf("runs remain: %d", len(runs))
	}
	if _, ok, _ := s.RunByID(run.ID); ok {
		t.Fatal("run still present")
	}
	if _, ok, _ := s.RunByID(handoff.ID); ok {
		t.Fatal("internal handoff run still present")
	}
	if arts, _ := s.ArtifactsByMessage(report.ID); len(arts) != 0 {
		t.Fatalf("artifacts remain: %d", len(arts))
	}
}

func TestDeleteProjectNullsExternalBacklinks(t *testing.T) {
	s := open(t)
	p, _, run, report := seedProject(t, s, "src", "/src")

	// Other project holding backlinks into p: a forwarded copy and a
	// handoff-spawned run, plus a stray message crediting p's run.
	other, _ := s.CreateProject("keep", "/keep")
	och, _ := s.CreateChannel(other.ID, "general", "/keep", "main")
	fwd, err := s.CreateMessage(Message{ChannelID: och.ID, Kind: "report",
		AuthorKind: "agent", AuthorName: "a", OriginMessageID: report.ID, Body: "done"})
	if err != nil {
		t.Fatal(err)
	}
	orun, err := s.CreateRun(AgentRun{ChannelID: och.ID, Provider: "codex",
		AgentName: "b", Workdir: "/keep", Status: "done", Spawner: "tmux", OriginMessageID: report.ID})
	if err != nil {
		t.Fatal(err)
	}
	stray, err := s.CreateMessage(Message{ChannelID: och.ID, Kind: "message",
		AuthorKind: "agent", AuthorName: "a", AgentRunID: run.ID, Body: "hi"})
	if err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteProject(p.ID); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	if m, ok, _ := s.MessageByID(fwd.ID); !ok || m.OriginMessageID != 0 {
		t.Fatalf("forwarded copy: ok=%v origin=%d", ok, m.OriginMessageID)
	}
	if r, ok, _ := s.RunByID(orun.ID); !ok || r.OriginMessageID != 0 {
		t.Fatalf("handoff run: ok=%v origin=%d", ok, r.OriginMessageID)
	}
	if m, ok, _ := s.MessageByID(stray.ID); !ok || m.AgentRunID != 0 {
		t.Fatalf("stray message: ok=%v run=%d", ok, m.AgentRunID)
	}
	if _, ok, _ := s.ProjectByID(other.ID); !ok {
		t.Fatal("other project must survive")
	}
}

func TestDeleteProjectMissing(t *testing.T) {
	s := open(t)
	if err := s.DeleteProject(999); err != nil {
		t.Fatalf("deleting a missing project should be a no-op, got %v", err)
	}
}

func TestProjectStats(t *testing.T) {
	s := open(t)
	p, _, _, _ := seedProject(t, s, "st", "/st")
	got, err := s.ProjectStats(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := ProjectStatsRow{Channels: 2, Messages: 2, Artifacts: 1, Runs: 1, ActiveRuns: 1}
	if got != want {
		t.Fatalf("stats = %+v, want %+v", got, want)
	}
}

func TestProjectCleanupIDs(t *testing.T) {
	s := open(t)
	p, _, run, report := seedProject(t, s, "cl", "/cl")
	msgIDs, runIDs, err := s.ProjectCleanupIDs(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgIDs) != 1 || msgIDs[0] != report.ID {
		t.Fatalf("artifact message ids = %v, want [%d]", msgIDs, report.ID)
	}
	if len(runIDs) != 1 || runIDs[0] != run.ID {
		t.Fatalf("run ids = %v, want [%d]", runIDs, run.ID)
	}
}

func TestActiveRunsByProject(t *testing.T) {
	s := open(t)
	p, _, run, _ := seedProject(t, s, "ar", "/ar")
	if err := s.FinishRun(run.ID, "done", 0); err != nil {
		t.Fatal(err)
	}
	ch2, _, _ := s.ChannelByName(p.ID, "feature")
	live, err := s.CreateRun(AgentRun{ChannelID: ch2.ID, Provider: "claude-code",
		AgentName: "c", Workdir: "/ar", Status: "starting", Spawner: "tmux"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.ActiveRunsByProject(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != live.ID {
		t.Fatalf("active runs = %+v, want just id %d", got, live.ID)
	}
}
