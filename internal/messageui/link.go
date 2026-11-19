package messageui

import (
	"context"
	"fmt"
	"html"
	"strings"

	"github.com/base698/amythest/internal/apiclient"
	"github.com/base698/amythest/internal/messages"
	"github.com/base698/amythest/internal/tasks"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

type noteTarget struct {
	kind, slug, label string
	task              *tasks.Task
}
type linkTargetsMsg struct {
	targets []noteTarget
	err     error
}
type contactsMsg struct {
	names []string
	err   error
}
type linkState struct {
	input   textinput.Model
	targets []noteTarget
	kind    int
	cursor  int
	message messages.Item
	names   []string
}

func (m *Model) startLink() tea.Cmd {
	if m.opts.Notes == nil {
		m.status = "Notes connection is unavailable"
		return nil
	}
	it := m.current()
	if it == nil || it.Message.ID == "" {
		return nil
	}
	i := textinput.New()
	i.Prompt = "Find note or task: "
	i.CharLimit = 300
	i.Focus()
	m.link = linkState{input: i, message: *it}
	m.mode = "link"
	m.status = "Loading notes and tasks…"
	api := m.opts.Notes
	return func() tea.Msg {
		c, cancel := ctx()
		defer cancel()
		notes, e := api.ListNotes(c)
		if e != nil {
			return linkTargetsMsg{err: e}
		}
		groups, e := api.ListTasks(c, "not done")
		if e != nil {
			return linkTargetsMsg{err: e}
		}
		var targets []noteTarget
		for _, g := range groups {
			for _, t := range g.Tasks {
				t := t
				if !strings.HasPrefix(t.Path, "kanban/") {
					targets = append(targets, noteTarget{kind: "task", slug: t.Slug, label: t.Text + " · " + t.Path, task: &t})
				}
			}
		}
		for _, n := range notes {
			if !strings.HasPrefix(n.Path, "kanban/") {
				targets = append(targets, noteTarget{kind: "note", slug: n.Slug, label: n.Title + " · " + n.Path})
			}
		}
		return linkTargetsMsg{targets: targets}
	}
}
func (m *Model) linkMatches() []noteTarget {
	result := []noteTarget{}
	q := strings.Fields(strings.ToLower(m.link.input.Value()))
	for _, t := range m.link.targets {
		if m.link.kind == 1 && t.kind != "task" || m.link.kind == 2 && t.kind != "note" {
			continue
		}
		match := true
		for _, word := range q {
			if !strings.Contains(strings.ToLower(t.label), word) {
				match = false
			}
		}
		if match {
			result = append(result, t)
		}
	}
	return result
}
func attachMessage(ctx context.Context, c *apiclient.Client, it messages.Item, target noteTarget) error {
	note, e := c.GetNote(ctx, target.slug)
	if e != nil {
		return e
	}
	marker := "<!-- amy-message:" + it.Message.ID + " -->"
	if strings.Contains(note.Markdown, marker) {
		return fmt.Errorf("this message is already attached to this note")
	}
	text := fmt.Sprintf("Message (%s · %s · %s) %s\n%s", messages.DisplayService(it.Conversation), html.EscapeString(it.Conversation.Name), it.Message.At.Local().Format("2006-01-02 15:04"), marker, html.EscapeString(messages.Plain(it.Message.Body)))
	text = messages.Plain(text)
	if it.Message.SourceURL != "" {
		text += "\nSource: " + it.Message.SourceURL
	}
	var updated string
	if target.task != nil {
		if target.task.Version != note.Version {
			return fmt.Errorf("destination changed; reopen the picker")
		}
		updated, e = tasks.Annotate(note.Markdown, *target.task, text)
		if e != nil {
			return e
		}
	} else {
		updated = strings.TrimRight(note.Markdown, "\n") + "\n\n> " + strings.ReplaceAll(text, "\n", "\n> ") + "\n"
	}
	return c.SaveNote(ctx, note.Slug, updated, note.Version)
}
func (m *Model) linkKey(k tea.KeyMsg) tea.Cmd {
	switch k.String() {
	case "esc":
		m.mode = "focus"
		return nil
	case "tab":
		m.link.kind = (m.link.kind + 1) % 3
		m.link.cursor = 0
		return nil
	case "down", "ctrl+n":
		m.link.cursor = min(max(0, len(m.linkMatches())-1), m.link.cursor+1)
		return nil
	case "up", "ctrl+p":
		m.link.cursor = max(0, m.link.cursor-1)
		return nil
	case "enter":
		matches := m.linkMatches()
		if len(matches) == 0 {
			return nil
		}
		target := matches[min(m.link.cursor, len(matches)-1)]
		it := m.link.message
		m.busy = true
		api := m.opts.Notes
		return func() tea.Msg {
			c, cancel := ctx()
			defer cancel()
			e := attachMessage(c, api, it, target)
			return linkedMsg{e}
		}
	}
	var cmd tea.Cmd
	m.link.input, cmd = m.link.input.Update(k)
	m.link.cursor = 0
	return cmd
}

type linkedMsg struct{ err error }

func (m *Model) linkView(h int) string {
	m.link.input.Width = max(15, m.w-24)
	lines := []string{"Attach message from " + messages.Plain(m.link.message.Conversation.Name), m.link.input.View(), "Tab: " + []string{"notes + tasks", "tasks", "notes"}[m.link.kind] + " · ↑↓ choose · Enter attach · Esc back", ""}
	matches := m.linkMatches()
	start := max(0, m.link.cursor-max(1, h-6)+1)
	for i := start; i < len(matches) && len(lines) < h; i++ {
		prefix := "  "
		if i == m.link.cursor {
			prefix = "› "
		}
		lines = append(lines, prefix+matches[i].kind+" · "+messages.Plain(matches[i].label))
	}
	return fit(lines, h, m.w-2)
}
