package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
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
	find    finder
	picker  movePicker

	newCard    textinput.Model
	addingCard bool
	del        confirm
	delID      string
	delTitle   string
}

func newBoardView(client *apiclient.Client, name string) *boardView {
	ci := textinput.New()
	ci.CharLimit = 200
	return &boardView{client: client, name: name, find: newFinder(), newCard: ci}
}

func (v *boardView) Title() string { return v.name }
func (v *boardView) Busy() bool    { return v.busy }
func (v *boardView) Capturing() bool {
	return v.find.active() || v.picker.active || v.addingCard || v.del.active
}

// cardCreatedMsg announces a new card so board views refresh.
type cardCreatedMsg struct {
	board string
	card  *board.Card
}

// boardMovePickerMsg carries the board list needed to offer cross-board
// destinations; typed per-view so the stack broadcast can't open two pickers.
type boardMovePickerMsg struct {
	board  string
	cardID string
	boards []board.BoardSummary
}

// cardMovedBoardMsg announces a completed cross-board transfer.
type cardMovedBoardMsg struct {
	from, to, cardID string
}

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

	case cardArchivedMsg:
		if msg.board != v.name {
			return v, nil
		}
		v.busy = true
		v.archiveLoaded = false // the done column just gained a card
		return v, tea.Batch(v.loadCmd(), v.maybeLoadArchive())

	case cardRestoredMsg:
		if msg.board != v.name {
			return v, nil
		}
		v.busy = true
		v.archiveLoaded = false
		return v, tea.Batch(v.loadCmd(), v.maybeLoadArchive())

	case errMsg:
		v.busy = false
		return v, nil

	case boardMovePickerMsg:
		if msg.board != v.name {
			return v, nil
		}
		v.busy = false
		card := v.currentCard()
		if card == nil || card.ID != msg.cardID {
			return v, nil
		}
		v.picker.open(card.Title, buildMoveOptions(card.Status, v.name, msg.boards))
		return v, nil

	case cardMovedBoardMsg:
		if msg.from != v.name && msg.to != v.name {
			return v, nil
		}
		v.busy = true
		return v, v.loadCmd()

	case cardCreatedMsg:
		if msg.board != v.name {
			return v, nil
		}
		v.busy = true
		return v, v.loadCmd()

	case cardDeletedMsg:
		if msg.board != v.name {
			return v, nil
		}
		v.busy = true
		v.archiveLoaded = false
		return v, tea.Batch(v.loadCmd(), v.maybeLoadArchive())

	case tea.KeyMsg:
		if v.find.active() {
			committed, cmd := v.find.handleKey(msg)
			if committed {
				return v, v.searchJump(1)
			}
			return v, cmd
		}
		if v.picker.active {
			choice := v.picker.handleKey(msg)
			if choice == nil {
				return v, nil
			}
			if choice.boardName != "" {
				return v.moveCurrentToBoard(choice.boardName)
			}
			return v.moveCurrent(choice.status)
		}
		if v.addingCard {
			return v.handleNewCardKey(msg)
		}
		if v.del.active {
			if v.del.handleKey(msg) {
				v.busy = true
				return v, deleteCardCmd(v.client, v.name, v.delID, v.delTitle)
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
		case "D":
			if card := v.currentCard(); card != nil {
				v.delID, v.delTitle = card.ID, card.Title
				v.del.open(fmt.Sprintf("permanently delete card %q (comments and attachments too)?", card.Title))
			}
		case "+":
			target := v.newCardStatus()
			v.newCard.Prompt = "new card (" + statusLabels[target] + "): "
			v.newCard.SetValue("")
			v.addingCard = true
			return v, v.newCard.Focus()
		case "d":
			return v.moveCurrent(board.Done)
		case "m":
			if card := v.currentCard(); card != nil {
				v.busy = true
				client, name, cardID := v.client, v.name, card.ID
				return v, func() tea.Msg {
					boards, err := client.ListBoards(context.Background())
					if err != nil {
						return fail(err)
					}
					return boardMovePickerMsg{board: name, cardID: cardID, boards: boards}
				}
			}
		}
	}
	return v, nil
}

// newCardStatus is the column a "+" card lands in: the focused column, or
// Triage when the Done archive is focused.
func (v *boardView) newCardStatus() board.Status {
	status := boardColumns[v.col]
	if status == board.Done {
		return board.Triage
	}
	return status
}

func (v *boardView) handleNewCardKey(msg tea.KeyMsg) (view, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		v.addingCard = false
		v.newCard.Blur()
		return v, nil
	case tea.KeyEnter:
		title := strings.TrimSpace(v.newCard.Value())
		v.addingCard = false
		v.newCard.Blur()
		if title == "" {
			return v, nil
		}
		v.busy = true
		client, name, status := v.client, v.name, v.newCardStatus()
		return v, func() tea.Msg {
			card, err := client.CreateCard(context.Background(), name, title, status)
			if err != nil {
				return fail(err)
			}
			return cardCreatedMsg{board: name, card: card}
		}
	}
	var cmd tea.Cmd
	v.newCard, cmd = v.newCard.Update(msg)
	return v, cmd
}

func (v *boardView) moveCurrentToBoard(destination string) (view, tea.Cmd) {
	card := v.currentCard()
	if card == nil || v.busy {
		return v, nil
	}
	if boardColumns[v.col] == board.Done {
		return v, flash("restore the card first, then move it between boards")
	}
	v.busy = true
	client, name, cardID := v.client, v.name, card.ID
	return v, func() tea.Msg {
		if err := client.MoveCardToBoard(context.Background(), name, cardID, destination); err != nil {
			return fail(err)
		}
		return cardMovedBoardMsg{from: name, to: destination, cardID: cardID}
	}
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
		// m+key on an archived card restores it into that column.
		v.busy = true
		client, name, id := v.client, v.name, card.ID
		return v, func() tea.Msg {
			if err := client.RestoreCard(context.Background(), name, id, status); err != nil {
				return fail(err)
			}
			return cardRestoredMsg{board: name, cardID: id, status: status}
		}
	}
	v.busy = true
	client, name, id, prev := v.client, v.name, card.ID, card.Status
	return v, func() tea.Msg {
		if status == board.Done {
			done := board.Done
			saved, err := client.PatchCard(context.Background(), name, id, apiclient.CardPatch{Status: &done})
			if err != nil {
				return fail(err)
			}
			return cardArchivedMsg{board: name, card: saved, prevStatus: prev}
		}
		if err := client.MoveCard(context.Background(), name, id, status, ""); err != nil {
			return fail(err)
		}
		return cardSavedMsg{}
	}
}

func (v *boardView) View(width, height int) string {
	if !v.loaded {
		return "\n  loading board…"
	}
	if v.picker.active {
		return v.picker.view()
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
	hint := dimStyle.Render(" enter open · + new card · e edit · d done · m move · / search · h/l columns")
	if bar := v.find.bar(); bar != "" {
		hint = " " + bar
	}
	if v.addingCard {
		hint = " " + v.newCard.View()
	}
	if v.del.active {
		hint = v.del.bar()
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
