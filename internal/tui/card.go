package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/base698/amythest/internal/apiclient"
	"github.com/base698/amythest/internal/herdr"
	"github.com/base698/amythest/internal/kanban/board"
	"github.com/base698/amythest/internal/tasks"
)

var checkboxLineRe = regexp.MustCompile(`^(\s*[-*+]\s+\[)(.)(\])\s*(.*)$`)

type cardView struct {
	client      *apiclient.Client
	boardName   string
	card        board.Card
	lines       []string // description split on \n; index = 0-based line
	checkIdxs   []int    // indexes into lines that are checkboxes
	focus       int      // index into checkIdxs
	offset      int      // scroll offset in display rows
	busy        bool
	pendingEdit bool
	now         func() time.Time
	find        finder
	comment     textinput.Model
	commenting  bool
	picker      movePicker
	agents      agentPicker
	del         confirm
}

// cardAgentsMsg carries the herdr agent list for this card's send picker.
type cardAgentsMsg struct {
	cardID string
	agents []herdr.Agent
}

// cardMovePickerMsg mirrors boardMovePickerMsg for the card view.
type cardMovePickerMsg struct {
	cardID string
	boards []board.BoardSummary
}

func newCardView(client *apiclient.Client, boardName string, card board.Card) *cardView {
	ci := textinput.New()
	ci.Prompt = "comment: "
	ci.CharLimit = 4000
	v := &cardView{client: client, boardName: boardName, card: card, now: time.Now, find: newFinder(), comment: ci}
	v.setDescription(card.Description)
	return v
}

// newCardViewEditing opens the card with $EDITOR immediately — used by the
// board and today views' "e" shortcut.
func newCardViewEditing(client *apiclient.Client, boardName string, card board.Card) *cardView {
	v := newCardView(client, boardName, card)
	v.pendingEdit = true
	return v
}

func (v *cardView) setDescription(desc string) {
	v.card.Description = desc
	v.lines = strings.Split(desc, "\n")
	v.checkIdxs = v.checkIdxs[:0]
	for i, line := range v.lines {
		if checkboxLineRe.MatchString(line) {
			v.checkIdxs = append(v.checkIdxs, i)
		}
	}
	if v.focus >= len(v.checkIdxs) {
		v.focus = max(0, len(v.checkIdxs)-1)
	}
}

func (v *cardView) Title() string   { return v.card.Title }
func (v *cardView) Busy() bool      { return v.busy }
func (v *cardView) Capturing() bool {
	return v.find.active() || v.commenting || v.picker.active || v.agents.active || v.del.active
}

func (v *cardView) Init() tea.Cmd {
	if v.pendingEdit {
		v.pendingEdit = false
		return v.editCmd()
	}
	return nil
}

type editorDoneMsg struct {
	cardID string
	path   string
	err    error
}

// editCmd suspends the TUI and opens $EDITOR (VISUAL > EDITOR > vi) on the
// whole card — a key/value header plus the description — so title, status,
// priority, due, labels, and blocked are all editable in one place.
func (v *cardView) editCmd() tea.Cmd {
	tmp, err := os.CreateTemp("", "amy-card-*.md")
	if err != nil {
		return func() tea.Msg { return fail(err) }
	}
	if _, err := tmp.WriteString(serializeCardEdit(v.card)); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return func() tea.Msg { return fail(err) }
	}
	tmp.Close()
	cardID := v.card.ID
	return tea.ExecProcess(editorExec(tmp.Name()), func(err error) tea.Msg {
		return editorDoneMsg{cardID: cardID, path: tmp.Name(), err: err}
	})
}

// editorExec builds the $VISUAL/$EDITOR/vi invocation for a file.
func editorExec(path string) *exec.Cmd {
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		editor = "vi"
	}
	parts := strings.Fields(editor)
	return exec.Command(parts[0], append(parts[1:], path)...)
}

