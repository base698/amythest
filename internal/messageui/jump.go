package messageui

import (
	"context"
	"time"

	"github.com/base698/amythest/internal/messages"
	tea "github.com/charmbracelet/bubbletea"
)

// eventsAPI is what a view needs to be sent places (by amy find, or by
// `amy messages jump`). A client without it simply never jumps.
type eventsAPI interface {
	Events(context.Context, int64, messages.Listener, time.Duration) (messages.Events, error)
	Message(context.Context, string) (messages.Message, error)
}

const (
	eventWait  = 10 * time.Second
	eventRetry = 5 * time.Second
)

type eventsMsg struct {
	events messages.Events
	err    error
}
type listenMsg struct{}
type jumpMsg struct {
	jump messages.Jump
	err  error
}

// listen waits for the next events. The first call (seq < 0) also picks up a
// jump made just before this view opened: the reason it was opened.
func (m *Model) listen() tea.Cmd {
	api, ok := m.api.(eventsAPI)
	if !ok {
		return nil
	}
	since, who := m.eventSeq, m.opts.Listener
	return func() tea.Msg {
		c, cancel := context.WithTimeout(context.Background(), eventWait+5*time.Second)
		defer cancel()
		ev, e := api.Events(c, since, who, eventWait)
		return eventsMsg{ev, e}
	}
}

func (m *Model) onEvents(x eventsMsg) tea.Cmd {
	if x.err != nil {
		// The gateway restarted or the tunnel dropped: try again shortly.
		// Sequence numbers restart with the gateway, so start over too.
		m.eventSeq = -1
		return tea.Tick(eventRetry, func(time.Time) tea.Msg { return listenMsg{} })
	}
	m.eventSeq = x.events.Seq
	var last *messages.Jump
	for _, e := range x.events.Events {
		if e.Kind == "jump" && e.Jump != nil {
			last = e.Jump
		}
	}
	if last == nil {
		return m.listen()
	}
	return tea.Batch(m.listen(), m.startJump(*last))
}

// startJump shows what a jump points at. A message without its conversation
// is looked up first.
func (m *Model) startJump(j messages.Jump) tea.Cmd {
	if j.Message != "" && j.Conversation == "" {
		api, ok := m.api.(eventsAPI)
		if !ok {
			return nil
		}
		return func() tea.Msg {
			c, cancel := ctx()
			defer cancel()
			msg, e := api.Message(c, j.Message)
			j.Conversation = msg.Conversation
			return jumpMsg{j, e}
		}
	}
	if m.target != nil && m.mode == "compose" {
		m.status = "Reply paused · r resumes it"
	}
	if m.mode != "focus" {
		m.mode = "focus"
		m.review = nil
	}
	m.highlight = ""
	if j.Section == "drafts" && j.Conversation == "" {
		m.busy = true
		return tea.Batch(m.load(), m.loadDrafts(false))
	}
	// Show every conversation unless the jump asks for Focus, so a handled
	// conversation can still be reached; drop filters that could hide it.
	m.all = j.Section != "focus"
	m.trash = false
	m.query = ""
	m.provider = ""
	m.scroll = 0
	if j.Conversation != "" {
		copy := j
		m.jump = &copy
	} else {
		m.cursor = 0
	}
	m.loading = true
	return m.load()
}

// arrive puts the cursor on the jump's conversation once the list is loaded.
func (m *Model) arrive() {
	if m.jump == nil {
		return
	}
	for i, it := range m.items {
		if it.Conversation.ID == m.jump.Conversation {
			m.cursor = i
			m.scroll = 0
			return
		}
	}
	m.status = "That conversation is not in this list (it may be in trash)."
	m.jump = nil
}

// land scrolls the loaded conversation so the jumped-to message is in view
// near the top, and marks it.
func (m *Model) land() {
	j := m.jump
	m.jump = nil
	if j.Message == "" {
		return
	}
	lines, at := m.threadLines(m.rightWidth(), j.Message)
	if at < 0 {
		m.status = "That message is no longer in this conversation."
		return
	}
	m.highlight = j.Message
	available := max(1, m.h-10)
	start := max(0, at-2)
	m.scroll = max(0, len(lines)-available-start)
}
