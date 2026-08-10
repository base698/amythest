package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/base698/amythest/internal/apiclient"
	"github.com/base698/amythest/internal/kanban/board"
	"github.com/base698/amythest/internal/tasks"
)

var checkboxLineRe = regexp.MustCompile(`^(\s*[-*+]\s+\[)(.)(\])\s*(.*)$`)

type cardView struct {
	client    *apiclient.Client
	boardName string
	card      board.Card
	lines     []string // description split on \n; index = 0-based line
	checkIdxs []int    // indexes into lines that are checkboxes
	focus     int      // index into checkIdxs
	offset    int
	busy      bool
	moving    bool
	pendingEdit bool
	now       func() time.Time
	find      finder
}

func newCardView(client *apiclient.Client, boardName string, card board.Card) *cardView {
	v := &cardView{client: client, boardName: boardName, card: card, now: time.Now, find: newFinder()}
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
func (v *cardView) Capturing() bool { return v.find.active() }

func (v *cardView) Init() tea.Cmd {
	if v.pendingEdit {
		v.pendingEdit = false
		return v.editCmd()
	}
	return nil
}

type editorDoneMsg struct {
	cardID   string
	path     string
	original string
	err      error
}

// editCmd suspends the TUI and opens $EDITOR (VISUAL > EDITOR > vi) on the
// card description in a temp file; on exit the result is PUT back if changed.
func (v *cardView) editCmd() tea.Cmd {
	tmp, err := os.CreateTemp("", "amy-card-*.md")
	if err != nil {
		return func() tea.Msg { return fail(err) }
	}
	if _, err := tmp.WriteString(v.card.Description); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return func() tea.Msg { return fail(err) }
	}
	tmp.Close()
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		editor = "vi"
	}
	parts := strings.Fields(editor)
	args := append(parts[1:], tmp.Name())
	cardID, original := v.card.ID, v.card.Description
	return tea.ExecProcess(exec.Command(parts[0], args...), func(err error) tea.Msg {
		return editorDoneMsg{cardID: cardID, path: tmp.Name(), original: original, err: err}
	})
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

func (v *cardView) Update(msg tea.Msg) (view, tea.Cmd) {
	switch msg := msg.(type) {
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

	case editorDoneMsg:
		if msg.cardID != v.card.ID {
			return v, nil
		}
		edited, readErr := os.ReadFile(msg.path)
		os.Remove(msg.path)
		if msg.err != nil {
			return v, func() tea.Msg { return fail(msg.err) }
		}
		if readErr != nil {
			return v, func() tea.Msg { return fail(readErr) }
		}
		desc := strings.TrimRight(string(edited), "\n")
		if desc == strings.TrimRight(msg.original, "\n") {
			return v, flash("no changes")
		}
		v.busy = true
		client, boardName, cardID := v.client, v.boardName, v.card.ID
		return v, func() tea.Msg {
			card, err := client.PatchCard(context.Background(), boardName, cardID, apiclient.CardPatch{Description: &desc})
			if err != nil {
				return fail(err)
			}
			return cardSavedMsg{card}
		}

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
				return v.moveSelf(status)
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
		case "m":
			v.moving = true
		case "e":
			if !v.busy {
				return v, v.editCmd()
			}
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

func (v *cardView) View(width, height int) string {
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

	focusLine := -1
	if len(v.checkIdxs) > 0 {
		focusLine = v.checkIdxs[v.focus]
	}
	bodyHeight := height - 5
	start := v.offset
	if focusLine >= 0 {
		if focusLine < start {
			start = focusLine
		}
		if focusLine >= start+bodyHeight {
			start = focusLine - bodyHeight + 1
		}
	}
	end := min(len(v.lines), start+bodyHeight)
	for i := start; i < end; i++ {
		b.WriteString(renderDescriptionLine(v.lines[i], i == focusLine) + "\n")
	}
	hint := dimStyle.Render(" space toggle · e edit · d done · m move · / search · esc back")
	if len(v.checkIdxs) == 0 {
		hint = dimStyle.Render(" (no checklist — e edit, d done, m move, esc back)")
		b.WriteString("\n")
	}
	if bar := v.find.bar(); bar != "" {
		hint = " " + bar
	}
	b.WriteString(hint)
	return b.String()
}

func renderDescriptionLine(line string, focused bool) string {
	prefix := "  "
	if focused {
		prefix = cursorStyle.Render("▸ ")
	}
	if m := checkboxLineRe.FindStringSubmatch(line); m != nil {
		checked := m[2] != " "
		rendered := line
		if checked {
			rendered = doneStyle.Render(line)
		} else if focused {
			rendered = cursorStyle.Render(line)
		}
		return prefix + rendered
	}
	return prefix + line
}
