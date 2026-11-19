package messages

import (
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// Listing is one conversation in the directory a client browses: every
// conversation the gateway knows, including channels and contacts a
// connector registered that have no messages yet.
type Listing struct {
	Conversation Conversation `json:"conversation"`
	// Zero when the conversation has no messages here yet.
	LastActivity time.Time `json:"last_activity,omitzero"`
	// Kind is channel, group or direct (empty when unknown).
	Kind string `json:"kind,omitempty"`
	// Parent is the channel a Slack thread belongs to.
	Parent string `json:"parent,omitempty"`
}

// Conversations lists every conversation the actor may see, by service and
// account, then most recent activity first. Trashed ones are left out.
func (s *Store) Conversations(actor string) ([]Listing, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	latest, e := latestKeys(s.db, false)
	if e != nil {
		return nil, e
	}
	rows, e := s.db.Query("SELECT payload FROM conversations WHERE id NOT IN (SELECT conversation FROM conversation_trash)")
	if e != nil {
		return nil, e
	}
	var convs []Conversation
	for rows.Next() {
		var raw string
		if e = rows.Scan(&raw); e != nil {
			rows.Close()
			return nil, e
		}
		var c Conversation
		if err := json.Unmarshal([]byte(raw), &c); err != nil {
			rows.Close()
			return nil, err
		}
		convs = append(convs, c)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	out := make([]Listing, 0, len(convs))
	for _, c := range convs {
		if permitted(s.db, actor, c.ID, false, false, s.now()) != nil {
			continue
		}
		l := Listing{Conversation: c, Kind: conversationKind(c)}
		if key, ok := latest[c.ID]; ok {
			if at, _, err := splitOrderKey(key); err == nil {
				l.LastActivity = at
			}
		}
		if p, ok := parentConversation(c); ok {
			l.Parent = p
			l.Kind = "thread"
		}
		out = append(out, l)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Conversation.Service != b.Conversation.Service {
			return a.Conversation.Service < b.Conversation.Service
		}
		if a.Conversation.Account != b.Conversation.Account {
			return a.Conversation.Account < b.Conversation.Account
		}
		if !a.LastActivity.Equal(b.LastActivity) {
			return a.LastActivity.After(b.LastActivity)
		}
		return strings.ToLower(a.Conversation.Name) < strings.ToLower(b.Conversation.Name)
	})
	return out, nil
}

// conversationKind reads what it can from provider identifiers.
func conversationKind(c Conversation) string {
	chat := c.Route.Chat
	switch c.Route.Provider {
	case "slack":
		switch {
		case strings.HasPrefix(chat, "D"):
			return "direct"
		case strings.HasPrefix(chat, "G"):
			return "group"
		case strings.HasPrefix(chat, "C"):
			return "channel"
		}
	case "discord":
		if strings.HasPrefix(chat, "dm:") {
			return "direct"
		}
		return "channel"
	case "signal":
		if strings.HasPrefix(chat, "group/") {
			return "group"
		}
		return "direct"
	case "imessage":
		// Chat GUIDs are SERVICE;+;chatN for groups, SERVICE;-;HANDLE for one person.
		if strings.Contains(chat, ";+;") {
			return "group"
		}
		return "direct"
	case "telegram":
		if strings.HasPrefix(chat, "-") {
			return "group"
		}
		return "direct"
	}
	return ""
}
