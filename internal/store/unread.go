package store

// UnreadInfo describes a channel's messages newer than its last-read mark.
// Attention is set when the unread batch contains a report or system
// message — the cases that ask for the user's eyes, not just chatter.
type UnreadInfo struct {
	Count     int64
	Attention bool
}

// UnreadByChannel returns unread info for every channel that has any,
// keyed by channel ID. Channels with nothing unread are absent.
func (s *Store) UnreadByChannel() (map[int64]UnreadInfo, error) {
	rows, err := s.db.Query(`SELECT m.channel_id, COUNT(*),
			MAX(m.kind IN ('report','system'))
		FROM messages m JOIN channels c ON c.id = m.channel_id
		WHERE m.id > c.last_read_message_id
		GROUP BY m.channel_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]UnreadInfo{}
	for rows.Next() {
		var chID int64
		var info UnreadInfo
		if err := rows.Scan(&chID, &info.Count, &info.Attention); err != nil {
			return nil, err
		}
		out[chID] = info
	}
	return out, rows.Err()
}

// ActiveRunCountByChannel counts starting/running runs per channel, keyed
// by channel ID; channels with none are absent. Feeds the presence dots in
// the sidebar and the per-project agent badges.
func (s *Store) ActiveRunCountByChannel() (map[int64]int64, error) {
	rows, err := s.db.Query(`SELECT channel_id, COUNT(*) FROM agent_runs
		WHERE status IN ('starting','running') GROUP BY channel_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int64{}
	for rows.Next() {
		var chID, n int64
		if err := rows.Scan(&chID, &n); err != nil {
			return nil, err
		}
		out[chID] = n
	}
	return out, rows.Err()
}

// MarkChannelRead moves a channel's last-read mark to its newest message.
func (s *Store) MarkChannelRead(channelID int64) error {
	_, err := s.db.Exec(`UPDATE channels SET last_read_message_id =
		(SELECT COALESCE(MAX(id), 0) FROM messages WHERE channel_id = ?)
		WHERE id = ?`, channelID, channelID)
	return err
}
