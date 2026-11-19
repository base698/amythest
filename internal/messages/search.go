package messages

import (
	"database/sql"
	"encoding/json"
	"strings"
)

// Full-text search over message bodies and senders. Triggers keep the index in
// step with every writer of the messages table (ingest, send, delivery,
// attention), so no write path has to remember it.
const searchSchema = `
 CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(body, sender, tokenize='unicode61 remove_diacritics 2');
 CREATE TRIGGER IF NOT EXISTS messages_fts_insert AFTER INSERT ON messages BEGIN
   INSERT INTO messages_fts(rowid, body, sender) VALUES (new.rowid, coalesce(json_extract(new.payload,'$.body'),''), coalesce(json_extract(new.payload,'$.sender'),''));
 END;
 CREATE TRIGGER IF NOT EXISTS messages_fts_update AFTER UPDATE OF payload ON messages BEGIN
   DELETE FROM messages_fts WHERE rowid = old.rowid;
   INSERT INTO messages_fts(rowid, body, sender) VALUES (new.rowid, coalesce(json_extract(new.payload,'$.body'),''), coalesce(json_extract(new.payload,'$.sender'),''));
 END;
 CREATE TRIGGER IF NOT EXISTS messages_fts_delete AFTER DELETE ON messages BEGIN
   DELETE FROM messages_fts WHERE rowid = old.rowid;
 END;`

// openSearch creates the index and fills it from messages stored before it
// existed. Indexing is idempotent: only rows missing from the index are added.
func openSearch(db *sql.DB) error {
	if _, e := db.Exec(searchSchema); e != nil {
		return e
	}
	_, e := db.Exec(`INSERT INTO messages_fts(rowid, body, sender)
 SELECT rowid, coalesce(json_extract(payload,'$.body'),''), coalesce(json_extract(payload,'$.sender'),'')
 FROM messages WHERE rowid NOT IN (SELECT rowid FROM messages_fts)`)
	return e
}

// Hit is one search result: the message, where it was said and a short
// excerpt around the match.
type Hit struct {
	Conversation Conversation `json:"conversation"`
	Message      Message      `json:"message"`
	Snippet      string       `json:"snippet"`
}

const (
	defaultSearchLimit = 30
	maxSearchLimit     = 100
)

// ftsQuery turns what a person typed into an FTS5 query that cannot be a
// syntax error: every word is a quoted phrase, and the last one is a prefix
// so results appear while typing. All words must match.
func ftsQuery(q string) string {
	words := strings.Fields(q)
	if len(words) > 12 {
		words = words[:12]
	}
	parts := make([]string, 0, len(words))
	for i, w := range words {
		w = strings.ReplaceAll(w, `"`, `""`)
		p := `"` + w + `"`
		if i == len(words)-1 {
			p += "*"
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, " ")
}

// Search finds messages whose body or sender matches q, best match first.
// conversation, when set, limits the search to one conversation. The owner
// searches everything outside the trash; an agent only conversations it holds
// an active grant for.
func (s *Store) Search(actor, q, conversation string, limit int) ([]Hit, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if actor != "owner" && actor != "agent" {
		return nil, fault(403, "unknown identity")
	}
	q = strings.TrimSpace(q)
	if q == "" || len(q) > 200 {
		return nil, fault(400, "search needs 1 to 200 characters")
	}
	if conversation != "" {
		if e := permitted(s.db, actor, conversation, false, false, s.now()); e != nil {
			return nil, e
		}
	}
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	limit = min(limit, maxSearchLimit)
	query := `SELECT m.payload, snippet(messages_fts, 0, '', '', '…', 16)
 FROM messages_fts JOIN messages m ON m.rowid = messages_fts.rowid
 WHERE messages_fts MATCH ?
   AND m.conversation NOT IN (SELECT conversation FROM conversation_trash)
   AND (? = '' OR m.conversation = ?)
   AND (? = 'owner' OR m.conversation IN (SELECT conversation FROM grants))
 ORDER BY bm25(messages_fts, 4.0, 1.0), m.occurred DESC
 LIMIT ?`
	// Grant expiry is checked per row below; read a few extra rows so expired
	// grants do not starve an agent's result list.
	fetch := limit
	if actor != "owner" {
		fetch = maxSearchLimit
	}
	rows, e := s.db.Query(query, ftsQuery(q), conversation, conversation, actor, fetch)
	if e != nil {
		return nil, e
	}
	type raw struct{ payload, snippet string }
	var found []raw
	for rows.Next() {
		var r raw
		if e = rows.Scan(&r.payload, &r.snippet); e != nil {
			rows.Close()
			return nil, e
		}
		found = append(found, r)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	now := s.now()
	allowed := map[string]bool{}
	conversations := map[string]Conversation{}
	out := []Hit{}
	for _, r := range found {
		if len(out) == limit {
			break
		}
		m, e := decodeMessage(r.payload)
		if e != nil {
			return nil, e
		}
		ok, seen := allowed[m.Conversation]
		if !seen {
			ok = permitted(s.db, actor, m.Conversation, false, false, now) == nil
			allowed[m.Conversation] = ok
		}
		if !ok {
			continue
		}
		c, seen := conversations[m.Conversation]
		if !seen {
			if c, e = getJSON[Conversation](s.db, "SELECT payload FROM conversations WHERE id=?", m.Conversation); e != nil {
				return nil, e
			}
			c = s.named(c)
			conversations[m.Conversation] = c
		}
		out = append(out, Hit{Conversation: c, Message: m, Snippet: Plain(r.snippet)})
	}
	return out, nil
}

func decodeMessage(raw string) (Message, error) {
	var m Message
	err := json.Unmarshal([]byte(raw), &m)
	return m, err
}
