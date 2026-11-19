package messageui

import (
	"github.com/base698/amythest/internal/messages"
	tea "github.com/charmbracelet/bubbletea"
	"strings"
)

var services = []string{"", "Signal", "Telegram", "Messages", "Gmail", "Outlook", "Slack", "Outlook (Chrome)", "Calendar (Chrome)"}

func (m *Model) utilityKey(k tea.KeyMsg) tea.Cmd {
	if k.String() == "esc" {
		m.mode = "focus"
		return nil
	}
	if m.mode == "service" {
		switch k.String() {
		case "j", "down":
			m.serviceCursor = min(len(services)-1, m.serviceCursor+1)
		case "k", "up":
			m.serviceCursor = max(0, m.serviceCursor-1)
		case "enter":
			m.provider = services[m.serviceCursor]
			m.mode = "focus"
			m.cursor = 0
			return m.load()
		}
		return nil
	}
	if k.String() == "enter" {
		value := strings.TrimSpace(m.link.input.Value())
		if m.mode == "search" {
			m.query = value
			m.mode = "focus"
			m.cursor = 0
			return m.load()
		}
		id := m.link.message.Conversation.ID
		m.mode = "focus"
		m.busy = true
		return func() tea.Msg {
			c, cancel := ctx()
			defer cancel()
			e := m.api.Do(c, "PUT", "/contacts", map[string]string{"conversation": id, "name": value}, nil)
			return statusMsg{text: "Contact name saved", err: e}
		}
	}
	if k.String() == "tab" && m.mode == "contact" {
		for _, name := range m.link.names {
			if strings.HasPrefix(strings.ToLower(name), strings.ToLower(m.link.input.Value())) {
				m.link.input.SetValue(name)
				break
			}
		}
		return nil
	}
	var cmd tea.Cmd
	m.link.input, cmd = m.link.input.Update(k)
	return cmd
}
func (m *Model) utilityView(h int) string {
	m.link.input.Width = max(15, m.w-20)
	lines := []string{}
	if m.mode == "service" {
		lines = append(lines, "Focus a service (keeps Focus / All selection)", "")
		for i, s := range services {
			if s == "" {
				s = "All services"
			}
			p := "  "
			if i == m.serviceCursor {
				p = "› "
			}
			lines = append(lines, p+s)
		}
		lines = append(lines, "", "Enter choose · Esc back")
	} else {
		lines = append(lines, m.link.input.View(), "Enter save · Esc back")
		if m.mode == "contact" {
			lines = append(lines, "Empty clears the override · Tab completes a known name", "")
			for _, name := range m.link.names {
				if strings.Contains(strings.ToLower(name), strings.ToLower(m.link.input.Value())) {
					lines = append(lines, messages.Plain(name))
				}
			}
		}
	}
	return fit(lines, h, m.w-2)
}
