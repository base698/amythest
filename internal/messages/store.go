package messages

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	mu  sync.Mutex
	db  *sql.DB
	now func() time.Time
}

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "messages.db")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	f.Close()
	if err = os.Chmod(path, 0600); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL; PRAGMA busy_timeout=5000;
 CREATE TABLE IF NOT EXISTS conversations(id TEXT PRIMARY KEY, payload TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS messages(id TEXT PRIMARY KEY, conversation TEXT NOT NULL, native_id TEXT NOT NULL, occurred TEXT NOT NULL, payload TEXT NOT NULL, UNIQUE(conversation,native_id));
 CREATE INDEX IF NOT EXISTS message_thread ON messages(conversation,occurred);
 CREATE TABLE IF NOT EXISTS attachments(id TEXT PRIMARY KEY, payload TEXT NOT NULL, data BLOB NOT NULL);
 CREATE TABLE IF NOT EXISTS drafts(id TEXT PRIMARY KEY, request_id TEXT UNIQUE NOT NULL, fingerprint TEXT NOT NULL, payload TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS grants(conversation TEXT PRIMARY KEY,payload TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS connectors(account TEXT PRIMARY KEY,payload TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS contact_names(conversation TEXT PRIMARY KEY,name TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS conversation_trash(conversation TEXT PRIMARY KEY);`)
	if err != nil {
		db.Close()
		return nil, err
	}
	if err = migrateOrder(db); err != nil {
		db.Close()
		return nil, err
	}
	if err = openTags(db); err != nil {
		db.Close()
		return nil, err
	}
	if err = openSearch(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, now: time.Now}, nil
}
func (s *Store) Close() error { return s.db.Close() }
func encode(v any) string     { b, _ := json.Marshal(v); return string(b) }

type querier interface{ QueryRow(string, ...any) *sql.Row }

func getJSON[T any](q querier, query string, args ...any) (T, error) {
	var v T
	var b string
	err := q.QueryRow(query, args...).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return v, fault(404, "item not found")
	}
	if err != nil {
		return v, err
	}
	err = json.Unmarshal([]byte(b), &v)
	return v, err
}
func permitted(q querier, actor, conversation string, send, attachments bool, now time.Time) error {
	if actor == "owner" {
		return nil
	}
	if actor != "agent" {
		return fault(403, "unknown identity")
	}
	g, e := getJSON[Grant](q, "SELECT payload FROM grants WHERE conversation=?", conversation)
	if e != nil || !g.Expires.After(now) {
		return fault(403, "agent has no active grant for this conversation")
	}
	if send && !g.Send {
		return fault(403, "agent may draft here; sending needs a grant")
	}
	if attachments && !g.Attachments {
		return fault(403, "this grant does not allow attachments")
	}
	return nil
}
func (s *Store) Ingest(in Ingress) (Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, m := in.Conversation, in.Message
	if !validID(c.ID) || !validID(m.NativeID) || len(c.Service) > 40 || len(c.Account) > 200 || len(c.Name) > 300 || len(c.Recipient) > 300 || len(c.Thread) > 300 || c.Service == "" || c.Account == "" || c.Recipient == "" || len(m.Body) > 20000 || len(m.Sender) > 200 {
		return Message{}, fault(400, "invalid message or destination")
	}
	if !validSourceURL(m.SourceURL) {
		return Message{}, fault(400, "invalid source URL")
	}
	if m.SenderID != "" && (c.Route.Provider != "slack" || !slackSenderRE.MatchString(m.SenderID)) {
		return Message{}, fault(400, "invalid sender ID")
	}
	if len(m.Attachments) > 8 {
		return Message{}, fault(400, "a message can carry up to 8 attachments")
	}
	tx, e := s.db.Begin()
	if e != nil {
		return Message{}, e
	}
	defer tx.Rollback()
	// Incoming files were uploaded first; the stored record, not the
	// sender's description, says what each one is.
	for i, a := range m.Attachments {
		stored, err := getJSON[Attachment](tx, "SELECT payload FROM attachments WHERE id=?", a.ID)
		if err != nil {
			return Message{}, fault(400, "unknown attachment; upload it before the message")
		}
		m.Attachments[i] = stored
	}
	// A provider ID is only unique within this account/conversation. Replays do not
	// reopen handled messages or overwrite a previously pinned destination.
	if old, e := getJSON[Message](tx, "SELECT payload FROM messages WHERE conversation=? AND native_id=?", c.ID, m.NativeID); e == nil {
		// A connector's correction replaces only the text of its own message,
		// in the same conversation and direction; nothing else changes.
		if in.Correct && c.Route.Provider != "" {
			stored, err := getJSON[Conversation](tx, "SELECT payload FROM conversations WHERE id=?", c.ID)
			if err != nil {
				return Message{}, err
			}
			if stored.Route != c.Route || old.Outgoing != m.Outgoing {
				return Message{}, fault(409, "a correction must match the stored message")
			}
			if old.Body == m.Body {
				return old, nil
			}
			old.Body = m.Body
			if _, err = tx.Exec("UPDATE messages SET payload=? WHERE id=?", encode(old), old.ID); err != nil {
				return Message{}, err
			}
			return old, tx.Commit()
		}
		// Files fetched after the message was first imported join it once,
		// from the same connector route; nothing else changes.
		if len(m.Attachments) > 0 && len(old.Attachments) == 0 && c.Route.Provider != "" {
			stored, err := getJSON[Conversation](tx, "SELECT payload FROM conversations WHERE id=?", c.ID)
			if err != nil {
				return Message{}, err
			}
			if stored.Route == c.Route && old.Outgoing == m.Outgoing {
				old.Attachments = m.Attachments
				if _, err = tx.Exec("UPDATE messages SET payload=? WHERE id=?", encode(old), old.ID); err != nil {
					return Message{}, err
				}
				if err = tx.Commit(); err != nil {
					return Message{}, err
				}
				return old, nil
			}
		}
		// Profile enrichment must not replace content, attention state or identity.
		if c.Route.Provider == "slack" && m.SenderID != "" {
			stored, err := getJSON[Conversation](tx, "SELECT payload FROM conversations WHERE id=?", c.ID)
			if err != nil {
				return Message{}, err
			}
			legacySender := old.SenderID == "" && (old.Sender == m.SenderID || old.Outgoing && m.Outgoing && old.Sender == "You")
			if stored.Route != c.Route || (old.SenderID != m.SenderID && !legacySender) {
				return Message{}, fault(409, "message sender or destination differs from the stored identity")
			}
			old.SenderID = m.SenderID
			if !old.Outgoing && m.Sender != "" && m.Sender != m.SenderID {
				old.Sender = m.Sender
			}
			if _, err = tx.Exec("UPDATE messages SET payload=? WHERE id=?", encode(old), old.ID); err != nil {
				return Message{}, err
			}
			return old, tx.Commit()
		}
		return old, nil
	} else if in.Correct {
		return Message{}, fault(404, "nothing to correct: the message was never imported")
	}
	h := sha256.Sum256([]byte(c.ID))
	c.Channel = "#amy-" + hex.EncodeToString(h[:6])
	old, e := getJSON[Conversation](tx, "SELECT payload FROM conversations WHERE id=?", c.ID)
	if e == nil {
		// Display names can change. The service/account/address remain immutable.
		check := c
		check.Name = old.Name
		if check.Route.Provider == "imessage" && (check.Service == "Messages" || check.Service == "iMessage") && (old.Service == "Messages" || old.Service == "iMessage") {
			check.Service = old.Service
		}
		if check != old {
			return Message{}, fault(409, "conversation destination differs from the stored destination")
		}
		if _, e = tx.Exec("UPDATE conversations SET payload=? WHERE id=?", encode(c), c.ID); e != nil {
			return Message{}, e
		}
	} else {
		var f *Fault
		if !errors.As(e, &f) || f.Status != 404 {
			return Message{}, e
		}
		if _, e = tx.Exec("INSERT INTO conversations VALUES(?,?)", c.ID, encode(c)); e != nil {
			return Message{}, e
		}
	}
	m.ID = ID()
	m.Conversation = c.ID
	m.Handled = m.Outgoing
	m.ReplyTo = ""
	m.SnoozedUntil = time.Time{}
	if m.At.IsZero() {
		m.At = s.now()
	}
	m.At = m.At.UTC()
	m.Category, m.Reason = classify(m.Body)
	if m.Outgoing {
		m.Sender = "You"
		m.Category, m.Reason = "Sent", "Your message in this conversation"
	}
	_, e = tx.Exec("INSERT INTO messages VALUES(?,?,?,?,?)", m.ID, c.ID, m.NativeID, occurredKey(m.At), encode(m))
	if e != nil {
		return Message{}, e
	}
	return m, tx.Commit()
}
func (s *Store) Items(actor string, all bool, trashMode ...bool) ([]Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	onlyTrash := len(trashMode) > 0 && trashMode[0]
	trashed := map[string]bool{}
	trashRows, e := s.db.Query("SELECT conversation FROM conversation_trash")
	if e != nil {
		return nil, e
	}
	for trashRows.Next() {
		var id string
		if e = trashRows.Scan(&id); e != nil {
			trashRows.Close()
			return nil, e
		}
		trashed[id] = true
	}
	e = trashRows.Err()
	trashRows.Close()
	if e != nil {
		return nil, e
	}
	// The latest message per conversation comes from the index; only the
	// messages that are shown are read and decoded.
	activity, e := latestKeys(s.db, false)
	if e != nil {
		return nil, e
	}
	latest, e := latestKeys(s.db, true)
	if e != nil {
		return nil, e
	}
	out := []Item{}
	for id, recentKey := range activity {
		key, incoming := latest[id]
		if all || onlyTrash {
			key = recentKey
		} else if !incoming {
			continue
		}
		if trashed[id] != onlyTrash {
			continue
		}
		recentAt, _, e := splitOrderKey(recentKey)
		if e != nil {
			return nil, e
		}
		_, rowid, e := splitOrderKey(key)
		if e != nil {
			return nil, e
		}
		m, e := messageAt(s.db, rowid)
		if e != nil {
			return nil, e
		}
		if !all && !onlyTrash && (m.Handled || m.SnoozedUntil.After(s.now())) {
			continue
		}
		if permitted(s.db, actor, m.Conversation, false, false, s.now()) != nil {
			continue
		}
		c, e := getJSON[Conversation](s.db, "SELECT payload FROM conversations WHERE id=?", m.Conversation)
		if e != nil {
			return nil, e
		}
		out = append(out, Item{Conversation: c, Message: m, LastActivity: recentAt})
	}
	if all || onlyTrash {
		rows, e := s.db.Query("SELECT payload FROM conversations")
		if e != nil {
			return nil, e
		}
		var contacts []Conversation
		for rows.Next() {
			var raw string
			if e = rows.Scan(&raw); e != nil {
				rows.Close()
				return nil, e
			}
			var c Conversation
			if e = json.Unmarshal([]byte(raw), &c); e != nil {
				rows.Close()
				return nil, e
			}
			contacts = append(contacts, c)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, e
		}
		for _, c := range contacts {
			if _, exists := activity[c.ID]; !exists && trashed[c.ID] == onlyTrash && permitted(s.db, actor, c.ID, false, false, s.now()) == nil {
				out = append(out, Item{Conversation: c, Message: Message{Conversation: c.ID, Category: "Contact", Reason: "No messages imported yet."}})
			}
		}
	}
	for i := range out {
		out[i].Conversation = s.named(out[i].Conversation)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].Message, out[j].Message
		if out[i].LastActivity.Equal(out[j].LastActivity) {
			if a.ID == "" && b.ID == "" {
				an, bn := strings.ToLower(out[i].Conversation.Name), strings.ToLower(out[j].Conversation.Name)
				if an != bn {
					return an < bn
				}
				return out[i].Conversation.ID < out[j].Conversation.ID
			}
			return a.ID < b.ID
		}
		return out[i].LastActivity.After(out[j].LastActivity)
	})
	return out, nil
}
func (s *Store) Thread(actor, conversation string) (Thread, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, e := s.thread(actor, conversation)
	if e != nil {
		return t, e
	}
	if e := s.tagThread(actor, &t); e != nil {
		return t, e
	}
	return t, s.annotate(actor, &t)
}
func (s *Store) thread(actor, conversation string) (Thread, error) {
	var out Thread
	if e := permitted(s.db, actor, conversation, false, false, s.now()); e != nil {
		return out, e
	}
	c, e := getJSON[Conversation](s.db, "SELECT payload FROM conversations WHERE id=?", conversation)
	if e != nil {
		return out, e
	}
	out.Conversation = s.named(c)
	out.Messages = []Message{}
	rows, e := s.db.Query("SELECT payload FROM messages WHERE conversation=? ORDER BY occurred, rowid", conversation)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if e = rows.Scan(&raw); e != nil {
			return out, e
		}
		var m Message
		if e = json.Unmarshal([]byte(raw), &m); e != nil {
			return out, e
		}
		out.Messages = append(out.Messages, m)
	}
	// RFC3339 timestamps with different fractional precision do not sort
	// chronologically as text. Equal timestamps retain ingestion order.
	sort.SliceStable(out.Messages, func(i, j int) bool {
		return out.Messages[i].At.Before(out.Messages[j].At)
	})
	return out, rows.Err()
}
func (s *Store) Message(actor, id string) (Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, e := getJSON[Message](s.db, "SELECT payload FROM messages WHERE id=?", id)
	if e != nil {
		return m, e
	}
	return m, permitted(s.db, actor, m.Conversation, false, false, s.now())
}
func (s *Store) Attention(id, action string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, e := getJSON[Message](s.db, "SELECT payload FROM messages WHERE id=?", id)
	if e != nil {
		return e
	}
	switch action {
	case "handled":
		m.Handled = true
	case "reopen":
		m.Handled = false
		m.SnoozedUntil = time.Time{}
	case "snooze":
		m.SnoozedUntil = s.now().Add(time.Hour)
	default:
		return fault(400, "unknown attention action")
	}
	_, e = s.db.Exec("UPDATE messages SET payload=? WHERE id=?", encode(m), id)
	return e
}
func (s *Store) AddAttachment(name string, data []byte) (Attachment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name = filepath.Base(name)
	if len(data) == 0 || len(data) > MaxAttachment || name == "." || len(name) > 200 || strings.ContainsAny(name, "\r\n\x00\x1b") {
		return Attachment{}, fault(400, "attachment must have a name and contain 1 byte to 5 MiB")
	}
	h := sha256.Sum256(append([]byte(name+"\x00"), data...))
	a := Attachment{ID: hex.EncodeToString(h[:]), Name: name, MIME: http.DetectContentType(data), Size: len(data)}
	if old, e := getJSON[Attachment](s.db, "SELECT payload FROM attachments WHERE id=?", a.ID); e == nil {
		return old, nil
	}
	var total int64
	if e := s.db.QueryRow("SELECT coalesce(sum(length(data)),0) FROM attachments").Scan(&total); e != nil {
		return a, e
	}
	if total+int64(len(data)) > MaxAttachmentStore {
		return a, fault(409, fmt.Sprintf("attachment storage is full (%d MiB)", MaxAttachmentStore>>20))
	}
	_, e := s.db.Exec("INSERT INTO attachments VALUES(?,?,?)", a.ID, encode(a), data)
	return a, e
}
func (s *Store) Attachments() ([]Attachment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, e := s.db.Query("SELECT payload FROM attachments ORDER BY rowid DESC")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Attachment{}
	for rows.Next() {
		var b string
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		var a Attachment
		if e = json.Unmarshal([]byte(b), &a); e != nil {
			return nil, e
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
func (s *Store) Attachment(id string) (Attachment, []byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, e := getJSON[Attachment](s.db, "SELECT payload FROM attachments WHERE id=?", id)
	if e != nil {
		return a, nil, e
	}
	var b []byte
	e = s.db.QueryRow("SELECT data FROM attachments WHERE id=?", id).Scan(&b)
	return a, b, e
}
func (s *Store) Draft(actor string, in DraftInput) (Draft, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validID(in.RequestID) || len(in.Body) > 20000 || len(in.Attachments) > 8 || (strings.TrimSpace(in.Body) == "" && len(in.Attachments) == 0) {
		return Draft{}, fault(400, "reply needs text or up to 8 attachments and a request_id")
	}
	m, e := getJSON[Message](s.db, "SELECT payload FROM messages WHERE id=?", in.ReplyTo)
	if e != nil {
		return Draft{}, e
	}
	if e = permitted(s.db, actor, m.Conversation, false, len(in.Attachments) > 0, s.now()); e != nil {
		return Draft{}, e
	}
	h := sha256.Sum256([]byte(actor + encode(in)))
	fingerprint := hex.EncodeToString(h[:])
	var oldHash, raw string
	e = s.db.QueryRow("SELECT fingerprint,payload FROM drafts WHERE request_id=?", in.RequestID).Scan(&oldHash, &raw)
	if e == nil {
		if oldHash != fingerprint {
			return Draft{}, fault(409, "request_id already belongs to a different reply")
		}
		var d Draft
		e = json.Unmarshal([]byte(raw), &d)
		return d, e
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return Draft{}, e
	}
	c, e := getJSON[Conversation](s.db, "SELECT payload FROM conversations WHERE id=?", m.Conversation)
	if e != nil {
		return Draft{}, e
	}
	if in.Thread {
		if c, e = s.startThread(actor, c, m); e != nil {
			return Draft{}, e
		}
	}
	d := Draft{ID: ID(), Actor: actor, Target: c, ReplyTo: m.ID, Body: in.Body, State: "draft", Created: s.now().UTC()}
	seen := map[string]bool{}
	for _, id := range in.Attachments {
		if seen[id] {
			continue
		}
		seen[id] = true
		a, e := getJSON[Attachment](s.db, "SELECT payload FROM attachments WHERE id=?", id)
		if e != nil {
			return Draft{}, e
		}
		d.Attachments = append(d.Attachments, a)
	}
	_, e = s.db.Exec("INSERT INTO drafts VALUES(?,?,?,?)", d.ID, in.RequestID, fingerprint, encode(d))
	return d, e
}
func (s *Store) Drafts(actor string) ([]Draft, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, e := s.db.Query("SELECT payload FROM drafts ORDER BY rowid DESC LIMIT 100")
	if e != nil {
		return nil, e
	}
	ds := []Draft{}
	for rows.Next() {
		var raw string
		if e = rows.Scan(&raw); e != nil {
			rows.Close()
			return nil, e
		}
		var d Draft
		if e = json.Unmarshal([]byte(raw), &d); e != nil {
			rows.Close()
			return nil, e
		}
		ds = append(ds, d)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	out := []Draft{}
	for _, d := range ds {
		if permitted(s.db, actor, d.Target.ID, false, false, s.now()) == nil {
			out = append(out, d)
		}
	}
	return out, nil
}
func (s *Store) Send(actor, id string) (Draft, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, e := s.db.Begin()
	if e != nil {
		return Draft{}, e
	}
	defer tx.Rollback()
	d, e := getJSON[Draft](tx, "SELECT payload FROM drafts WHERE id=?", id)
	if e != nil {
		return d, e
	}
	if e = permitted(tx, actor, d.Target.ID, true, len(d.Attachments) > 0, s.now()); e != nil {
		return d, e
	}
	if d.State == "simulated" || d.State == "sent" || d.State == "submitted" || d.State == "queued" || d.State == "sending" {
		return d, nil
	}
	if d.State != "draft" && d.State != "failed" && d.State != "blocked" {
		return d, fault(409, "reply is not a draft")
	}
	if !d.Target.Demo {
		return s.queue(tx, actor, d)
	}
	// The simulated transport and local receipt commit together. A future live
	// transport must use a durable outbox and represent uncertain delivery.
	d.State = "simulated"
	m := Message{ID: "reply-" + d.ID, NativeID: "reply-" + d.ID, Conversation: d.Target.ID, Sender: "You (simulated)", Body: d.Body, At: s.now().UTC(), Outgoing: true, ReplyTo: d.ReplyTo, Attachments: d.Attachments}
	if _, e = tx.Exec("INSERT INTO messages VALUES(?,?,?,?,?)", m.ID, m.Conversation, m.NativeID, occurredKey(m.At), encode(m)); e != nil {
		return d, e
	}
	parent, e := getJSON[Message](tx, "SELECT payload FROM messages WHERE id=?", d.ReplyTo)
	if e != nil {
		return d, e
	}
	parent.Handled = true
	if _, e = tx.Exec("UPDATE messages SET payload=? WHERE id=?", encode(parent), parent.ID); e != nil {
		return d, e
	}
	if _, e = tx.Exec("UPDATE drafts SET payload=? WHERE id=?", encode(d), d.ID); e != nil {
		return d, e
	}
	return d, tx.Commit()
}
func (s *Store) Grant(g Grant) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, e := getJSON[Conversation](s.db, "SELECT payload FROM conversations WHERE id=?", g.Conversation); e != nil {
		return e
	}
	if !g.Expires.After(s.now()) || g.Expires.After(s.now().Add(24*time.Hour)) {
		return fault(400, "grant expiry must be within the next 24 hours")
	}
	_, e := s.db.Exec("INSERT INTO grants VALUES(?,?) ON CONFLICT(conversation) DO UPDATE SET payload=excluded.payload", g.Conversation, encode(g))
	return e
}
func (s *Store) Revoke(conversation string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, e := s.db.Exec("DELETE FROM grants WHERE conversation=?", conversation)
	return e
}
func (s *Store) Seed() error {
	now := s.now().UTC()
	examples := []struct {
		c            Conversation
		sender, body string
	}{
		{Conversation{ID: "demo-telegram-robot", Service: "Telegram", Account: "personal (demo)", Name: "Robot build", Recipient: "Robot build group", Thread: "Motor controller", Demo: true}, "Maya", "Can you show the motor controller change and a screenshot?"},
		{Conversation{ID: "demo-gmail-review", Service: "Gmail", Account: "personal@example.test", Name: "Design review", Recipient: "alex@example.test", Thread: "Re: Design review", Demo: true}, "Alex", "Could you send the patch and your notes before the review?"},
		{Conversation{ID: "demo-outlook-gatech", Service: "Outlook", Account: "student@example.test (GT demo)", Name: "Project deadline", Recipient: "course@example.test", Thread: "Project deadline changed", Demo: true}, "Course staff", "The project deadline changed to Friday. Updated submission instructions are posted."},
		{Conversation{ID: "demo-slack-release", Service: "Slack", Account: "work (demo)", Name: "#release", Recipient: "#release", Thread: "Deployment review", Demo: true}, "Sam", "Please review the deployment fix when you have a moment."},
	}
	for i, x := range examples {
		_, e := s.Ingest(Ingress{Conversation: x.c, Message: Message{NativeID: fmt.Sprintf("seed-%d", i), Sender: x.sender, Body: x.body, At: now.Add(-time.Duration(i+1) * 7 * time.Minute)}})
		if e != nil {
			return e
		}
	}
	// Incoming pictures, so the clients have files to show.
	pictures := []struct {
		conv         int
		name, sender string
		body         string
		data         []byte
	}{
		{0, "scope-trace.png", "Maya", "Here is the scope trace after the fix.", scopeTrace()},
		{3, "error-rate.png", "Sam", "Error rate from this morning; the red bar is the bad deploy.", dashboard()},
	}
	for i, p := range pictures {
		a, e := s.AddAttachment(p.name, p.data)
		if e != nil {
			return e
		}
		x := examples[p.conv]
		if _, e = s.Ingest(Ingress{Conversation: x.c, Message: Message{NativeID: fmt.Sprintf("seed-picture-%d", i), Sender: p.sender, Body: p.body, Attachments: []Attachment{a}, At: now.Add(-time.Duration(i+1) * 3 * time.Minute)}}); e != nil {
			return e
		}
	}
	_, e := s.AddAttachment("controller.patch", []byte("commit demo-controller\n\nKeep the controller responsive while telemetry reconnects.\n\ndiff --git a/controller.go b/controller.go\n--- a/controller.go\n+++ b/controller.go\n@@ -1 +1 @@\n-waitForTelemetry()\n+go reconnectTelemetry()\n"))
	if e != nil {
		return e
	}
	_, e = s.AddAttachment("review-notes.txt", []byte("Demo notes\n\nThe reconnect work runs independently of the control loop.\nThe screenshot and patch should be reviewed together.\n"))
	return e
}
