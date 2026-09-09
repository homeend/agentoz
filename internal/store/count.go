package store

// MessageCount is how many messages a channel holds (all kinds).
func (s *Store) MessageCount(channelID int64) (int64, error) {
	var n int64
	err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE channel_id = ?`, channelID).Scan(&n)
	return n, err
}
