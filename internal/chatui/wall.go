package chatui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/base698/amythest/internal/messages"
	tea "github.com/charmbracelet/bubbletea"
)

// /tag [REF] [PROJECT] [-milestone NAME] puts a message on a project's
// wall: by default the newest message here, on this workspace's wall. With
// -milestone it is also filed under that milestone of the project, so the
// milestone collects it whenever it was sent. /untag takes it off the wall.

type (
	workspaceMsg struct{ project string }
	tagMsg       struct {
		win       *window
		message   string
		project   string
		milestone string
		on        bool
		err       error
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
	milestone := ""
	for i, a := range args {
		if a == "-milestone" || a == "--milestone" {
			milestone, args = strings.Join(args[i+1:], " "), args[:i]
			if !on || milestone == "" {
				w.note(m.o.Now(), "-!- /tag [REF] [PROJECT] -milestone NAME files a message under a milestone.")
				return nil
			}
			if m.o.Milestones == nil {
				w.note(m.o.Now(), "-!- Milestones need the work server; set it up for amy first.")
				return nil
			}
			break
		}
	}
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
		named := ""
		if milestone != "" {
			// Find the milestone first, so a wrong name changes nothing.
			list, err := m.o.Milestones.List(c, project)
			if err != nil {
				return tagMsg{win: w, err: err}
			}
			found, ok := messages.FindMilestone(list, milestone)
			if !ok {
				return tagMsg{win: w, err: fmt.Errorf("%s has no milestone %q (amy milestone list)", project, milestone)}
			}
			named = found.Name
			if e = m.o.Milestones.Link(c, found.ID, msg.ID); e != nil {
				return tagMsg{win: w, err: e}
			}
		}
		if on {
			_, e = m.api.Tag(c, msg.ID, project)
		} else {
			e = m.api.Untag(c, msg.ID, project)
		}
		return tagMsg{win: w, message: msg.ID, project: project, milestone: named, on: on, err: e}
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
		if x.milestone != "" {
			w.note(m.o.Now(), "-!- [%s] is on the %s wall, in %s.", ref, x.project, x.milestone)
			return
		}
		w.note(m.o.Now(), "-!- [%s] is on the %s wall.", ref, x.project)
		return
	}
	w.tags[x.message] = slices.DeleteFunc(w.tags[x.message], func(p string) bool { return p == x.project })
	w.note(m.o.Now(), "-!- [%s] is off the %s wall.", ref, x.project)
}
