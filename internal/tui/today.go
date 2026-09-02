package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/base698/amythest/internal/apiclient"
	"github.com/base698/amythest/internal/kanban/board"
	"github.com/base698/amythest/internal/source"
	srcamythest "github.com/base698/amythest/internal/source/amythest"
	"github.com/base698/amythest/internal/tasks"
)

// todayItem is one row of the focus view: a normalized source.Item plus its
// assigned section. Amythest-native behaviors (toggle, delete, edit, open)
// reach the underlying task/card through Payload type-switches; items from
// other sources fall back to read-only handling here and full interaction in
// their own view.
type todayItem struct {
	section string // "Focus" | "Overdue" | "Due today" | "Done today"
	item    source.Item
}

func (it *todayItem) task() *tasks.Task {
	if p, ok := it.item.Payload.(srcamythest.TaskPayload); ok {
		return p.Task
	}
	return nil
}

func (it *todayItem) card() *srcamythest.CardPayload {
	if p, ok := it.item.Payload.(*srcamythest.CardPayload); ok {
		return p
	}
	return nil
}

func (it *todayItem) isDone() bool {
	if t := it.task(); t != nil {
		return t.Status == tasks.StatusDone
	}
	return it.item.Done
}

func (it *todayItem) text() string { return it.item.Title }
func (it *todayItem) due() string  { return it.item.Due }

type todayLoadedMsg struct {
	items   []todayItem
	srcErrs map[string]error
}

type todayView struct {
	client   *apiclient.Client
	reg      *source.Registry
	items    []todayItem
	cursor   int
	offset   int
	busy     bool
	loaded   bool
	showDone bool // "x": include the Done-today section
	phase    int  // shimmer phase, advanced by the root App
	find     finder
	prompt   duePrompt
	del      confirm
	delItem  todayItem
	now      func() time.Time

	fxFrames []string // ttfx gem intro (AMY_GEM_FX); empty = builtin shimmer
	fxIdx    int
}

// fxActive reports whether the ttfx intro is still playing — the root App
// speeds up the animation tick while it is.
func (v *todayView) fxActive() bool { return v.fxIdx < len(v.fxFrames) }

func newTodayView(client *apiclient.Client, reg *source.Registry) *todayView {
	return &todayView{client: client, reg: reg, find: newFinder(), prompt: newDuePrompt(), now: time.Now}
}

func (v *todayView) Title() string { return "today" }
func (v *todayView) Busy() bool    { return v.busy }
func (v *todayView) Capturing() bool {
	return v.find.active() || v.prompt.active() || v.del.active
}

func (v *todayView) Init() tea.Cmd {
	v.busy = true
	cmds := []tea.Cmd{v.loadCmd()}
	if fx := gemFXCmd(); fx != nil {
		cmds = append(cmds, fx)
	}
	return tea.Batch(cmds...)
}

// loadCmd aggregates every registered source's due items. An amythest error
// fails the view (matching pre-registry behavior); other sources degrade to
// a status-bar note with partial results.
func (v *todayView) loadCmd() tea.Cmd {
	reg := v.reg
	day := v.now().Format("2006-01-02")
	showDone := v.showDone
	return func() tea.Msg {
		ctx := context.Background()
		found, errs := reg.DueItems(ctx, day, showDone)
		if err := errs["amythest"]; err != nil {
			return fail(err)
		}
		items := make([]todayItem, 0, len(found))
		for _, it := range found {
			items = append(items, todayItem{section: source.SectionFor(it, day), item: it})
		}
		sortTodayItems(items)
		return todayLoadedMsg{items: items, srcErrs: errs}
	}
}

var todaySectionOrder = map[string]int{"Focus": 0, "Overdue": 1, "Due today": 2, "Done today": 3}

func sortTodayItems(items []todayItem) {
	sort.SliceStable(items, func(i, j int) bool {
		if a, b := todaySectionOrder[items[i].section], todaySectionOrder[items[j].section]; a != b {
			return a < b
		}
		return items[i].due() < items[j].due()
	})
}

func (v *todayView) searchTexts() []string {
	texts := make([]string, len(v.items))
	for i, it := range v.items {
		texts[i] = it.text()
	}
	return texts
}