func (v *cardView) Update(msg tea.Msg) (view, tea.Cmd) {
	switch msg := msg.(type) {
	case editorDoneMsg:
		if msg.cardID != v.card.ID {
			return v, nil
		}
		edited, readErr := os.ReadFile(msg.path)
		if msg.err != nil {
			os.Remove(msg.path)
			return v, func() tea.Msg { return fail(msg.err) }
		}
		if readErr != nil {
			os.Remove(msg.path)
			return v, func() tea.Msg { return fail(readErr) }
		}
		patch, err := parseCardEdit(string(edited), v.card)
		if err != nil {
			// Keep the file so the edit isn't lost to a syntax slip.
			return v, flash(fmt.Sprintf("edit not saved: %v — fix %s and press e", err, msg.path))
		}
		os.Remove(msg.path)
		if emptyPatch(patch) {
			return v, flash("no changes")
		}
		v.busy = true
		client, boardName, cardID := v.client, v.boardName, v.card.ID
		prev := v.card.Status
		return v, func() tea.Msg {
			card, err := client.PatchCard(context.Background(), boardName, cardID, patch)
			if err != nil {
				return fail(err)
			}
			if card.Status == board.Done && prev != board.Done {
				return cardArchivedMsg{board: boardName, card: card, prevStatus: prev}
			}
			return cardSavedMsg{card}
		}

	case cardSavedMsg:
		v.busy = false
		if msg.card != nil && msg.card.ID == v.card.ID {
			status := v.card.Status
			v.card = *msg.card
			v.setDescription(msg.card.Description)
			if msg.card.Status == board.Done && status != board.Done {
				return v, popView() // card archived out from under this view
			}
		}
		return v, nil

	case cardArchivedMsg:
		v.busy = false
		if msg.card != nil && msg.card.ID == v.card.ID {
			return v, popView() // this card just got archived; back to the list
		}
		return v, nil

	case cardMovePickerMsg:
		if msg.cardID != v.card.ID {
			return v, nil
		}
		v.busy = false
		v.picker.open(v.card.Title, buildMoveOptions(v.card.Status, v.boardName, msg.boards))
		return v, nil

	case cardAgentsMsg:
		if msg.cardID != v.card.ID {
			return v, nil
		}
		v.busy = false
		v.agents.open(v.card.Title, msg.agents)
		return v, nil

	case agentPromptSentMsg:
		if msg.id != v.card.ID {
			return v, nil
		}
		v.busy = false
		return v, flash("sent to agent ✓")

	case cardMovedBoardMsg:
		v.busy = false
		if msg.cardID == v.card.ID {
			return v, popView() // the card left this board; back to the list
		}
		return v, nil

	case cardDeletedMsg:
		v.busy = false
		if msg.cardID == v.card.ID {
			return v, popView()
		}
		return v, nil

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
		if v.commenting {
			return v.handleCommentKey(msg)
		}
		if v.picker.active {
			choice := v.picker.handleKey(msg)
			if choice == nil {
				return v, nil
			}
			if choice.boardName != "" {
				return v.moveSelfToBoard(choice.boardName)
			}
			return v.moveSelf(choice.status)
		}
		if v.agents.active {
			agent := v.agents.handleKey(msg)
			if agent == nil {
				return v, nil
			}
			v.busy = true
			return v, sendToAgentCmd(*agent, v.card.ID, v.card.Title, cardContextPrompt(v.card, v.boardName, v.client.Endpoint()))
		}
		if v.del.active {
			if v.del.handleKey(msg) {
				v.busy = true
				return v, deleteCardCmd(v.client, v.boardName, v.card.ID, v.card.Title)
			}
			return v, nil
		}
		switch msg.String() {
		case "j", "down":
			if v.focus < len(v.checkIdxs)-1 {
				v.focus++
			} else {
				v.offset++
			}
		case "k", "up":
			if v.focus > 0 {
				v.focus--
			} else if v.offset > 0 {
				v.offset--
			}
		case " ":
			if v.busy || len(v.checkIdxs) == 0 {
				return v, nil
			}
			lineIdx := v.checkIdxs[v.focus]
			m := checkboxLineRe.FindStringSubmatch(v.lines[lineIdx])
			if m == nil {
				return v, nil
			}
			v.busy = true
			return v, v.toggleCmd(lineIdx, m[2] == " ")
		case "d":
			return v.moveSelf(board.Done)
		case "D":
			v.del.open(fmt.Sprintf("permanently delete card %q (comments and attachments too)?", v.card.Title))
		case "m":
			if v.busy {
				return v, nil
			}
			v.busy = true
			client, cardID := v.client, v.card.ID
			return v, func() tea.Msg {
				boards, err := client.ListBoards(context.Background())
				if err != nil {
					return fail(err)
				}
				return cardMovePickerMsg{cardID: cardID, boards: boards}
			}
		case "e":
			if !v.busy {
				return v, v.editCmd()
			}
		case "c":
			v.commenting = true
			v.comment.SetValue("")
			return v, v.comment.Focus()
		case "a":
			if v.busy {
				return v, nil
			}
			v.busy = true
			cardID := v.card.ID
			return v, listAgentsCmd(func(agents []herdr.Agent) tea.Msg {
				return cardAgentsMsg{cardID: cardID, agents: agents}
			})
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
		}
	}
	return v, nil
}

