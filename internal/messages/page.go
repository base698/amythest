package messages

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
//
// Messages are ordered by their time the same way Thread orders them.
// Anchors must belong to the conversation.
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
	t, e := s.thread(actor, conversation)
	if e != nil {
		return t, e
	}
	all := t.Messages
	anchor := p.Around + p.Before + p.After
	at := len(all)
	if anchor != "" {
		at = -1
		for i, m := range all {
			if m.ID == anchor {
				at = i
				break
			}
		}
		if at < 0 {
			return Thread{}, fault(404, "message is not in this conversation")
		}
	}
	var lo, hi int
	switch {
	case p.Around != "":
		lo = max(0, at-p.Limit/2)
		hi = min(len(all), lo+p.Limit)
		lo = max(0, hi-p.Limit)
	case p.After != "":
		lo = at + 1
		hi = min(len(all), lo+p.Limit)
	default: // Before, or the newest page
		hi = at
		lo = max(0, hi-p.Limit)
	}
	t.Messages = all[lo:hi]
	t.HasBefore = lo > 0
	t.HasAfter = hi < len(all)
	return t, nil
}
