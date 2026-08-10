package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// wrapLine soft-wraps one logical line into display rows of at most width
// cells, breaking at spaces when possible and mid-word only when a single
// word overflows. Continuation rows inherit the line's leading indentation
// so wrapped list items stay visually nested.
func wrapLine(line string, width int) []string {
	if width < 8 {
		width = 8
	}
	if lipgloss.Width(line) <= width {
		return []string{line}
	}
	indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
	cont := strings.Repeat(" ", lipgloss.Width(indent)+2)
	if len(cont) >= width {
		cont = "  "
	}

	var rows []string
	current := ""
	limit := width
	for _, word := range strings.Fields(line) {
		candidate := word
		if current != "" {
			candidate = current + " " + word
		} else if len(rows) == 0 {
			candidate = indent + word
		} else {
			candidate = cont + word
		}
		if lipgloss.Width(candidate) <= limit {
			current = candidate
			continue
		}
		if current != "" {
			rows = append(rows, current)
			current = cont + word
		}
		// A single word longer than the width gets hard-broken.
		for lipgloss.Width(current) > limit {
			cut := truncate(current, limit)
			rows = append(rows, cut)
			current = cont + current[len(cut):]
		}
	}
	if strings.TrimSpace(current) != "" {
		rows = append(rows, current)
	}
	if len(rows) == 0 {
		return []string{line}
	}
	return rows
}
