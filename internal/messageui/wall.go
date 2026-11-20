package messageui

import (
	"net/url"
	"slices"

	"github.com/base698/amythest/internal/messages"
	tea "github.com/charmbracelet/bubbletea"
)

// w puts the selected message on this workspace's wall (the project the
// wall shows here), or takes it off again. The message is the one a jump
// landed on, else the conversation's latest.

type (
	workspaceMsg struct{ project string }
	taggedMsg    struct {
		text string
		err  error
	}
)

// resolveWorkspace finds this workspace's project once, in the background.
func (m *Model) resolveWorkspace() tea.Cmd {
	if m.opts.Workspace == nil {
		return nil
	}
	find := m.opts.Workspace
	return func() tea.Msg { return workspaceMsg{find()} }
}

func (m *Model) wallTarget() (messages.Message, bool) {
	for _, msg := range m.thread.Messages {
		if m.highlight != "" && msg.ID == m.highlight {
			return msg, true
		}
	}
	it := m.current()
	if it == nil || it.Message.ID == "" {
		return messages.Message{}, false
	}
	return it.Message, true
}

func (m *Model) toggleWall() tea.Cmd {
	msg, ok := m.wallTarget()
	if !ok {
		m.status = "No message to put on the wall"
		return nil
	}
	project := m.project
	if project == "" {
		m.status = "This isn't a herdr workspace with a wall; open Messages from one"
		return nil
	}
	on := slices.Contains(m.thread.Tags[msg.ID], project)
	return func() tea.Msg {
		c, cancel := ctx()
		defer cancel()
		if on {
			q := url.Values{"message": {msg.ID}, "project": {project}}
			return taggedMsg{"Taken off the " + project + " wall", m.api.Do(c, "DELETE", "/tags?"+q.Encode(), nil, nil)}
		}
		e := m.api.Do(c, "POST", "/tags", map[string]string{"message": msg.ID, "project": project}, nil)
		return taggedMsg{"On the " + project + " wall", e}
	}
}

func (m *Model) onTagged(x taggedMsg) tea.Cmd {
	if x.err != nil {
		m.status = "Wall: " + x.err.Error()
		return nil
	}
	m.status = x.text
	return m.loadThread()
}
