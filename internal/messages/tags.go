package messages

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"
	"unicode"
)

// A message can be put on a project's wall: the owner tags it with the
// project (the wall's name for a herdr workspace), and the wall lists the
// project's tagged messages among its items. Only the owner tags or reads
// tags; agents see neither.

type Tag struct {
	Message      string    `json:"message"`
	Conversation string    `json:"conversation"`
	Project      string    `json:"project"`
	At           time.Time `json:"at"`
}

// Tagged is a tagged message with what a wall needs to show it.
type Tagged struct {
	Tag          Tag          `json:"tag"`
	Message      Message      `json:"message"`
	Conversation Conversation `json:"conversation"`
}

const tagSchema = `
CREATE TABLE IF NOT EXISTS message_tags(message TEXT NOT NULL, project TEXT NOT NULL, created TEXT NOT NULL, PRIMARY KEY(message, project));
CREATE INDEX IF NOT EXISTS message_tags_project ON message_tags(project, created);`

func openTags(db *sql.DB) error {
	_, e := db.Exec(tagSchema)
	return e
}

func validProject(p string) bool {
	if p == "" || len(p) > 200 || strings.TrimSpace(p) != p {
		return false
	}
	for _, r := range p {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// TagMessage puts a message on a project's wall. Tagging twice is harmless.
func (s *Store) TagMessage(actor, message, project string) (Tag, error) {
	if actor != "owner" {
		return Tag{}, fault(403, "only the owner tags messages")
	}
	if !validProject(project) {
		return Tag{}, fault(400, "a tag needs a project name")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m, e := getJSON[Message](s.db, "SELECT payload FROM messages WHERE id=?", message)
	if e != nil {
		return Tag{}, e
	}
	t := Tag{Message: m.ID, Conversation: m.Conversation, Project: project, At: s.now().UTC()}
	if _, e = s.db.Exec("INSERT INTO message_tags VALUES(?,?,?) ON CONFLICT DO NOTHING", t.Message, t.Project, occurredKey(t.At)); e != nil {
		return Tag{}, e
	}
	var at string
	if e = s.db.QueryRow("SELECT created FROM message_tags WHERE message=? AND project=?", t.Message, t.Project).Scan(&at); e == nil {
		t.At, _ = time.Parse(occurredLayout, at)
	}
	return t, e
}

// Untag takes a message off a project's wall.
func (s *Store) Untag(actor, message, project string) error {
	if actor != "owner" {
		return fault(403, "only the owner tags messages")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, e := s.db.Exec("DELETE FROM message_tags WHERE message=? AND project=?", message, project)
	return e
}

// Tagged lists a project's tagged messages, most recently tagged first.
func (s *Store) Tagged(actor, project string, limit int) ([]Tagged, error) {
	if actor != "owner" {
		return nil, fault(403, "only the owner reads tags")
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, e := s.db.Query(`SELECT t.message, t.created, m.payload, c.payload FROM message_tags t
		JOIN messages m ON m.id = t.message JOIN conversations c ON c.id = m.conversation
		WHERE t.project=? AND m.conversation NOT IN (SELECT conversation FROM conversation_trash)
		ORDER BY t.created DESC LIMIT ?`, project, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Tagged{}
	for rows.Next() {
		var id, at, mraw, craw string
		if e = rows.Scan(&id, &at, &mraw, &craw); e != nil {
			return nil, e
		}
		m, err := decodeMessage(mraw)
		if err != nil {
			return nil, err
		}
		var c Conversation
		if err = json.Unmarshal([]byte(craw), &c); err != nil {
			return nil, err
		}
		when, _ := time.Parse(occurredLayout, at)
		out = append(out, Tagged{Tag: Tag{Message: id, Conversation: m.Conversation, Project: project, At: when}, Message: m, Conversation: c})
	}
	return out, rows.Err()
}

// tagThread marks which shown messages are on which walls (owner only).
func (s *Store) tagThread(actor string, t *Thread) error {
	if actor != "owner" || len(t.Messages) == 0 {
		return nil
	}
	ids := make([]any, len(t.Messages))
	for i, m := range t.Messages {
		ids[i] = m.ID
	}
	rows, e := s.db.Query("SELECT message, project FROM message_tags WHERE message IN (?"+strings.Repeat(",?", len(ids)-1)+") ORDER BY created", ids...)
	if e != nil {
		return e
	}
	defer rows.Close()
	for rows.Next() {
		var id, p string
		if e = rows.Scan(&id, &p); e != nil {
			return e
		}
		if t.Tags == nil {
			t.Tags = map[string][]string{}
		}
		t.Tags[id] = append(t.Tags[id], p)
	}
	return rows.Err()
}

// Milestone is a Work milestone a message can be filed under.
type Milestone struct {
	ID   string
	Name string
	Open bool
}

// Milestones lets a Messages view file a message under one of a project's
// milestones as well as putting it on the project's wall. The views take it
// from their caller, since milestones live on the work server.
type Milestones struct {
	// List is a project's milestones, open ones first, newest first.
	List func(ctx context.Context, project string) ([]Milestone, error)
	// Link files a message under a milestone.
	Link func(ctx context.Context, milestone, message string) error
}

// FindMilestone picks a milestone by name (any case), preferring an open one.
func FindMilestone(list []Milestone, name string) (Milestone, bool) {
	var found Milestone
	ok := false
	for _, m := range list {
		if strings.EqualFold(m.Name, name) && (!ok || m.Open && !found.Open) {
			found, ok = m, true
		}
	}
	return found, ok
}
