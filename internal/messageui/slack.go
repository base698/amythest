package messageui

import (
	"net/url"
	"sort"
	"strings"

	"github.com/base698/amythest/internal/messages"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

type slackPerson struct{ id, name, url string }

func (m *Model) startPeople() tea.Cmd {
	it := m.current()
	if it == nil || it.Conversation.Service != "Slack" || it.Conversation.Demo {
		m.status = "Choose a live Slack conversation first"
		return nil
	}
	if m.thread.Conversation.ID != it.Conversation.ID {
		m.status = "Wait for the conversation to load, then press m"
		return nil
	}
	byID := map[string]slackPerson{}
	for _, msg := range m.thread.Messages {
		link := messages.SlackPersonURL(it.Conversation, msg)
		if link == "" {
			continue
		}
		id := msg.SenderID
		if id == "" {
			id = msg.Sender
		}
		byID[id] = slackPerson{id, messages.Plain(msg.Sender), link}
	}
	m.people = nil
	for _, person := range byID {
		m.people = append(m.people, person)
	}
	sort.Slice(m.people, func(i, j int) bool { return m.people[i].name < m.people[j].name })
	if len(m.people) == 0 {
		m.status = "No Slack person links are available yet; refresh the Slack import"
		return nil
	}
	m.peopleInput = textinput.New()
	m.peopleInput.Prompt = "Find a person: "
	m.peopleInput.CharLimit = 200
	m.peopleInput.Focus()
	m.peopleCursor, m.mode = 0, "people"
	return textinput.Blink
}

func (m *Model) peopleMatches() []slackPerson {
	var out []slackPerson
	query := strings.ToLower(strings.TrimSpace(m.peopleInput.Value()))
	for _, p := range m.people {
		if strings.Contains(strings.ToLower(p.name), query) {
			out = append(out, p)
		}
	}
	return out
}

func (m *Model) peopleKey(k tea.KeyMsg) tea.Cmd {
	people := m.peopleMatches()
	switch k.String() {
	case "esc":
		m.mode = "focus"
		return nil
	case "down", "ctrl+n":
		m.peopleCursor = min(max(0, len(people)-1), m.peopleCursor+1)
		return nil
	case "up", "ctrl+p":
		m.peopleCursor = max(0, m.peopleCursor-1)
		return nil
	case "enter":
		if m.peopleCursor < len(people) {
			link := people[m.peopleCursor].url
			m.mode = "focus"
			return m.openLink(link, "Opened in Slack; choose Message on the person's profile to DM")
		}
		return nil
	}
	m.peopleCursor = 0
	var cmd tea.Cmd
	m.peopleInput, cmd = m.peopleInput.Update(k)
	return cmd
}

func (m *Model) peopleView(h int) string {
	m.peopleInput.Width = max(15, m.w-22)
	lines := []string{"People in this Slack conversation", "", m.peopleInput.View(), "", "Choose a person to open Slack. On a profile, choose Message to start a DM.", ""}
	people := m.peopleMatches()
	start := max(0, m.peopleCursor-max(1, h-8)+1)
	for i := start; i < len(people) && len(lines) < h; i++ {
		prefix := "  "
		if i == m.peopleCursor {
			prefix = "› "
		}
		lines = append(lines, prefix+people[i].name)
	}
	return fit(lines, h, m.w-2)
}

func (m *Model) openSource(raw string) tea.Cmd {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || strings.ContainsAny(raw, "\n\r\t <>") {
		m.status = "This message has no source link"
		return nil
	}
	return m.openLink(raw, "Opened the original message")
}

func (m *Model) openLink(link, status string) tea.Cmd {
	if m.opts.Open == nil {
		m.status = "Opening links is unavailable in this view"
		return nil
	}
	return func() tea.Msg { return statusMsg{text: status, err: m.opts.Open(link)} }
}
