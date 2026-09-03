package tui

import "github.com/charmbracelet/lipgloss"

// The package styles every view renders with. They are populated (and
// re-populated on theme switch) by applyTheme in theme.go — the palette
// values live there, not here.
var (
	headerStyle lipgloss.Style
	statusStyle lipgloss.Style

	groupHeaderStyle lipgloss.Style
	cursorStyle      lipgloss.Style
	dimStyle         lipgloss.Style
	doneStyle        lipgloss.Style
	dueStyle         lipgloss.Style
	blockedStyle     lipgloss.Style

	searchHitStyle lipgloss.Style
	linkStyle      lipgloss.Style
	dangerStyle    lipgloss.Style

	columnStyle      lipgloss.Style
	columnFocusStyle lipgloss.Style
	columnTitleStyle lipgloss.Style

	// ASCII badges for chrome; emoji stay confined to free text where
	// width miscounts can't break the layout.
	priorityStyles map[string]lipgloss.Style
)

func priorityBadge(p string) string {
	if style, ok := priorityStyles[p]; ok {
		return style.Render("[" + p + "]")
	}
	return ""
}
