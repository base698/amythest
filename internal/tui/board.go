package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

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

// columnWindow keeps a column's cursor visible inside a scroll window of
// `visible` rows: it returns the adjusted scroll offset for the column.
func columnWindow(total, cursor, offset, visible int) int {
	if visible < 1 {
		visible = 1
	}
	if cursor >= 0 {
		if cursor < offset {
			offset = cursor
		}
		if cursor >= offset+visible {
			offset = cursor - visible + 1
		}
	}
	if offset > total-visible {
		offset = total - visible
	}
	return max(0, offset)
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

	col      int
	firstCol int
	cursors  [6]int // one per column in boardColumns order
	offsets  [6]int // scroll offset per column
	busy     bool
	loaded   bool
	find     finder
	picker   movePicker

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

// focusFirstMatch moves focus off an emptied column after a filter commit.
func (v *boardView) focusFirstMatch() {
	if len(v.columnCards(v.col)) > 0 {
		return
	}
	for col := range boardColumns {
		if len(v.columnCards(col)) > 0 {
			v.col = col
			v.cursors[col] = 0
			return
		}
	}
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

// columnCards returns the cards in the given column, preserving board order
// and applying the live "/" fuzzy filter.
func (v *boardView) columnCards(col int) []board.Card {
	status := boardColumns[col]
	pool := v.archive
	if status != board.Done {
		if v.b == nil {
			return nil
		}
		pool = v.b.Cards
	}
	query := v.find.liveQuery()
	var cards []board.Card
	for _, c := range pool {
		if c.Status != status && status != board.Done {
			continue
		}
		if query != "" && !fuzzyMatch(c.Title+" "+c.Description, query) {
			continue
		}
		cards = append(cards, c)
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
			v.clampCursors() // the live filter narrows columns as it's typed
			if committed {
				v.focusFirstMatch()
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
	count := 2
	if width < 70 {
		count = 1
	}
	v.firstCol = columnWindow(len(boardColumns), v.col, v.firstCol, count)
	outer := max(6, (width-(count-1))/count)
	inner := max(2, outer-4)
	room := max(1, height-7)
	var columns []string
	for col := v.firstCol; col < min(len(boardColumns), v.firstCol+count); col++ {
		status := boardColumns[col]
		cards := v.columnCards(col)
		title := fmt.Sprintf("%s (%d)", statusLabels[status], len(cards))
		if status == board.Done && !v.archiveLoaded {
			title = "Done (…)"
		}
		blocks := make([][]string, len(cards))
		for i, card := range cards {
			blocks[i] = boardCardRows(card, col == v.col && i == v.cursors[col], inner)
		}
		off := min(v.offsets[col], max(0, len(cards)-1))
		if v.cursors[col] < off {
			off = v.cursors[col]
		}
		endAt := func(start int) int {
			used, end := 0, start
			for end < len(blocks) {
				n := min(room, len(blocks[end]))
				if used+n > room {
					break
				}
				used += n
				end++
			}
			return end
		}
		for off < v.cursors[col] && endAt(off) <= v.cursors[col] {
			off++
		}
		v.offsets[col] = off
		end := endAt(off)
		above, below := "", ""
		if off > 0 {
			above = fmt.Sprintf("↑ %d more", off)
		}
		if end < len(cards) {
			below = fmt.Sprintf("↓ %d more", len(cards)-end)
		}
		lines := []string{columnTitleStyle.Render(ansi.Truncate(title, inner, "…")), dimStyle.Render(above)}
		var content []string
		for i := off; i < end; i++ {
			content = append(content, blocks[i][:min(room, len(blocks[i]))]...)
		}
		if len(cards) == 0 {
			content = append(content, dimStyle.Render("—"))
		}
		for len(content) < room {
			content = append(content, "")
		}
		lines = append(lines, content...)
		lines = append(lines, dimStyle.Render(below))
		style := columnStyle
		if col == v.col {
			style = columnFocusStyle
		}
		if len(columns) > 0 {
			columns = append(columns, " ")
		}
		columns = append(columns, style.Width(outer-2).Render(strings.Join(lines, "\n")))
	}
	nav := fmt.Sprintf(" Columns %d–%d of %d · h/l ←→ columns · j/k ↑↓ cards", v.firstCol+1, min(len(boardColumns), v.firstCol+count), len(boardColumns))
	view := dimStyle.Render(ansi.Truncate(nav, width, "…")) + "\n" + lipgloss.JoinHorizontal(lipgloss.Top, columns...)
	hint := dimStyle.Render(" enter open · + new card · e edit · d done · m move · / filter · h/l columns")
	if bar := v.find.filterBar(); bar != "" {
		hint = " " + bar
	}
	if v.addingCard {
		hint = " " + v.newCard.View()
	}
	if v.del.active {
		hint = v.del.bar()
	}
	return view + "\n" + ansi.Truncate(hint, width, "…")
}

// Cards use their column's width for the title and a short description instead
// of squeezing all six statuses into single-line labels.
func boardCardRows(card board.Card, selected bool, width int) []string {
	var rows []string
	for i, line := range limitedWrap(card.Title, max(1, width-2), 3) {
		prefix := "  "
		if selected {
			line = cursorStyle.Render(line)
			if i == 0 {
				prefix = cursorStyle.Render("▸ ")
			}
		} else {
			line = columnTitleStyle.Render(line)
		}
		rows = append(rows, prefix+line)
	}
	if desc := strings.Join(strings.Fields(card.Description), " "); desc != "" {
		for _, line := range limitedWrap(desc, max(1, width-2), 2) {
			rows = append(rows, "  "+dimStyle.Render(line))
		}
	}
	var meta []string
	if card.Priority != "" && card.Priority != board.P3 {
		meta = append(meta, priorityBadge(string(card.Priority)))
	}
	if card.Blocked {
		meta = append(meta, blockedStyle.Render("blocked"))
	}
	if card.DueDate != "" {
		meta = append(meta, dueStyle.Render("due "+card.DueDate))
	}
	if len(meta) > 0 {
		rows = append(rows, "  "+ansi.Truncate(strings.Join(meta, " · "), max(1, width-2), "…"))
	}
	return append(rows, "")
}

// renderCardLine renders one card as a single row that never exceeds width
// cells — overflow would wrap inside the lipgloss column and break the
// row-per-card scroll math. Tags are dropped before the title is starved.
func renderCardLine(card board.Card, selected bool, width int) string {
	prefix := "  "
	if selected {
		prefix = cursorStyle.Render("▸ ")
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
	tagStr := strings.Join(tags, " ")
	avail := width - 2 // prefix
	if tagStr != "" && lipgloss.Width(tagStr)+10 > avail {
		tagStr = ""
	}
	if tagStr != "" {
		avail -= lipgloss.Width(tagStr) + 1
	}
	title := card.Title
	if lipgloss.Width(title) > avail {
		title = truncate(title, max(1, avail-1)) + "…"
	}
	if selected {
		title = cursorStyle.Render(title)
	}
	line := prefix + title
	if tagStr != "" {
		line += " " + tagStr
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
