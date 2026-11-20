package messages

import "database/sql"

// Page selects part of a conversation, oldest first. At most one of Around,
// Before and After is set; with none, the newest Limit messages are returned.
type Page struct {
	Around string // centre the page on this message ID
	Before string // messages older than this message ID
	After  string // messages newer than this message ID
	Limit  int
}

const (
	defaultPageLimit = 50
	maxPageLimit     = 500
)

// ThreadPage returns one page of a conversation, so a client can open at a
// search hit and scroll from there instead of loading the whole history.
// Pages are read through the (conversation, occurred) index in the same
// order Thread uses. Anchors must belong to the conversation.
func (s *Store) ThreadPage(actor, conversation string, p Page) (Thread, error) {
	set := 0
	for _, v := range []string{p.Around, p.Before, p.After} {
		if v != "" {
			set++
		}
	}
	if set > 1 {
		return Thread{}, fault(400, "use only one of around, before and after")
	}
	if p.Limit <= 0 {
		p.Limit = defaultPageLimit
	}
	p.Limit = min(p.Limit, maxPageLimit)
	s.mu.Lock()
	defer s.mu.Unlock()
	var t Thread
	if e := permitted(s.db, actor, conversation, false, false, s.now()); e != nil {
		return t, e
	}
	c, e := getJSON[Conversation](s.db, "SELECT payload FROM conversations WHERE id=?", conversation)
	if e != nil {
		return t, e
	}
	t.Conversation = s.named(c)
	t.Messages = []Message{}
	anchor := p.Around + p.Before + p.After
	var at cursor
	if anchor != "" {
		if e = s.db.QueryRow("SELECT occurred, rowid FROM messages WHERE id=? AND conversation=?", anchor, conversation).Scan(&at.occurred, &at.rowid); e == sql.ErrNoRows {
			return Thread{}, fault(404, "message is not in this conversation")
		} else if e != nil {
			return Thread{}, e
		}
	}
	switch {
	case p.Around != "":
		// Read a full page each side, then centre the anchor in what exists.
		before, moreBefore, e := s.pageSide(conversation, &at, false, p.Limit)
		if e != nil {
			return t, e
		}
		after, moreAfter, e := s.pageSide(conversation, &at, true, p.Limit)
		if e != nil {
			return t, e
		}
		self, e := getJSON[Message](s.db, "SELECT payload FROM messages WHERE rowid=?", at.rowid)
		if e != nil {
			return t, e
		}
		all := append(append(before, self), after...)
		lo := max(0, len(before)-p.Limit/2)
		hi := min(len(all), lo+p.Limit)
		lo = max(0, hi-p.Limit)
		t.Messages = all[lo:hi]
		t.HasBefore = lo > 0 || moreBefore
		t.HasAfter = hi < len(all) || moreAfter
	case p.After != "":
		t.Messages, t.HasAfter, e = s.pageSide(conversation, &at, true, p.Limit)
		t.HasBefore = true
	case p.Before != "":
		t.Messages, t.HasBefore, e = s.pageSide(conversation, &at, false, p.Limit)
		t.HasAfter = true
	default:
		t.Messages, t.HasBefore, e = s.pageSide(conversation, nil, false, p.Limit)
	}
	if e != nil {
		return t, e
	}
	if e := s.tagThread(actor, &t); e != nil {
		return t, e
	}
	return t, s.annotate(actor, &t)
}

type cursor struct {
	occurred string
	rowid    int64
}

// pageSide reads up to limit messages after (or before) from, oldest first,
// and whether more lie beyond them. A nil from reads from the newest end.
func (s *Store) pageSide(conversation string, from *cursor, after bool, limit int) ([]Message, bool, error) {
	q := "SELECT payload FROM messages WHERE conversation=?"
	args := []any{conversation}
	if from != nil {
		if after {
			q += " AND (occurred > ? OR (occurred = ? AND rowid > ?))"
		} else {
			q += " AND (occurred < ? OR (occurred = ? AND rowid < ?))"
		}
		args = append(args, from.occurred, from.occurred, from.rowid)
	}
	if after {
		q += " ORDER BY occurred, rowid"
	} else {
		q += " ORDER BY occurred DESC, rowid DESC"
	}
	q += " LIMIT ?"
	args = append(args, limit+1)
	rows, e := s.db.Query(q, args...)
	if e != nil {
		return nil, false, e
	}
	defer rows.Close()
	out := []Message{}
	for rows.Next() {
		var raw string
		if e = rows.Scan(&raw); e != nil {
			return nil, false, e
		}
		m, e := decodeMessage(raw)
		if e != nil {
			return nil, false, e
		}
		out = append(out, m)
	}
	if e = rows.Err(); e != nil {
		return nil, false, e
	}
	more := len(out) > limit
	if more {
		out = out[:limit]
	}
	if !after {
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	}
	return out, more, nil
}
