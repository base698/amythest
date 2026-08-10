package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/base698/amythest/internal/apiclient"
	"github.com/base698/amythest/internal/kanban/board"
)

// columns are the active statuses plus a Done column fed lazily from the
// archive endpoint (GET board returns active cards only).
var boardColumns = append(append([]board.Status{}, board.ActiveStatuses...), board.Done)

var statusLabels = map[board.Status]string{
	board.Triage:     "Triage",
	board.Backlog:    "Backlog",
	board.Ready:      "Ready",
	board.InProgress: "In progress",
	board.Verify:     "Verify",
	board.Done:       "Done",
}

var moveKeys = map[string]board.Status{
	"t": board.Triage,
	"b": board.Backlog,
	"y": board.Ready,
	"i": board.InProgress,
	"v": board.Verify,
	"d": board.Done,
}

type boardView struct {
	client        *apiclient.Client
	name          string
	b             *board.Board
	archive       []board.Card
	archiveLoaded bool

	col     int
	cursors [6]int // one per column in boardColumns order
	busy    bool
	loaded  bool
	moving  bool // "m" pressed, waiting for a destination status key
	find    finder
}

func newBoardView(client *apiclient.Client, name string) *boardView {
	return &boardView{client: client, name: name, find: newFinder()}
}

func (v *boardView) Title() string   { return v.name }
func (v *boardView) Busy() bool      { return v.busy }
func (v *boardView) Capturing() bool { return v.find.active() }

// flatCards enumerates every visible card in column order so search can jump
// across columns; each entry remembers its (column, row) position.
type flatCard struct {
	col, row int
	text     string
}

func (v *boardView) flatCards() []flatCard {
	var flat []flatCard
	for col := range boardColumns {
		for row, card := range v.columnCards(col) {
			flat = append(flat, flatCard{col: col, row: row, text: card.Title + " " + card.Description})
		}
	}
	return flat
}

func (v *boardView) flatIndex(flat []flatCard) int {
	for i, fc := range flat {
		if fc.col == v.col && fc.row == v.cursors[v.col] {
			return i
		}
	}
	return 0
}

func (v *boardView) jumpFlat(flat []flatCard, i int) {
	v.col = flat[i].col
	v.cursors[v.col] = flat[i].row
}

func (v *boardView) searchJump(dir int) tea.Cmd {
	flat := v.flatCards()
	texts := make([]string, len(flat))
	for i, fc := range flat {
		texts[i] = fc.text
	}
	if hit := findMatch(texts, v.flatIndex(flat), dir, v.find.query); hit >= 0 {
		v.jumpFlat(flat, hit)
		return nil
	}
	return flash("no match: " + v.find.query)
}

func (v *boardView) Init() tea.Cmd {
	v.busy = true
	return v.loadCmd()
}

func (v *boardView) loadCmd() tea.Cmd {
	client, name := v.client, v.name
	return func() tea.Msg {
		b, err := client.GetBoard(context.Background(), name)
		if err != nil {
			return fail(err)
		}
		return boardLoadedMsg{b}
	}
}

func (v *boardView) loadArchiveCmd() tea.Cmd {
	client, name := v.client, v.name
	return func() tea.Msg {
		cards, err := client.ListArchive(context.Background(), name, 50)
		if err != nil {
			return fail(err)
		}
		return archiveLoadedMsg{board: name, cards: cards}
	}
}

// columnCards returns the cards in the given column, preserving board order.
func (v *boardView) columnCards(col int) []board.Card {
	status := boardColumns[col]
	if status == board.Done {
		return v.archive
	}
	if v.b == nil {
		return nil
	}
	var cards []board.Card
	for _, c := range v.b.Cards {
		if c.Status == status {
			cards = append(cards, c)
		}
	}
	return cards
}

func (v *boardView) currentCard() *board.Card {
	cards := v.columnCards(v.col)
	cursor := v.cursors[v.col]
	if cursor < 0 || cursor >= len(cards) {
		return nil
	}
	return &cards[cursor]
}

func (v *boardView) clampCursors() {
	for col := range boardColumns {
		if n := len(v.columnCards(col)); v.cursors[col] >= n {
			v.cursors[col] = max(0, n-1)
		}
	}
}

