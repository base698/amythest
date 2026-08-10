package tui

import (
	"context"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/base698/amythest/internal/apiclient"
	"github.com/base698/amythest/internal/kanban/board"
	"github.com/base698/amythest/internal/tasks"
)

// todayItem is one row of the focus view: either a vault task or a kanban
// card, normalized so toggling/searching/rendering can treat them alike.
type todayItem struct {
	section string // "Focus" | "Overdue" | "Due today" | "Done today"
	task    *tasks.Task
	card    *board.Card
	board   string // owning board for cards
	// done marks a card completed (archived); prevStatus is where space
	// restores it to. Task doneness lives on task.Status.
	done       bool
	prevStatus board.Status
}

func (it todayItem) isDone() bool {
	if it.task != nil {
		return it.task.Status == tasks.StatusDone
	}
	return it.done
}

func (it todayItem) text() string {
	if it.task != nil {
		return it.task.Text
	}
	return it.card.Title
}

func (it todayItem) due() string {
	if it.task != nil {
		return it.task.Due
	}
	return it.card.DueDate
}

type todayLoadedMsg struct{ items []todayItem }

type todayView struct {
	client   *apiclient.Client
	items    []todayItem
	cursor   int
	offset   int
	busy     bool
	loaded   bool
	showDone bool // "x": include the Done-today section
	phase    int  // shimmer phase, advanced by the root App
	find     finder
	prompt   duePrompt
	now      func() time.Time
}

func newTodayView(client *apiclient.Client) *todayView {
	return &todayView{client: client, find: newFinder(), prompt: newDuePrompt(), now: time.Now}
}

func (v *todayView) Title() string { return "today" }
func (v *todayView) Busy() bool    { return v.busy }
func (v *todayView) Capturing() bool {
	return v.find.active() || v.prompt.active()
}

func (v *todayView) Init() tea.Cmd {
	v.busy = true
	return v.loadCmd()
}