func (v *todayView) Update(msg tea.Msg) (view, tea.Cmd) {
	switch msg := msg.(type) {
	case gemFXMsg:
		if msg.err != nil {
			return v, flash(msg.err.Error())
		}
		v.fxFrames = msg.frames
		v.fxIdx = 0
		return v, nil

	case todayLoadedMsg:
		v.busy = false
		v.loaded = true
		v.items = msg.items
		if v.cursor >= len(v.items) {
			v.cursor = max(0, len(v.items)-1)
		}
		if len(msg.srcErrs) > 0 {
			var notes []string
			for name, err := range msg.srcErrs {
				if name != "amythest" {
					notes = append(notes, fmt.Sprintf("%s: %v", name, err))
				}
			}
			if len(notes) > 0 {
				return v, flash(strings.Join(notes, " · "))
			}
		}
		return v, nil

	case taskToggledMsg:
		v.busy = false
		if msg.recurred {
			v.busy = true
			return v, v.loadCmd()
		}
		for i := range v.items {
			if t := v.items[i].task(); t != nil && t.Slug == msg.slug && t.Text == msg.text {
				applyTaskToggle(t, msg.done, v.now().Format("2006-01-02"))
			}
		}
		return v, nil

	case cardArchivedMsg:
		v.busy = false
		for i := range v.items {
			if c := v.items[i].card(); c != nil && msg.card != nil && c.Card.ID == msg.card.ID {
				v.items[i].item.Done = true
				c.PrevStatus = msg.prevStatus
			}
		}
		return v, nil

	case cardRestoredMsg:
		v.busy = false
		for i := range v.items {
			if c := v.items[i].card(); c != nil && c.Card.ID == msg.cardID {
				v.items[i].item.Done = false
			}
		}
		return v, nil

	case cardSavedMsg:
		v.busy = true
		return v, v.loadCmd()

	case editSavedMsg:
		v.busy = true
		return v, tea.Batch(v.loadCmd(), flash(msg.summary))

	case taskAddedMsg:
		v.busy = true
		return v, v.loadCmd()

	case taskCancelledMsg, taskPurgedMsg, cardDeletedMsg:
		v.busy = true
		return v, v.loadCmd()

	case errMsg:
		v.busy = false
		return v, nil

	case tea.KeyMsg:
		if v.del.active {
			if v.del.handleKey(msg) {
				v.busy = true
				if t := v.delItem.task(); t != nil {
					return v, deleteTaskCmd(v.client, *t)
				}
				if c := v.delItem.card(); c != nil {
					return v, deleteCardCmd(v.client, c.Board, c.Card.ID, c.Card.Title)
				}
				return v, nil
			}
			return v, nil
		}
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
		if v.prompt.active() {
			saved, cmd := v.prompt.handleKey(msg, v.client, v.now())
			if saved != nil {
				v.busy = true
				return v, saved
			}
			return v, cmd
		}
		switch msg.String() {
		case "j", "down":
			if v.cursor < len(v.items)-1 {
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
			v.busy = true
			return v, v.loadCmd()
		case "x":
			v.showDone = !v.showDone
			v.busy = true
			return v, v.loadCmd()
		case " ", "d":
			return v.toggleCurrent()
		case "enter":
			it := v.current()
			if it == nil {
				return v, nil
			}
			if c := it.card(); c != nil {
				return v, pushView(newCardView(v.client, c.Board, *c.Card))
			}
			if it.task() == nil && it.item.URL != "" {
				return v, openURLCmd(it.item.URL)
			}
		case "e":
			it := v.current()
			if it == nil {
				return v, nil
			}
			if t := it.task(); t != nil {
				return v, v.prompt.start(*t)
			}
			if c := it.card(); c != nil {
				return v, pushView(newCardViewEditing(v.client, c.Board, *c.Card))
			}
			return v, v.foreignItemFlash(it)
		case "D":
			it := v.current()
			if it == nil {
				return v, nil
			}
			if t := it.task(); t != nil {
				v.delItem = *it
				if t.Status == tasks.StatusCancelled {
					v.del.open(fmt.Sprintf("permanently delete cancelled task %q?", t.Text))
				} else {
					v.del.open(fmt.Sprintf("cancel task %q?", t.Text))
				}
				return v, nil
			}
			if c := it.card(); c != nil {
				v.delItem = *it
				v.del.open(fmt.Sprintf("permanently delete card %q (comments and attachments too)?", c.Card.Title))
				return v, nil
			}
			return v, v.foreignItemFlash(it)
		}
	}
	return v, nil
}

// foreignItemFlash explains that items from external sources are read-only
// on the Today view.
func (v *todayView) foreignItemFlash(it *todayItem) tea.Cmd {
	return flash(fmt.Sprintf("%s items are read-only here — press 5 for the %s view", it.item.Source, it.item.Source))
}

func (v *todayView) current() *todayItem {
	if v.cursor < 0 || v.cursor >= len(v.items) {
		return nil
	}
	return &v.items[v.cursor]
}

// toggleCurrent completes the selected item, or brings it back when it is
// already done: tasks re-open in place, archived cards restore to the column
// they were completed from. Foreign items are read-only here.
func (v *todayView) toggleCurrent() (view, tea.Cmd) {
	it := v.current()
	if it == nil || v.busy {
		return v, nil
	}
	if t := it.task(); t != nil {
		v.busy = true
		return v, toggleTaskCmd(v.client, *t, t.Status != tasks.StatusDone)
	}
	c := it.card()
	if c == nil {
		return v, v.foreignItemFlash(it)
	}
	v.busy = true
	client := v.client
	boardName, cardID := c.Board, c.Card.ID
	if it.isDone() {
		status := c.PrevStatus
		if status == "" || status == board.Done {
			status = board.Triage
		}
		return v, func() tea.Msg {
			if err := client.RestoreCard(context.Background(), boardName, cardID, status); err != nil {
				return fail(err)
			}
			return cardRestoredMsg{board: boardName, cardID: cardID, status: status}
		}
	}
	prev := c.Card.Status
	return v, func() tea.Msg {
		done := board.Done
		card, err := client.PatchCard(context.Background(), boardName, cardID, apiclient.CardPatch{Status: &done})
		if err != nil {
			return fail(err)
		}
		return cardArchivedMsg{board: boardName, card: card, prevStatus: prev}
	}
}

func (v *todayView) View(width, height int) string {
	if !v.loaded {
		return "\n  gathering today's list…"
	}
	listWidth := width
	showGem := width >= gemWidth()+50
	if showGem {
		listWidth = width - gemWidth() - 4
	}

	var b strings.Builder
	if len(v.items) == 0 {
		b.WriteString("\n  nothing due — clear skies ✨\n")
	}
	if v.cursor < v.offset {
		v.offset = v.cursor
	}
	reserved := 0
	if v.find.bar() != "" {
		reserved++
	}
	if v.prompt.active() {
		reserved++
	}
	if v.del.active {
		reserved++
	}
	rowsAvail := max(3, height-3-reserved)
	if v.cursor >= v.offset+rowsAvail {
		v.offset = v.cursor - rowsAvail + 1
	}
	lastSection := ""
	rendered := 0
	for i := v.offset; i < len(v.items) && rendered < rowsAvail; i++ {
		it := v.items[i]
		if it.section != lastSection {
			b.WriteString(groupHeaderStyle.Render(" "+it.section) + "\n")
			lastSection = it.section
			rendered++
		}
		b.WriteString(v.renderItem(it, i == v.cursor, listWidth) + "\n")
		rendered++
	}
	if bar := v.find.bar(); bar != "" {
		b.WriteString(" " + bar + "\n")
	}
	if v.prompt.active() {
		b.WriteString(" " + v.prompt.view() + "\n")
	}
	if v.del.active {
		b.WriteString(v.del.bar() + "\n")
	}
	list := b.String()
	if !showGem {
		return list
	}
	gem := renderGem(v.phase)
	if v.fxActive() {
		gem = v.fxFrames[v.fxIdx]
	}
	return lipgloss.JoinHorizontal(lipgloss.Top,
		lipgloss.NewStyle().Width(listWidth).Render(list),
		"  ",
		gem,
	)
}

// kindBadgeStyles maps item kinds to their badge style; unknown kinds share
// the issue style so a future source needs no TUI change to render.
func kindBadge(kind string) string {
	switch kind {
	case "task":
		return dimStyle.Render("[task]")
	case "card":
		return dueStyle.Render("[card]")
	default:
		return linkStyle.Render("[" + kind + "]")
	}
}

func (v *todayView) renderItem(it todayItem, selected bool, width int) string {
	prefix := "  "
	if selected {
		prefix = cursorStyle.Render("▸ ")
	}
	badge := kindBadge(it.item.Kind)
	if it.item.Source != "amythest" {
		badge = linkStyle.Render("[" + it.item.Source + "]")
	}
	text := highlight(it.text(), v.find.query)
	if selected {
		text = cursorStyle.Render(it.text())
	}
	if it.isDone() {
		text = doneStyle.Render(it.text() + " ✓")
	}
	var meta []string
	if it.due() != "" {
		meta = append(meta, dueStyle.Render(it.due()))
	}
	for _, b := range it.item.Badges {
		meta = append(meta, blockedStyle.Render("["+b+"]"))
	}
	if it.item.Meta != "" {
		meta = append(meta, dimStyle.Render(it.item.Meta))
	}
	return prefix + badge + " " + text + "  " + strings.Join(meta, " ")
}