func (v *boardView) Update(msg tea.Msg) (view, tea.Cmd) {
	switch msg := msg.(type) {
	case boardLoadedMsg:
		if msg.b.Name != v.name {
			return v, nil
		}
		v.busy = false
		v.loaded = true
		v.b = msg.b
		v.clampCursors()
		return v, nil

	case archiveLoadedMsg:
		if msg.board != v.name {
			return v, nil
		}
		v.busy = false
		v.archiveLoaded = true
		v.archive = msg.cards
		v.clampCursors()
		return v, nil

	case cardSavedMsg:
		// A child card view saved; refresh to pick up the change.
		v.busy = true
		return v, v.loadCmd()

	case errMsg:
		v.busy = false
		return v, nil

	case tea.KeyMsg:
		if v.find.active() {
			committed, cmd := v.find.handleKey(msg)
			if committed {
				return v, v.searchJump(1)
			}
			return v, cmd
		}
		if v.moving {
			v.moving = false
			if status, ok := moveKeys[msg.String()]; ok {
				return v.moveCurrent(status)
			}
			return v, nil
		}
		switch msg.String() {
		case "/":
			return v, v.find.start()
		case "n", "N":
			if v.find.query != "" {
				dir := 1
				if msg.String() == "N" {
					dir = -1
				}
				return v, v.searchJump(dir)
			}
		case "e":
			if card := v.currentCard(); card != nil && boardColumns[v.col] != board.Done {
				return v, pushView(newCardViewEditing(v.client, v.name, *card))
			}
		}
		switch msg.String() {
		case "h", "left":
			if v.col > 0 {
				v.col--
				return v, v.maybeLoadArchive()
			}
		case "l", "right":
			if v.col < len(boardColumns)-1 {
				v.col++
				return v, v.maybeLoadArchive()
			}
		case "j", "down":
			if v.cursors[v.col] < len(v.columnCards(v.col))-1 {
				v.cursors[v.col]++
			}
		case "k", "up":
			if v.cursors[v.col] > 0 {
				v.cursors[v.col]--
			}
		case "r":
			v.busy = true
			v.archiveLoaded = false
			return v, tea.Batch(v.loadCmd(), v.maybeLoadArchive())
		case "enter":
			if card := v.currentCard(); card != nil {
				return v, pushView(newCardView(v.client, v.name, *card))
			}
		case "d":
			return v.moveCurrent(board.Done)
		case "m":
			if v.currentCard() != nil {
				v.moving = true
			}
		}
	}
	return v, nil
}

func (v *boardView) maybeLoadArchive() tea.Cmd {
	if boardColumns[v.col] == board.Done && !v.archiveLoaded {
		v.busy = true
		return v.loadArchiveCmd()
	}
	return nil
}

func (v *boardView) moveCurrent(status board.Status) (view, tea.Cmd) {
	card := v.currentCard()
	if card == nil || v.busy {
		return v, nil
	}
	if card.Status == status {
		return v, nil
	}
	if boardColumns[v.col] == board.Done {
		return v, flash("archived cards are restored from the web UI")
	}
	v.busy = true
	client, name, id := v.client, v.name, card.ID
	return v, func() tea.Msg {
		var err error
		if status == board.Done {
			done := board.Done
			_, err = client.PatchCard(context.Background(), name, id, apiclient.CardPatch{Status: &done})
		} else {
			err = client.MoveCard(context.Background(), name, id, status, "")
		}
		if err != nil {
			return fail(err)
		}
		return cardSavedMsg{}
	}
}

func (v *boardView) View(width, height int) string {
	if !v.loaded {
		return "\n  loading board…"
	}
	colWidth := max(18, width/len(boardColumns)-2)
	perColumn := max(3, height-4)
	var columns []string
	for col, status := range boardColumns {
		cards := v.columnCards(col)
		title := columnTitleStyle.Render(fmt.Sprintf("%s (%d)", statusLabels[status], len(cards)))
		if status == board.Done && !v.archiveLoaded {
			title = columnTitleStyle.Render("Done (…)")
		}
		lines := []string{title}
		for i, card := range cards {
			if i >= perColumn {
				lines = append(lines, dimStyle.Render(fmt.Sprintf("… %d more", len(cards)-perColumn)))
				break
			}
			lines = append(lines, renderCardLine(card, col == v.col && i == v.cursors[v.col], colWidth-4))
		}
		if len(cards) == 0 {
			lines = append(lines, dimStyle.Render("—"))
		}
		style := columnStyle
		if col == v.col {
			style = columnFocusStyle
		}
		columns = append(columns, style.Width(colWidth).Render(strings.Join(lines, "\n")))
	}
	view := lipgloss.JoinHorizontal(lipgloss.Top, columns...)
	hint := dimStyle.Render(" enter open · e edit · d done · m+key move · / search · h/l columns")
	if v.moving {
		hint = dimStyle.Render(" move to: t triage · b backlog · y ready · i in_progress · v verify · d done")
	}
	if bar := v.find.bar(); bar != "" {
		hint = " " + bar
	}
	return view + "\n" + hint
}

func renderCardLine(card board.Card, selected bool, width int) string {
	prefix := "  "
	if selected {
		prefix = cursorStyle.Render("▸ ")
	}
	title := card.Title
	if width > 6 && lipgloss.Width(title) > width {
		title = truncate(title, width-1) + "…"
	}
	if selected {
		title = cursorStyle.Render(title)
	}
	var tags []string
	if badge := priorityBadge(string(card.Priority)); badge != "" && card.Priority != board.P3 {
		tags = append(tags, badge)
	}
	if card.Blocked {
		tags = append(tags, blockedStyle.Render("[blocked]"))
	}
	if card.DueDate != "" {
		tags = append(tags, dueStyle.Render(card.DueDate))
	}
	line := prefix + title
	if len(tags) > 0 {
		line += " " + strings.Join(tags, " ")
	}
	return line
}

// truncate cuts a string to at most width terminal cells, rune-safe.
func truncate(s string, width int) string {
	var b strings.Builder
	total := 0
	for _, r := range s {
		w := lipgloss.Width(string(r))
		if total+w > width {
			break
		}
		b.WriteRune(r)
		total += w
	}
	return b.String()
}
