package messageui

import tea "github.com/charmbracelet/bubbletea"

func (m *Model) trashConversation(trashed bool) tea.Cmd {
	item := m.current()
	if item == nil {
		return nil
	}
	id := item.Conversation.ID
	m.busy = true
	return func() tea.Msg {
		c, cancel := ctx()
		defer cancel()
		e := m.api.Do(c, "POST", "/trash", map[string]any{"conversation": id, "trashed": trashed}, nil)
		text := "Restored conversation"
		if trashed {
			text = "Moved to Amythest trash. T opens trash; u restores."
		}
		return statusMsg{text: text, err: e}
	}
}
