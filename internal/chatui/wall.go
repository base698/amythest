package chatui

import (
	"slices"
	"strings"

	"github.com/base698/amythest/internal/messages"
	tea "github.com/charmbracelet/bubbletea"
)

// /tag [REF] [PROJECT] puts a message on a project's wall: by default the
// newest message here, on this workspace's wall. /untag takes it off.

type (
	workspaceMsg struct{ project string }
	tagMsg       struct {
		win     *window
		message string
		project string
		on      bool
		err     error
	}
)

func (m *Model) resolveWorkspace() tea.Cmd {
	if m.o.Workspace == nil {
		return nil
	}
	find := m.o.Workspace
	return func() tea.Msg { return workspaceMsg{find()} }
}

func (m *Model) tag(w *window, rest string, on bool) tea.Cmd {
	cmd := "/tag"
	if !on {
		cmd = "/untag"
	}
	if w.kind != kindConv {
		m.status("-!- %s works in a chat window.", cmd)
		return nil
	}
	args := strings.Fields(rest)
	var msg messages.Message
	ok := false
	if len(args) > 0 {
		if msg, ok = w.message(args[0]); ok {
			args = args[1:]
		}
	}
	if !ok && len(w.msgs) > 0 {
		msg, ok = w.msgs[len(w.msgs)-1], true
	}
	if !ok {
		w.note(m.o.Now(), "-!- No message here to tag.")
		return nil
	}
	project := strings.Join(args, " ")
	if project == "" {
		project = m.workspace
	}
	if project == "" {
		w.note(m.o.Now(), "-!- %s [REF] PROJECT — this isn't a herdr workspace, so name the wall.", cmd)
		return nil
	}
	return func() tea.Msg {
		c, cancel := ctx()
		defer cancel()
		var e error
		if on {
			_, e = m.api.Tag(c, msg.ID, project)
		} else {
			e = m.api.Untag(c, msg.ID, project)
		}
		return tagMsg{win: w, message: msg.ID, project: project, on: on, err: e}
	}
}

func (m *Model) onTag(x tagMsg) {
	w := x.win
	if x.err != nil {
		w.note(m.o.Now(), "-!- %v", x.err)
		return
	}
	if w.tags == nil {
		w.tags = map[string][]string{}
	}
	ref := w.refFor(x.message)
	if x.on {
		if !slices.Contains(w.tags[x.message], x.project) {
			w.tags[x.message] = append(w.tags[x.message], x.project)
		}
		w.note(m.o.Now(), "-!- [%s] is on the %s wall.", ref, x.project)
		return
	}
	w.tags[x.message] = slices.DeleteFunc(w.tags[x.message], func(p string) bool { return p == x.project })
	w.note(m.o.Now(), "-!- [%s] is off the %s wall.", ref, x.project)
}
