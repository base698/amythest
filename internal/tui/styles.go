package tui

import "github.com/charmbracelet/lipgloss"

var (
	headerStyle = lipgloss.NewStyle().Bold(true).
			Foreground(lipgloss.Color("15")).Background(lipgloss.Color("54"))
	statusStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("245")).Background(lipgloss.Color("236"))

	groupHeaderStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("110"))
	cursorStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	dimStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("242"))
	doneStyle        = lipgloss.NewStyle().Strikethrough(true).Foreground(lipgloss.Color("242"))
	dueStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("179"))
	blockedStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))

	searchHitStyle = lipgloss.NewStyle().Bold(true).
			Foreground(lipgloss.Color("16")).Background(lipgloss.Color("179"))
	linkStyle = lipgloss.NewStyle().Underline(true).Foreground(lipgloss.Color("111"))

	columnStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("240")).
			Padding(0, 1)
	columnFocusStyle = columnStyle.
				BorderForeground(lipgloss.Color("212"))
	columnTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("110"))

	// ASCII badges for chrome; emoji stay confined to free text where
	// width miscounts can't break the layout.
	priorityStyles = map[string]lipgloss.Style{
		"p0": lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("203")),
		"p1": lipgloss.NewStyle().Foreground(lipgloss.Color("215")),
		"p2": lipgloss.NewStyle().Foreground(lipgloss.Color("110")),
		"p3": dimStyle,
	}
)

func priorityBadge(p string) string {
	if style, ok := priorityStyles[p]; ok {
		return style.Render("[" + p + "]")
	}
	return ""
}
