package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/base698/amythest/internal/kanban/board"
)

// movePicker is the shared "move card" selector: the six lanes first, then
// every other board as a cross-board destination. Arrows + enter, esc
// cancels, and the old t/b/y/i/v/d lane shortcuts still work inside it.
type moveOption struct {
	status    board.Status // set for lane moves
	boardName string       // set for cross-board moves
	label     string
	current   bool
}

type movePicker struct {
	active  bool
	title   string
	options []moveOption
	cursor  int
}

func buildMoveOptions(currentStatus board.Status, currentBoard string, boards []board.BoardSummary) []moveOption {
	var options []moveOption
	for _, status := range boardColumns {
		option := moveOption{status: status, label: statusLabels[status], current: status == currentStatus}
		if option.current {
			option.label += "  (current)"
		}
		options = append(options, option)
	}
	for _, bs := range boards {
		if bs.Archived || bs.Name == currentBoard {
			continue
		}
		name := bs.DisplayName
		if name == "" {
			name = bs.Name
		}
		options = append(options, moveOption{boardName: bs.Name, label: "→ board: " + name})
	}
	return options
}

func (p *movePicker) open(title string, options []moveOption) {
	p.active = true
	p.title = title
	p.options = options
	p.cursor = 0
	for i, option := range options {
		if option.current {
			p.cursor = i
			break
		}
	}
}

// handleKey drives the picker; it returns the chosen option once the user
// commits. Choosing the current lane just closes the picker.
func (p *movePicker) handleKey(msg tea.KeyMsg) *moveOption {
	switch msg.String() {
	case "esc", "q", "m":
		p.active = false
		return nil
	case "j", "down":
		if p.cursor < len(p.options)-1 {
			p.cursor++
		}
	case "k", "up":
		if p.cursor > 0 {
			p.cursor--
		}
	case "enter":
		p.active = false
		option := p.options[p.cursor]
		if option.current {
			return nil
		}
		return &option
	default:
		if status, ok := moveKeys[msg.String()]; ok {
			for _, option := range p.options {
				if option.status == status && option.boardName == "" {
					p.active = false
					if option.current {
						return nil
					}
					choice := option
					return &choice
				}
			}
		}
	}
	return nil
}

func (p *movePicker) view() string {
	var b strings.Builder
	b.WriteString(" " + columnTitleStyle.Render("Move card") + "  " + dimStyle.Render(p.title) + "\n\n")
	for i, option := range p.options {
		prefix := "   "
		label := option.label
		if i == p.cursor {
			prefix = cursorStyle.Render(" ▸ ")
			label = cursorStyle.Render(option.label)
		} else if option.current {
			label = dimStyle.Render(option.label)
		}
		b.WriteString(prefix + label + "\n")
	}
	b.WriteString("\n" + dimStyle.Render(" enter move · j/k or t/b/y/i/v/d · esc cancel"))
	return b.String()
}
