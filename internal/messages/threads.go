package messages

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"strings"
)

// Slack keeps a thread's replies in a conversation of their own: the
// channel's route plus the root message's timestamp. That is the same ID
// the Slack import computes, so replies sent from Amy and replies imported
// later land in one conversation.

// ReplySummary says that a message has a thread and where it is.
type ReplySummary struct {
	Conversation string `json:"conversation"`
	Count        int    `json:"count"`
}

// ThreadParent is where a thread conversation hangs from. Message is empty
// when the root message itself was never imported.
type ThreadParent struct {
	Conversation string `json:"conversation"`
	Message      string `json:"message,omitempty"`
}

const demoThreadSep = "/thread/"

// isThread reports whether c is a Slack thread. Demo conversations use
// Thread as a display label, so theirs are known by ID instead.
func isThread(c Conversation) bool {
	if c.Demo {
		return strings.Contains(c.ID, demoThreadSep)
	}
	return c.Route.Thread != "" || c.Thread != ""
}

// threadConversation is the conversation holding replies to root, which is a
// top-level message in c. ok is false where threads do not apply.
func threadConversation(c Conversation, root Message) (Conversation, bool) {
	if c.Service != "Slack" || isThread(c) || root.NativeID == "" {
		return Conversation{}, false
	}
	t := c
	t.Thread = root.NativeID
	t.Route.Thread = root.NativeID
	if c.Demo {
		t.ID = c.ID + demoThreadSep + root.NativeID
	} else {
		if c.Route.Provider != "slack" || !slackTimestampRE.MatchString(root.NativeID) {
			return Conversation{}, false
		}
		t.ID = SlackConversationID(t.Route)
	}
	h := sha256.Sum256([]byte(t.ID))
	t.Channel = "#amy-" + hex.EncodeToString(h[:6])
	return t, validID(t.ID)
}

// parentConversation is the channel or DM a thread conversation belongs to.
func parentConversation(c Conversation) (string, bool) {
	if c.Service != "Slack" || !isThread(c) {
		return "", false
	}
	if c.Demo {
		i := strings.LastIndex(c.ID, demoThreadSep)
		if i <= 0 {
			return "", false
		}
		return c.ID[:i], true
	}
	if c.Route.Provider != "slack" {
		return "", false
	}
	r := c.Route
	r.Thread = ""
	return SlackConversationID(r), true
}

// annotate adds reply counts to a channel's messages, or the parent to a
// thread. Only the owner sees them: an agent's grant covers one conversation,
// not the threads around it.
func (s *Store) annotate(actor string, t *Thread) error {
	if actor != "owner" || t.Conversation.Service != "Slack" {
		return nil
	}
	if parent, ok := parentConversation(t.Conversation); ok {
		var exists int
		if e := s.db.QueryRow("SELECT count(*) FROM conversations WHERE id=?", parent).Scan(&exists); e != nil || exists == 0 {
			return e
		}
		t.Parent = &ThreadParent{Conversation: parent}
		var id string
		e := s.db.QueryRow("SELECT id FROM messages WHERE conversation=? AND native_id=?", parent, t.Conversation.Route.Thread).Scan(&id)
		if e == nil {
			t.Parent.Message = id
		} else if e != sql.ErrNoRows {
			return e
		}
		return nil
	}
	byThread := map[string]string{} // thread conversation -> root message
	args := []any{}
	for _, m := range t.Messages {
		if tc, ok := threadConversation(t.Conversation, m); ok {
			byThread[tc.ID] = m.ID
			args = append(args, tc.ID)
		}
	}
	if len(args) == 0 {
		return nil
	}
	rows, e := s.db.Query("SELECT conversation, count(*) FROM messages WHERE conversation IN (?"+strings.Repeat(",?", len(args)-1)+") GROUP BY conversation", args...)
	if e != nil {
		return e
	}
	defer rows.Close()
	for rows.Next() {
		var conv string
		var n int
		if e = rows.Scan(&conv, &n); e != nil {
			return e
		}
		if t.Replies == nil {
			t.Replies = map[string]ReplySummary{}
		}
		t.Replies[byThread[conv]] = ReplySummary{Conversation: conv, Count: n}
	}
	return rows.Err()
}

// startThread is the conversation a reply in root's thread goes to, created
// if this is the thread's first reply. Only the owner starts threads: an
// agent's grant does not reach a conversation that does not exist yet.
func (s *Store) startThread(actor string, c Conversation, root Message) (Conversation, error) {
	if isThread(c) {
		return c, nil // already a thread: reply in it
	}
	if actor != "owner" {
		return Conversation{}, fault(403, "only the owner can start a thread")
	}
	t, ok := threadConversation(c, root)
	if !ok {
		return Conversation{}, fault(400, "threads are only available for Slack channel and DM messages")
	}
	if !t.Demo {
		if e := validateNativeConversation(t); e != nil {
			return Conversation{}, e
		}
	}
	if old, e := getJSON[Conversation](s.db, "SELECT payload FROM conversations WHERE id=?", t.ID); e == nil {
		return old, nil // imported or started before
	}
	_, e := s.db.Exec("INSERT INTO conversations VALUES(?,?) ON CONFLICT(id) DO NOTHING", t.ID, encode(t))
	return t, e
}