func (v *cardView) handleCommentKey(msg tea.KeyMsg) (view, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		v.commenting = false
		v.comment.Blur()
		return v, nil
	case tea.KeyEnter:
		body := strings.TrimSpace(v.comment.Value())
		v.commenting = false
		v.comment.Blur()
		if body == "" {
			return v, nil
		}
		v.busy = true
		client, boardName, cardID := v.client, v.boardName, v.card.ID
		return v, func() tea.Msg {
			card, err := client.AddComment(context.Background(), boardName, cardID, body)
			if err != nil {
				return fail(err)
			}
			return cardSavedMsg{card}
		}
	}
	var cmd tea.Cmd
	v.comment, cmd = v.comment.Update(msg)
	return v, cmd
}

// toggleCmd re-fetches the board so the read-modify-write happens against the
// freshest description (cards have no version field; last write wins). The
// focused line must still say the same thing — otherwise abort and refresh.
func (v *cardView) toggleCmd(lineIdx int, done bool) tea.Cmd {
	client, boardName, cardID := v.client, v.boardName, v.card.ID
	wantLine := v.lines[lineIdx]
	now := v.now()
	return func() tea.Msg {
		ctx := context.Background()
		b, err := client.GetBoard(ctx, boardName)
		if err != nil {
			return fail(err)
		}
		var fresh *board.Card
		for i := range b.Cards {
			if b.Cards[i].ID == cardID {
				fresh = &b.Cards[i]
				break
			}
		}
		if fresh == nil {
			return fail(fmt.Errorf("card %s is no longer on board %s", cardID, boardName))
		}
		freshLines := strings.Split(fresh.Description, "\n")
		target := -1
		if lineIdx < len(freshLines) && freshLines[lineIdx] == wantLine {
			target = lineIdx
		} else {
			// The description changed underneath us; accept a unique
			// text match, otherwise make the user look first.
			for i, line := range freshLines {
				if line == wantLine {
					if target != -1 {
						return fail(fmt.Errorf("description changed on server; refresh (r) and retry"))
					}
					target = i
				}
			}
		}
		if target == -1 {
			return fail(fmt.Errorf("checklist line changed on server; refresh (r) and retry"))
		}
		// tasks.ToggleLine is 1-based; description lines are 0-based.
		newBody, _, err := tasks.ToggleLine([]byte(fresh.Description), target+1, done, now)
		if err != nil {
			return fail(err)
		}
		desc := string(newBody)
		card, err := client.PatchCard(ctx, boardName, cardID, apiclient.CardPatch{Description: &desc})
		if err != nil {
			return fail(err)
		}
		return cardSavedMsg{card}
	}
}

func (v *cardView) moveSelfToBoard(destination string) (view, tea.Cmd) {
	if v.busy {
		return v, nil
	}
	v.busy = true
	client, boardName, cardID := v.client, v.boardName, v.card.ID
	return v, func() tea.Msg {
		if err := client.MoveCardToBoard(context.Background(), boardName, cardID, destination); err != nil {
			return fail(err)
		}
		return cardMovedBoardMsg{from: boardName, to: destination, cardID: cardID}
	}
}

func (v *cardView) moveSelf(status board.Status) (view, tea.Cmd) {
	if v.busy || v.card.Status == status {
		return v, nil
	}
	v.busy = true
	client, boardName, cardID := v.client, v.boardName, v.card.ID
	prev := v.card.Status
	return v, func() tea.Msg {
		if status == board.Done {
			card, err := client.PatchCard(context.Background(), boardName, cardID, apiclient.CardPatch{Status: &status})
			if err != nil {
				return fail(err)
			}
			return cardArchivedMsg{board: boardName, card: card, prevStatus: prev}
		}
		if err := client.MoveCard(context.Background(), boardName, cardID, status, ""); err != nil {
			return fail(err)
		}
		return cardSavedMsg{}
	}
}

// searchJump moves focus (for checkbox hits) or the scroll offset to the
// next description line matching the query.
func (v *cardView) searchJump(dir int) tea.Cmd {
	from := v.offset
	if len(v.checkIdxs) > 0 {
		from = v.checkIdxs[v.focus]
	}
	hit := findMatch(v.lines, from, dir, v.find.query)
	if hit < 0 {
		return flash("no match: " + v.find.query)
	}
	for fi, li := range v.checkIdxs {
		if li == hit {
			v.focus = fi
			return nil
		}
	}
	v.offset = hit
	return nil
}

