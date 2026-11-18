package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// wrapLine soft-wraps one logical line into display rows of at most width
// cells, breaking at spaces when possible and mid-word only when a single
// word overflows. Continuation rows inherit the line's leading indentation
// so wrapped list items stay visually nested.
func wrapLine(line string, width int) []string {
	width = max(8, width)
	if lipgloss.Width(line) <= width {
		return []string{line}
	}
	indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
	cont := strings.Repeat(" ", lipgloss.Width(indent)+2)
	if len(cont) >= width {
		cont = strings.Repeat(" ", min(2, width-1))
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
		if current == "" {
			current = candidate
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

func limitedWrap(text string, width, limit int) []string {
	rows := wrapLine(text, width)
	if len(rows) > limit {
		rows = rows[:limit]
		rows[limit-1] = ansi.Truncate(rows[limit-1], max(0, width-1), "") + "…"
	}
	return rows
}
