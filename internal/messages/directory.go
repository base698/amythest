package messages

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

// RegisterConversations imports a provider's directory without inventing
// incoming messages or putting quiet contacts into the attention queue.
func (s *Store) RegisterConversations(provider, account string, conversations []Conversation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(conversations) > 2000 {
		return fault(400, "too many directory entries")
	}
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	connector, e := getJSON[Connector](tx, "SELECT payload FROM connectors WHERE account=?", connectorKey(provider, account))
	if e != nil {
		return fault(403, "connector registration required")
	}
	for _, c := range conversations {
		if c.Route.Provider != provider || c.Route.Account != account || !connector.allows(c.Route.Chat) {
			return fault(403, "directory entry outside connector scope")
		}
		if e = validateNativeConversation(c); e != nil {
			return e
		}
		if !validID(c.ID) || len(c.Name) > 300 || len(c.Account) > 200 || len(c.Recipient) > 300 {
			return fault(400, "invalid directory entry")
		}
		c.Name = Plain(c.Name)
		hash := sha256.Sum256([]byte(c.ID))
		c.Channel = "#amy-" + hex.EncodeToString(hash[:6])
		old, readError := getJSON[Conversation](tx, "SELECT payload FROM conversations WHERE id=?", c.ID)
		if readError == nil {
			check := c
			check.Name = old.Name
			if check.Route.Provider == "imessage" && (check.Service == "Messages" || check.Service == "iMessage") && (old.Service == "Messages" || old.Service == "iMessage") {
				check.Service = old.Service
			}
			if check != old {
				return fault(409, "stored directory destination cannot change")
			}
		} else {
			var f *Fault
			if !errors.As(readError, &f) || f.Status != 404 {
				return readError
			}
		}
		if _, e = tx.Exec("INSERT INTO conversations VALUES(?,?) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload", c.ID, encode(c)); e != nil {
			return e
		}
	}
	return tx.Commit()
}