// displayRow is one terminal row of the card body after soft-wrapping.
type displayRow struct {
	text string
	line int // logical description line, or -1 for comments/chrome
}

func (v *cardView) bodyRows(width int) []displayRow {
	var rows []displayRow
	for i, line := range v.lines {
		for _, wrapped := range wrapLine(line, width) {
			rows = append(rows, displayRow{text: wrapped, line: i})
		}
	}
	rows = append(rows, displayRow{line: -1})
	header := fmt.Sprintf("Comments (%d)", len(v.card.Comments))
	if len(v.card.Comments) == 0 {
		header += " — c to add one"
	}
	rows = append(rows, displayRow{text: groupHeaderStyle.Render(header), line: -1})
	for _, comment := range v.card.Comments {
		meta := comment.CreatedAt.Local().Format("2006-01-02 15:04")
		if comment.Author != "" {
			meta = comment.Author + " · " + meta
		}
		rows = append(rows, displayRow{text: dimStyle.Render("· " + meta), line: -1})
		for _, bodyLine := range strings.Split(comment.Body, "\n") {
			for _, wrapped := range wrapLine("  "+bodyLine, width) {
				rows = append(rows, displayRow{text: wrapped, line: -1})
			}
		}
	}
	return rows
}

func (v *cardView) View(width, height int) string {
	if v.picker.active {
		return v.picker.view()
	}
	if v.agents.active {
		return v.agents.view()
	}
	var b strings.Builder
	var meta []string
	meta = append(meta, statusLabels[v.card.Status])
	if v.card.Priority != "" && v.card.Priority != board.P3 {
		meta = append(meta, priorityBadge(string(v.card.Priority)))
	}
	if v.card.DueDate != "" {
		meta = append(meta, dueStyle.Render("due "+v.card.DueDate))
	}
	if v.card.Blocked {
		meta = append(meta, blockedStyle.Render("[blocked]"))
	}
	if len(v.card.Labels) > 0 {
		meta = append(meta, dimStyle.Render(strings.Join(v.card.Labels, ", ")))
	}
	b.WriteString(" " + columnTitleStyle.Render(v.card.Title) + "\n")
	b.WriteString(" " + strings.Join(meta, " · ") + "\n\n")

	bodyWidth := max(20, width-4)
	rows := v.bodyRows(bodyWidth)

	focusLine := -1
	if len(v.checkIdxs) > 0 {
		focusLine = v.checkIdxs[v.focus]
	}
	// First display row of the focused logical line, for scroll math.
	focusRow := -1
	for i, row := range rows {
		if row.line == focusLine && focusLine >= 0 {
			focusRow = i
			break
		}
	}
	bodyHeight := height - 5
	start := v.offset
	if start > len(rows)-1 {
		start = max(0, len(rows)-1)
		v.offset = start
	}
	if focusRow >= 0 {
		if focusRow < start {
			start = focusRow
		}
		if focusRow >= start+bodyHeight {
			start = focusRow - bodyHeight + 1
		}
	}
	end := min(len(rows), start+bodyHeight)
	for i := start; i < end; i++ {
		row := rows[i]
		if row.line < 0 {
			b.WriteString(" " + row.text + "\n")
			continue
		}
		b.WriteString(v.renderBodyRow(row, row.line == focusLine) + "\n")
	}

	hint := dimStyle.Render(" space toggle · e edit · c comment · d done · m move · / search · esc back")
	if len(v.checkIdxs) == 0 {
		hint = dimStyle.Render(" (no checklist — e edit, c comment, d done, m move, esc back)")
	}
	if bar := v.find.bar(); bar != "" {
		hint = " " + bar
	}
	if v.commenting {
		hint = " " + v.comment.View()
	}
	if v.del.active {
		hint = v.del.bar()
	}
	b.WriteString(hint)
	return b.String()
}

func (v *cardView) renderBodyRow(row displayRow, focused bool) string {
	prefix := "  "
	if focused && strings.TrimSpace(row.text) != "" {
		prefix = cursorStyle.Render("▸ ")
	}
	text := row.text
	if m := checkboxLineRe.FindStringSubmatch(v.lines[row.line]); m != nil {
		if m[2] != " " {
			text = doneStyle.Render(text)
		} else if focused {
			text = cursorStyle.Render(text)
		}
	} else if v.find.query != "" {
		text = highlight(text, v.find.query)
	}
	return prefix + text
}