// loadCmd assembles the focus list: overdue/today vault tasks plus kanban
// cards that are due (or explicitly focused via the board's focus card).
// With showDone it also gathers what was finished today, so a completion can
// always be seen — and undone — after the fact.
func (v *todayView) loadCmd() tea.Cmd {
	client := v.client
	today := v.now().Format("2006-01-02")
	showDone := v.showDone
	return func() tea.Msg {
		ctx := context.Background()
		groups, err := client.ListTasks(ctx, "not done;due before tomorrow;sort by due")
		if err != nil {
			return fail(err)
		}
		var items []todayItem
		for gi := range groups {
			for ti := range groups[gi].Tasks {
				t := &groups[gi].Tasks[ti]
				if strings.HasPrefix(t.Path, "kanban/") {
					continue // card checkboxes surface through their card
				}
				section := "Due today"
				if t.Due < today {
					section = "Overdue"
				}
				items = append(items, todayItem{section: section, task: t})
			}
		}
		if showDone {
			doneGroups, err := client.ListTasks(ctx, "done;done on today")
			if err != nil {
				return fail(err)
			}
			for gi := range doneGroups {
				for ti := range doneGroups[gi].Tasks {
					t := &doneGroups[gi].Tasks[ti]
					if strings.HasPrefix(t.Path, "kanban/") {
						continue
					}
					items = append(items, todayItem{section: "Done today", task: t})
				}
			}
		}
		boards, err := client.ListBoards(ctx)
		if err != nil {
			return fail(err)
		}
		for _, bs := range boards {
			if bs.Archived {
				continue
			}
			b, err := client.GetBoard(ctx, bs.Name)
			if err != nil {
				return fail(err)
			}
			for i := range b.Cards {
				card := b.Cards[i]
				switch {
				case card.ID == b.FocusCardID:
					items = append(items, todayItem{section: "Focus", card: &b.Cards[i], board: b.Name})
				case card.DueDate != "" && card.DueDate <= today:
					section := "Due today"
					if card.DueDate < today {
						section = "Overdue"
					}
					items = append(items, todayItem{section: section, card: &b.Cards[i], board: b.Name})
				}
			}
			if showDone {
				archived, err := client.ListArchive(ctx, bs.Name, 50)
				if err != nil {
					return fail(err)
				}
				for i := range archived {
					card := archived[i]
					if card.DoneAt == nil || card.DoneAt.Local().Format("2006-01-02") != today {
						continue
					}
					items = append(items, todayItem{
						section: "Done today", card: &archived[i], board: bs.Name,
						done: true, prevStatus: board.Triage,
					})
				}
			}
		}
		sortTodayItems(items)
		return todayLoadedMsg{items}
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
	case todayLoadedMsg:
		v.busy = false
		v.loaded = true
		v.items = msg.items
		if v.cursor >= len(v.items) {
			v.cursor = max(0, len(v.items)-1)
		}
		return v, nil

	case taskToggledMsg:
		v.busy = false
		if msg.recurred {
			v.busy = true
			return v, v.loadCmd()
		}
		for i := range v.items {
			if t := v.items[i].task; t != nil && t.Slug == msg.slug && t.Text == msg.text {
				applyTaskToggle(t, msg.done, v.now().Format("2006-01-02"))
			}
		}
		return v, nil

	case cardArchivedMsg:
		v.busy = false
		for i := range v.items {
			if c := v.items[i].card; c != nil && c.ID != "" && msg.card != nil && c.ID == msg.card.ID {
				v.items[i].done = true
				v.items[i].prevStatus = msg.prevStatus
			}
		}
		return v, nil

	case cardRestoredMsg:
		v.busy = false
		for i := range v.items {
			if c := v.items[i].card; c != nil && c.ID == msg.cardID {
				v.items[i].done = false
			}
		}
		return v, nil

	case cardSavedMsg:
		v.busy = true
		return v, v.loadCmd()

	case dueSavedMsg:
		v.busy = true
		return v, tea.Batch(v.loadCmd(), flash("due date saved"))

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
			if it := v.current(); it != nil && it.card != nil {
				return v, pushView(newCardView(v.client, it.board, *it.card))
			}
		case "e":
			it := v.current()
			if it == nil {
				return v, nil
			}
			if it.task != nil {
				return v, v.prompt.start(*it.task)
			}
			return v, pushView(newCardViewEditing(v.client, it.board, *it.card))
		}
	}
	return v, nil
}

func (v *todayView) current() *todayItem {
	if v.cursor < 0 || v.cursor >= len(v.items) {
		return nil
	}
	return &v.items[v.cursor]
}

// toggleCurrent completes the selected item, or brings it back when it is
// already done: tasks re-open in place, archived cards restore to the column
// they were completed from.
func (v *todayView) toggleCurrent() (view, tea.Cmd) {
	it := v.current()
	if it == nil || v.busy {
		return v, nil
	}
	v.busy = true
	client := v.client
	if it.task != nil {
		return v, toggleTaskCmd(client, *it.task, it.task.Status != tasks.StatusDone)
	}
	boardName, cardID := it.board, it.card.ID
	if it.isDone() {
		status := it.prevStatus
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
	prev := it.card.Status
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
	rowsAvail := height - 3
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
	list := b.String()
	if !showGem {
		return list
	}
	gem := renderGem(v.phase)
	return lipgloss.JoinHorizontal(lipgloss.Top,
		lipgloss.NewStyle().Width(listWidth).Render(list),
		"  ",
		gem,
	)
}

func (v *todayView) renderItem(it todayItem, selected bool, width int) string {
	prefix := "  "
	if selected {
		prefix = cursorStyle.Render("▸ ")
	}
	kind := dimStyle.Render("[task]")
	if it.card != nil {
		kind = dueStyle.Render("[card]")
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
	if it.task != nil {
		meta = append(meta, dimStyle.Render(it.task.Path))
	} else {
		meta = append(meta, dimStyle.Render(it.board))
		if it.card.Blocked {
			meta = append(meta, blockedStyle.Render("[blocked]"))
		}
	}
	return prefix + kind + " " + text + "  " + strings.Join(meta, " ")
}
