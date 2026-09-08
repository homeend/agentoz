package store

// projectMsgs / projectRuns select the deletion set for a project; used as
// subqueries so the whole cascade runs inside one transaction.
const (
	projectChans = `SELECT id FROM channels WHERE project_id = ?`
	projectMsgs  = `SELECT id FROM messages WHERE channel_id IN (` + projectChans + `)`
	projectRuns  = `SELECT id FROM agent_runs WHERE channel_id IN (` + projectChans + `)`
)

// DeleteProject removes a project and its channels, messages, artifact rows
// and runs in one transaction. The schema has no ON DELETE CASCADE and
// foreign_keys is on, so children go first — and because forwards and
// handoffs create message<->run backlinks (including from other projects),
// every origin_message_id / agent_run_id pointing into the deletion set is
// nulled unconditionally before the deletes. Deleting a missing project is
// a no-op.
func (s *Store) DeleteProject(projectID int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmts := []string{
		`UPDATE messages SET origin_message_id = NULL WHERE origin_message_id IN (` + projectMsgs + `)`,
		`UPDATE agent_runs SET origin_message_id = NULL WHERE origin_message_id IN (` + projectMsgs + `)`,
		`UPDATE messages SET agent_run_id = NULL WHERE agent_run_id IN (` + projectRuns + `)`,
		`DELETE FROM artifacts WHERE message_id IN (` + projectMsgs + `)`,
		`DELETE FROM messages WHERE channel_id IN (` + projectChans + `)`,
		`DELETE FROM agent_runs WHERE channel_id IN (` + projectChans + `)`,
		`DELETE FROM channels WHERE project_id = ?`,
		`DELETE FROM projects WHERE id = ?`,
	}
	for _, q := range stmts {
		if _, err := tx.Exec(q, projectID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ProjectStatsRow counts what a delete would remove; shown on the
// confirmation page.
type ProjectStatsRow struct {
	Channels   int64
	Messages   int64
	Artifacts  int64
	Runs       int64
	ActiveRuns int64
}

func (s *Store) ProjectStats(projectID int64) (ProjectStatsRow, error) {
	var st ProjectStatsRow
	err := s.db.QueryRow(`SELECT
		(SELECT COUNT(*) FROM channels WHERE project_id = ?),
		(SELECT COUNT(*) FROM messages WHERE channel_id IN (`+projectChans+`)),
		(SELECT COUNT(*) FROM artifacts WHERE message_id IN (`+projectMsgs+`)),
		(SELECT COUNT(*) FROM agent_runs WHERE channel_id IN (`+projectChans+`)),
		(SELECT COUNT(*) FROM agent_runs WHERE channel_id IN (`+projectChans+`)
			AND status IN ('starting','running'))`,
		projectID, projectID, projectID, projectID, projectID).
		Scan(&st.Channels, &st.Messages, &st.Artifacts, &st.Runs, &st.ActiveRuns)
	return st, err
}

// ProjectCleanupIDs lists the message IDs that own artifacts and the run
// IDs of a project — the on-disk directories (dataDir/artifacts/<msgID>,
// dataDir/runs/<runID>) the server removes after a delete commits.
func (s *Store) ProjectCleanupIDs(projectID int64) (artifactMsgIDs, runIDs []int64, err error) {
	artifactMsgIDs, err = s.idList(
		`SELECT DISTINCT message_id FROM artifacts WHERE message_id IN (`+projectMsgs+`) ORDER BY message_id`, projectID)
	if err != nil {
		return nil, nil, err
	}
	runIDs, err = s.idList(projectRuns+` ORDER BY id`, projectID)
	if err != nil {
		return nil, nil, err
	}
	return artifactMsgIDs, runIDs, nil
}

func (s *Store) idList(query string, args ...any) ([]int64, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ActiveRunsByProject lists a project's starting/running runs — the ones a
// delete stops before removing their rows.
func (s *Store) ActiveRunsByProject(projectID int64) ([]AgentRun, error) {
	return s.runsWhere(`WHERE channel_id IN (`+projectChans+`)
		AND status IN ('starting','running') ORDER BY id`, projectID)
}

// Channel-scoped deletion set, mirroring the project one.
const (
	chanMsgs = `SELECT id FROM messages WHERE channel_id = ?`
	chanRuns = `SELECT id FROM agent_runs WHERE channel_id = ?`
)

// DeleteChannel removes one channel with its messages, artifact rows and
// runs in one transaction, nulling every backlink into the deletion set
// first (same reasoning as DeleteProject). Deleting a missing channel is a
// no-op. Callers decide whether the channel may go (the UI only offers it
// for archived channels).
func (s *Store) DeleteChannel(channelID int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmts := []string{
		`UPDATE messages SET origin_message_id = NULL WHERE origin_message_id IN (` + chanMsgs + `)`,
		`UPDATE agent_runs SET origin_message_id = NULL WHERE origin_message_id IN (` + chanMsgs + `)`,
		`UPDATE messages SET agent_run_id = NULL WHERE agent_run_id IN (` + chanRuns + `)`,
		`DELETE FROM artifacts WHERE message_id IN (` + chanMsgs + `)`,
		`DELETE FROM messages WHERE channel_id = ?`,
		`DELETE FROM agent_runs WHERE channel_id = ?`,
		`DELETE FROM channels WHERE id = ?`,
	}
	for _, q := range stmts {
		if _, err := tx.Exec(q, channelID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ChannelCleanupIDs: the on-disk directories a channel delete removes
// after commit (see ProjectCleanupIDs).
func (s *Store) ChannelCleanupIDs(channelID int64) (artifactMsgIDs, runIDs []int64, err error) {
	artifactMsgIDs, err = s.idList(
		`SELECT DISTINCT message_id FROM artifacts WHERE message_id IN (`+chanMsgs+`) ORDER BY message_id`, channelID)
	if err != nil {
		return nil, nil, err
	}
	runIDs, err = s.idList(chanRuns+` ORDER BY id`, channelID)
	if err != nil {
		return nil, nil, err
	}
	return artifactMsgIDs, runIDs, nil
}
