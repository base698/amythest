package messageui

import (
	"context"
	"net/url"
	"slices"

	"github.com/base698/amythest/internal/messages"
	tea "github.com/charmbracelet/bubbletea"
)

// w puts the selected message on this workspace's wall (the project the
// wall shows here), or takes it off again. The message is the one a jump
// landed on, else the conversation's latest. When the project has
// milestones, w first offers them: Enter on "Wall only" (the default) just
// tags it; choosing a milestone also files the message under it.

type (
	workspaceMsg struct{ project string }
	taggedMsg    struct {
		text string
		err  error
	}
	milestonesMsg struct {
		msg     messages.Message
		project string
		list    []messages.Milestone
		err     error
	}
	milestonePick struct {
		msg     messages.Message
		project string
		list    []messages.Milestone
		cursor  int // 0 is "Wall only"
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
	if !on && m.opts.Milestones != nil {
		list := m.opts.Milestones.List
		m.busy = true
		return func() tea.Msg {
			c, cancel := ctx()
			defer cancel()
			l, e := list(c, project)
			return milestonesMsg{msg: msg, project: project, list: l, err: e}
		}
	}
	return m.tagCmd(msg, project, on, messages.Milestone{})
}

func (m *Model) tagCmd(msg messages.Message, project string, on bool, milestone messages.Milestone) tea.Cmd {
	link := func(context.Context) error { return nil }
	if milestone.ID != "" {
		link = func(c context.Context) error { return m.opts.Milestones.Link(c, milestone.ID, msg.ID) }
	}
	return func() tea.Msg {
		c, cancel := ctx()
		defer cancel()
		if on {
			q := url.Values{"message": {msg.ID}, "project": {project}}
			return taggedMsg{"Taken off the " + project + " wall", m.api.Do(c, "DELETE", "/tags?"+q.Encode(), nil, nil)}
		}
		if e := link(c); e != nil {
			return taggedMsg{"", e}
		}
		e := m.api.Do(c, "POST", "/tags", map[string]string{"message": msg.ID, "project": project}, nil)
		text := "On the " + project + " wall"
		if milestone.ID != "" {
			text += ", in " + milestone.Name
		}
		return taggedMsg{text, e}
	}
}

func (m *Model) onMilestones(x milestonesMsg) tea.Cmd {
	m.busy = false
	if x.err != nil || len(x.list) == 0 {
		// Without milestones to offer, w stays a plain wall tag.
		return m.tagCmd(x.msg, x.project, false, messages.Milestone{})
	}
	m.pickMilestone = milestonePick{msg: x.msg, project: x.project, list: x.list}
	m.mode = "milestone"
	return nil
}

func (m *Model) milestoneKey(k tea.KeyMsg) tea.Cmd {
	p := &m.pickMilestone
	switch k.String() {
	case "esc", "q":
		m.mode = "focus"
		m.status = "Not put on the wall"
	case "down", "j", "ctrl+n":
		p.cursor = min(len(p.list), p.cursor+1)
	case "up", "k", "ctrl+p":
		p.cursor = max(0, p.cursor-1)
	case "enter":
		m.mode = "focus"
		var chosen messages.Milestone
		if p.cursor > 0 {
			chosen = p.list[p.cursor-1]
		}
		return m.tagCmd(p.msg, p.project, false, chosen)
	}
	return nil
}

func (m *Model) milestoneView(h int) string {
	p := m.pickMilestone
	lines := []string{"Put this message on the " + messages.Plain(p.project) + " wall", "↑↓ choose · Enter · Esc cancel", ""}
	rows := []string{"Wall only"}
	for _, x := range p.list {
		mark := "◇ "
		if x.Open {
			mark = "◆ "
		}
		rows = append(rows, "Wall and milestone "+mark+messages.Plain(x.Name))
	}
	start := max(0, p.cursor-max(1, h-4)+1)
	for i := start; i < len(rows) && len(lines) < h; i++ {
		prefix := "  "
		if i == p.cursor {
			prefix = "› "
		}
		lines = append(lines, prefix+rows[i])
	}
	return fit(lines, h, m.w-2)
}

func (m *Model) onTagged(x taggedMsg) tea.Cmd {
	if x.err != nil {
		m.status = "Wall: " + x.err.Error()
		return nil
	}
	m.status = x.text
	return m.loadThread()
}
