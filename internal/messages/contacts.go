package messages

import "strings"

// Contact names are owner overrides, separate from immutable provider routes.
// Reingest/contact sync cannot erase a name or redirect a reply.
func (s *Store) NameContact(conversation, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	name = strings.TrimSpace(name)
	if len(name) > 200 || strings.ContainsAny(name, "\r\n") || name != Plain(name) {
		return fault(400, "use a contact name of at most 200 characters")
	}
	if _, err := getJSON[Conversation](s.db, "SELECT payload FROM conversations WHERE id=?", conversation); err != nil {
		return err
	}
	if name == "" {
		_, e := s.db.Exec("DELETE FROM contact_names WHERE conversation=?", conversation)
		return e
	}
	_, e := s.db.Exec("INSERT INTO contact_names VALUES(?,?) ON CONFLICT(conversation) DO UPDATE SET name=excluded.name", conversation, name)
	return e
}
func (s *Store) named(c Conversation) Conversation {
	var name string
	if s.db.QueryRow("SELECT name FROM contact_names WHERE conversation=?", c.ID).Scan(&name) == nil {
		c.Name = name
	}
	return c
}
func (s *Store) ContactNames() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, e := s.db.Query("SELECT DISTINCT name FROM contact_names ORDER BY name COLLATE NOCASE")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	names := []string{}
	for rows.Next() {
		var name string
		if e = rows.Scan(&name); e != nil {
			return nil, e
		}
		names = append(names, name)
	}
	return names, rows.Err()
}
