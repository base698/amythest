// Package chatui is the irssi-style Messages client: numbered windows, one
// per conversation, an activity bar, and one input line that takes text or
// /commands. Window 1 is the status window with the attention queue.
package chatui

import (
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
	"time"

	"github.com/base698/amythest/internal/messages"
)

const (
	kindStatus = "status"
	kindConv   = "conv"
	kindSearch = "search"
)

// Activity levels, as irssi: text, a message for you, something not to miss.
const (
	actNone = iota
	actText
	actMsg
	actHilight
)

type notice struct {
	at   time.Time
	text string
}

type window struct {
	kind      string
	conv      messages.Conversation // conv windows
	title     string                // status and search windows
	msgs      []messages.Message    // oldest first
	refs      map[string]string     // message ID -> short ref
	byRef     map[string]string     // short ref -> message ID
	replies   map[string]messages.ReplySummary
	parent    *messages.ThreadParent
	hasBefore bool
	loaded    bool
	loading   bool
	notices   []notice
	hits      []messages.Hit // search windows
	scroll    int            // lines up from the bottom
	level     int
	highlight string
	staged    []string            // files to send with the next message (paste, Ctrl+V)
	tags      map[string][]string // message ID -> walls it is on
}

func newConvWindow(c messages.Conversation) *window {
	return &window{kind: kindConv, conv: c, refs: map[string]string{}, byRef: map[string]string{}}
}

func (w *window) name() string {
	switch w.kind {
	case kindStatus:
		return "amy"
	case kindSearch:
		return w.title
	}
	name := messages.Plain(w.conv.Name)
	if name == "" {
		name = messages.Plain(w.conv.Recipient)
	}
	if w.parent != nil || (w.conv.Service == "Slack" && w.conv.Route.Thread != "") {
		name += "›thread"
	}
	return name
}

// refFor gives a message a short, stable label: three base-36 characters
// from its ID, lengthened only when another message here already has them.
func (w *window) refFor(id string) string {
	if r, ok := w.refs[id]; ok {
		return r
	}
	h := fnv.New64a()
	h.Write([]byte(id))
	full := strconv.FormatUint(h.Sum64(), 36)
	for n := 3; n <= len(full); n++ {
		r := full[:n]
		if _, taken := w.byRef[r]; !taken {
			w.refs[id], w.byRef[r] = r, id
			return r
		}
	}
	w.refs[id], w.byRef[id] = id, id
	return id
}

// merge adds messages, keeping time order and dropping ones already here.
func (w *window) merge(in []messages.Message, older bool) {
	seen := make(map[string]bool, len(w.msgs))
	for _, m := range w.msgs {
		seen[m.ID] = true
	}
	var add []messages.Message
	for _, m := range in {
		if !seen[m.ID] {
			add = append(add, m)
			w.refFor(m.ID)
		}
	}
	if older {
		w.msgs = append(add, w.msgs...)
	} else {
		w.msgs = append(w.msgs, add...)
	}
}

func (w *window) message(ref string) (messages.Message, bool) {
	id, ok := w.byRef[strings.TrimPrefix(strings.ToLower(ref), "#")]
	if !ok {
		return messages.Message{}, false
	}
	for _, m := range w.msgs {
		if m.ID == id {
			return m, true
		}
	}
	return messages.Message{}, false
}

// replyTarget is what plain text answers: the newest message from someone
// else, else the newest message at all.
func (w *window) replyTarget() (messages.Message, bool) {
	for i := len(w.msgs) - 1; i >= 0; i-- {
		if !w.msgs[i].Outgoing {
			return w.msgs[i], true
		}
	}
	if len(w.msgs) > 0 {
		return w.msgs[len(w.msgs)-1], true
	}
	return messages.Message{}, false
}

func (w *window) note(at time.Time, format string, args ...any) {
	w.notices = append(w.notices, notice{at, fmt.Sprintf(format, args...)})
}

// levelOf is the activity a new message raises.
func levelOf(m messages.Message, c messages.Conversation) int {
	switch {
	case m.Outgoing:
		return actNone
	case m.Category == "Don't miss":
		return actHilight
	case m.Category == "Reply" || isDirect(c):
		return actMsg
	}
	return actText
}

// isDirect is a one-to-one chat rather than a channel or group.
func isDirect(c messages.Conversation) bool {
	switch c.Route.Provider {
	case "slack":
		return strings.HasPrefix(c.Route.Chat, "D")
	case "signal", "imessage", "telegram":
		return !strings.HasPrefix(c.Name, "#")
	}
	return false
}
