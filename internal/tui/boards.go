package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/base698/amythest/internal/apiclient"
	"github.com/base698/amythest/internal/kanban/board"
	"github.com/base698/amythest/internal/source/azboards"
)

// boardsView lists the amythest kanban boards plus any Azure Boards virtual
// boards from cli.yaml (marked [azure]); both open with enter, transparently.
type boardsView struct {
	client *apiclient.Client
	az     *azboards.Source // nil when no azboards source is configured
	boards []board.BoardSummary
	cursor int
	busy   bool
	loaded bool
	find   finder
}

func newBoardsView(client *apiclient.Client, az *azboards.Source) *boardsView {
	return &boardsView{client: client, az: az, find: newFinder()}
}

func (v *boardsView) azBoards() []azboards.BoardConfig {
	if v.az == nil {
		return nil
	}
	return v.az.Boards()
}

// rowCount is the full cursor range: local boards then virtual ones.
func (v *boardsView) rowCount() int { return len(v.boards) + len(v.azBoards()) }

func (v *boardsView) Title() string   { return "boards" }
func (v *boardsView) Busy() bool      { return v.busy }
func (v *boardsView) Capturing() bool { return v.find.active() }

func (v *boardsView) searchTexts() []string {
	texts := make([]string, 0, v.rowCount())
	for _, b := range v.boards {
		texts = append(texts, b.Name+" "+b.DisplayName)
	}
	for _, b := range v.azBoards() {
		texts = append(texts, b.Name+" azure")
	}
	return texts
}

func (v *boardsView) Init() tea.Cmd {
	v.busy = true
	client := v.client
	return func() tea.Msg {
		boards, err := client.ListBoards(context.Background())
		if err != nil {
			return fail(err)
		}
		visible := boards[:0]
		for _, b := range boards {
			if !b.Archived {
				visible = append(visible, b)
			}
		}
		return boardsLoadedMsg{visible}
	}
}

func (v *boardsView) Update(msg tea.Msg) (view, tea.Cmd) {
	switch msg := msg.(type) {
	case boardsLoadedMsg:
		v.busy = false
		v.loaded = true
		v.boards = msg.boards
		if v.cursor >= v.rowCount() {
			v.cursor = max(0, v.rowCount()-1)
		}
		return v, nil

	case errMsg:
		v.busy = false
		return v, nil

	case tea.KeyMsg:
		if v.find.active() {
			committed, cmd := v.find.handleKey(msg)
			if committed {
				if hit := findMatch(v.searchTexts(), v.cursor, 1, v.find.query); hit >= 0 {
					v.cursor = hit
				} else {
					return v, flash("no match: " + v.find.query)
				}
			}
			return v, cmd
		}
		switch msg.String() {
		case "j", "down":
			if v.cursor < v.rowCount()-1 {
				v.cursor++
			}
		case "k", "up":
			if v.cursor > 0 {
				v.cursor--
			}
		case "/":
			return v, v.find.start()
		case "n", "N":
			dir := 1
			if msg.String() == "N" {
				dir = -1
			}
			if hit := findMatch(v.searchTexts(), v.cursor, dir, v.find.query); hit >= 0 {
				v.cursor = hit
			}
		case "r":
			return v, v.Init()
		case "enter":
			if v.cursor < len(v.boards) {
				return v, pushView(newBoardView(v.client, v.boards[v.cursor].Name))
			}
			if az := v.azBoards(); v.cursor-len(v.boards) < len(az) {
				return v, pushView(newAZBoardView(v.az, az[v.cursor-len(v.boards)]))
			}
		}
	}
	return v, nil
}

func (v *boardsView) View(width, height int) string {
	if !v.loaded {
		return "\n  loading boards…"
	}
	if v.rowCount() == 0 {
		return "\n  no boards"
	}
	var b strings.Builder
	b.WriteString("\n")
	for i, bs := range v.boards {
		prefix := "   "
		if i == v.cursor {
			prefix = cursorStyle.Render(" ▸ ")
		}
		name := bs.DisplayName
		if name == "" {
			name = bs.Name
		}
		if i == v.cursor {
			name = cursorStyle.Render(name)
		}
		open := 0
		for _, status := range board.ActiveStatuses {
			open += bs.Counts[status]
		}
		counts := dimStyle.Render(fmt.Sprintf("%d open · %d done", open, bs.Counts[board.Done]))
		pin := ""
		if bs.Pinned {
			pin = dueStyle.Render(" *")
		}
		b.WriteString(fmt.Sprintf("%s%s%s  %s\n", prefix, name, pin, counts))
	}
	for i, ab := range v.azBoards() {
		row := len(v.boards) + i
		prefix := "   "
		name := ab.Name
		if row == v.cursor {
			prefix = cursorStyle.Render(" ▸ ")
			name = cursorStyle.Render(name)
		}
		b.WriteString(fmt.Sprintf("%s%s  %s\n", prefix, name, dueStyle.Render("[azure]")))
	}
	if bar := v.find.bar(); bar != "" {
		b.WriteString(" " + bar + "\n")
	}
	return b.String()
}
