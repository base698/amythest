package messages

// SetTrashed hides or restores a central conversation without modifying its
// provider. Incoming messages do not untrash a known spam conversation.
func (s *Store) SetTrashed(conversation string, trashed bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, e := getJSON[Conversation](s.db, "SELECT payload FROM conversations WHERE id=?", conversation); e != nil {
		return e
	}
	var e error
	if trashed {
		_, e = s.db.Exec("INSERT OR IGNORE INTO conversation_trash VALUES(?)", conversation)
	} else {
		_, e = s.db.Exec("DELETE FROM conversation_trash WHERE conversation=?", conversation)
	}
	return e
}
