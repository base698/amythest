package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/base698/amythest/internal/apiclient"
	"github.com/base698/amythest/internal/tasks"
)

var taskPresets = []string{
	"not done;sort by due",
	"not done;due before tomorrow;sort by due",
	"done;limit 50",
}

// taskRow flattens groups for cursor navigation: header rows interleaved
// with task rows.
type taskRow struct {
	header string
	task   *tasks.Task
}

type tasksView struct {
	marked map[string]bool
	client *apiclient.Client
	rows   []taskRow
	cursor int
	offset int
	preset int
	busy   bool
	loaded bool
	find   finder
	prompt duePrompt
	del    confirm
	target tasks.Task // task the delete prompt refers to
	hide   confirm    // "stop collecting tasks from this file"
	hideAt tasks.Task // task whose file the hide prompt refers to
	now    func() time.Time
}

func newTasksView(client *apiclient.Client) *tasksView {
	return &tasksView{client: client, marked: map[string]bool{}, find: newFinder(), prompt: newDuePrompt(), now: time.Now}
}

func (v *tasksView) Title() string {
	n := 0
	for _, row := range v.rows {
		if row.task != nil && v.marked[taskMarkKey(*row.task)] {
			n++
		}
	}
	return fmt.Sprintf("tasks (%s) · %d marked", taskPresets[v.preset], n)
}
func (v *tasksView) Busy() bool { return v.busy }
func (v *tasksView) Capturing() bool {
	return v.find.active() || v.prompt.active() || v.del.active || v.hide.active
}

func (v *tasksView) Init() tea.Cmd {
	v.busy = true
	return v.loadCmd()
}

func (v *tasksView) loadCmd() tea.Cmd {
	query := taskPresets[v.preset]
	client := v.client
	return func() tea.Msg {
		groups, err := client.ListTasks(context.Background(), query)
		if err != nil {
			return fail(err)
		}
		return tasksLoadedMsg{groups}
	}
}

// toggleTaskCmd toggles a vault task. On a stale-version conflict it re-finds
// the task by (slug, text) — text is metadata-stripped and stable — and
// retries exactly once. The retry query is status-agnostic so that reopening
// a completed task works even when the view's own query filters to "not done".
func toggleTaskCmd(client *apiclient.Client, t tasks.Task, done bool) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		recurred, err := client.ToggleTask(ctx, t.Slug, t.Line, t.Version, done)
		if errors.Is(err, apiclient.ErrConflict) {
			groups, qerr := client.ListTasks(ctx, "description includes "+t.Text)
			if qerr != nil {
				return fail(qerr)
			}
			// Completing wants an open line; reopening wants a done one.
			wantStatus := tasks.StatusOpen
			if !done {
				wantStatus = tasks.StatusDone
			}
			fresh, ok := findTask(groups, t.Slug, t.Text, wantStatus)
			if !ok {
				return fail(fmt.Errorf("task changed on server; refresh (r) and retry"))
			}
			recurred, err = client.ToggleTask(ctx, fresh.Slug, fresh.Line, fresh.Version, done)
		}
		if err != nil {
			return fail(err)
		}
		msg := taskToggledMsg{slug: t.Slug, text: t.Text, done: done, recurred: recurred}
		if recurred {
			// Surface where the spawned occurrence went — it is usually
			// due tomorrow, i.e. no longer on today's screen.
			if groups, err := client.ListTasks(ctx, "not done;description includes "+t.Text); err == nil {
				if fresh, ok := findTask(groups, t.Slug, t.Text, tasks.StatusOpen); ok {
					msg.nextDue = fresh.Due
				}
			}
		}
		return msg
	}
}

// applyTaskToggle flips a task's status in place so the row stays visible
// (struck through) instead of vanishing on an immediate reload — that both
// shows the change happened and leaves space-to-undo available. The stale
// version it leaves behind self-heals through the 409 retry above.
func applyTaskToggle(t *tasks.Task, done bool, today string) {
	if done {
		t.Status = tasks.StatusDone
		t.DoneDate = today
	} else {
		t.Status = tasks.StatusOpen
		t.DoneDate = ""
	}
}

// findTask re-locates a task by identity plus status. Status matters:
// recurring tasks leave many identically-worded completed copies in the same
// file, so a status-blind match can hand back a done line and a "successful"
// retry silently no-ops against it.
func findTask(groups []apiclient.TaskGroup, slug, text, status string) (tasks.Task, bool) {
	for _, g := range groups {
		for _, t := range g.Tasks {
			if t.Slug == slug && t.Text == text && t.Status == status {
				return t, true
			}
		}
	}
	return tasks.Task{}, false
}

func (v *tasksView) Update(msg tea.Msg) (view, tea.Cmd) {
	switch msg := msg.(type) {
	case taskNoteOpened:
		note := newNoteView(v.client, msg.note)
		matches := []int{}
		for i, t := range note.tasks {
			if t.Text == msg.target.Text {
				matches = append(matches, i)
			}
		}
		if len(matches) == 1 {
			note.offset = max(0, note.tasks[matches[0]].Line-3)
		} else {
			for _, i := range matches {
				if note.tasks[i].Line == msg.target.Line {
					note.offset = max(0, msg.target.Line-3)
					break
				}
			}
		}
		return v, pushView(note)
	case openNoteMsg:
		return v, pushView(newNoteView(v.client, msg.note))
	case taskReviewDone:
		v.marked = map[string]bool{}
		v.busy = true
		return v, v.loadCmd()

	case tasksLoadedMsg:
		v.busy = false
		v.loaded = true
		v.rows = flattenGroups(msg.groups)
		if v.cursor >= len(v.rows) {
			v.cursor = max(0, len(v.rows)-1)
		}
		v.snapToTask(1)
		return v, nil

	case taskToggledMsg:
		v.busy = false
		if msg.recurred {
			// The recurrence rewrote line numbers; a reload is the only
			// safe way to keep them addressable.
			v.busy = true
			return v, v.loadCmd()
		}
		for _, row := range v.rows {
			if row.task != nil && row.task.Slug == msg.slug && row.task.Text == msg.text {
				applyTaskToggle(row.task, msg.done, v.now().Format("2006-01-02"))
			}
		}
		return v, nil

	case errMsg:
		v.busy = false
		return v, nil

	case editSavedMsg:
		v.busy = true
		return v, tea.Batch(v.loadCmd(), flash(msg.summary))

	case taskAddedMsg:
		v.busy = true
		return v, v.loadCmd()

	case fileHiddenMsg:
		v.busy = true
		return v, v.loadCmd()

	case taskCancelledMsg:
		v.busy = false
		for _, row := range v.rows {
			if row.task != nil && row.task.Slug == msg.slug && row.task.Text == msg.text {
				row.task.Status = tasks.StatusCancelled
			}
		}
		return v, nil

	case taskPurgedMsg:
		v.busy = true
		return v, v.loadCmd()

	case tea.KeyMsg:
		if v.del.active {
			if v.del.handleKey(msg) {
				v.busy = true
				return v, deleteTaskCmd(v.client, v.target)
			}
			return v, nil
		}
		if v.hide.active {
			if v.hide.handleKey(msg) {
				v.busy = true
				return v, hideFileCmd(v.client, v.hideAt)
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
		case "m":
			if t := v.current(); t != nil && !strings.HasPrefix(t.Path, "kanban/") {
				key := taskMarkKey(*t)
				v.marked[key] = !v.marked[key]
			}
		case "M":
			all := true
			for _, row := range v.rows {
				if row.task != nil && !strings.HasPrefix(row.task.Path, "kanban/") && !v.marked[taskMarkKey(*row.task)] {
					all = false
				}
			}
			for _, row := range v.rows {
				if row.task != nil && !strings.HasPrefix(row.task.Path, "kanban/") {
					v.marked[taskMarkKey(*row.task)] = !all
				}
			}
		case "a":
			var selected []tasks.Task
			for _, row := range v.rows {
				if row.task != nil && v.marked[taskMarkKey(*row.task)] {
					selected = append(selected, *row.task)
				}
			}
			if len(selected) == 0 {
				return v, flash("m marks a task · M marks all visible tasks · a reviews marked tasks")
			}
			return v, pushView(newTaskReviewView(v.client, selected))
		case "enter":
			if t := v.current(); t != nil {
				target := *t
				client := v.client
				return v, func() tea.Msg {
					note, err := client.GetNote(context.Background(), target.Slug)
					if err != nil {
						return fail(err)
					}
					return taskNoteOpened{note, target}
				}
			}
		case "j", "down":
			v.move(1)
		case "k", "up":
			v.move(-1)
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
		case "e":
			if t := v.current(); t != nil {
				if strings.HasPrefix(t.Path, "kanban/") {
					return v, flash("card tasks are edited from the board view (3)")
				}
				return v, v.prompt.start(*t)
			}
		case "D":
			t := v.current()
			if t == nil {
				return v, nil
			}
			if strings.HasPrefix(t.Path, "kanban/") {
				return v, flash("card tasks are managed from the board view (3)")
			}
			v.target = *t
			if t.Status == tasks.StatusCancelled {
				v.del.open(fmt.Sprintf("permanently delete cancelled task %q?", t.Text))
			} else {
				v.del.open(fmt.Sprintf("cancel task %q? (D again on it deletes permanently)", t.Text))
			}
			return v, nil
		case "H":
			t := v.current()
			if t == nil {
				return v, nil
			}
			if strings.HasPrefix(t.Path, "kanban/") {
				return v, flash("board cards are managed from the board view (3)")
			}
			v.hideAt = *t
			v.hide.open(fmt.Sprintf("stop collecting tasks from %q? (adds tasks: false; the note stays readable)", t.Path))
			return v, nil
		case "p":
			v.preset = (v.preset + 1) % len(taskPresets)
			v.cursor, v.offset = 0, 0
			v.busy = true
			return v, v.loadCmd()
		case " ":
			if v.busy {
				return v, nil
			}
			t := v.current()
			if t == nil {
				return v, nil
			}
			if strings.HasPrefix(t.Path, "kanban/") {
				return v, flash("card checkboxes are toggled from the board view (2)")
			}
			switch t.Status {
			case tasks.StatusOpen:
				v.busy = true
				return v, toggleTaskCmd(v.client, *t, true)
			case tasks.StatusDone:
				v.busy = true
				return v, toggleTaskCmd(v.client, *t, false)
			default:
				return v, flash("only open/done tasks can be toggled")
			}
		}
	}
	return v, nil
}

func (v *tasksView) searchTexts() []string {
	texts := make([]string, len(v.rows))
	for i, row := range v.rows {
		if row.task != nil {
			texts[i] = row.task.Text + " " + row.task.Path
		}
	}
	return texts
}

func (v *tasksView) current() *tasks.Task {
	if v.cursor < 0 || v.cursor >= len(v.rows) {
		return nil
	}
	return v.rows[v.cursor].task
}

// move advances the cursor, skipping group-header rows.
func (v *tasksView) move(delta int) {
	next := v.cursor + delta
	for next >= 0 && next < len(v.rows) && v.rows[next].task == nil {
		next += delta
	}
	if next >= 0 && next < len(v.rows) {
		v.cursor = next
	}
}

// snapToTask ensures the cursor sits on a task row after a load.
func (v *tasksView) snapToTask(delta int) {
	if v.current() != nil {
		return
	}
	v.move(delta)
	if v.current() == nil {
		v.move(-delta)
	}
}

func flattenGroups(groups []apiclient.TaskGroup) []taskRow {
	var rows []taskRow
	for gi := range groups {
		if groups[gi].Name != "" {
			rows = append(rows, taskRow{header: groups[gi].Name})
		}
		for ti := range groups[gi].Tasks {
			rows = append(rows, taskRow{task: &groups[gi].Tasks[ti]})
		}
	}
	return rows
}

func (v *tasksView) View(width, height int) string {
	if !v.loaded {
		return "\n  loading tasks…"
	}
	if len(v.rows) == 0 {
		return "\n  no tasks match: " + taskPresets[v.preset]
	}
	// Reserve rows for the bars appended below the list, or a full screen
	// of tasks pushes them past the height budget and they get trimmed.
	reserved := 0
	if v.find.bar() != "" {
		reserved++
	}
	if v.prompt.active() {
		reserved++
	}
	if v.del.active || v.hide.active {
		reserved++
	}
	avail := max(3, height-2-reserved)
	if v.cursor < v.offset {
		v.offset = v.cursor
	}
	rowLines := func(i int) []string {
		row := v.rows[i]
		if row.task == nil {
			return []string{groupHeaderStyle.Render(" " + row.header)}
		}
		prefix := "  "
		if v.marked[taskMarkKey(*row.task)] {
			prefix = "◆ "
		}
		lines := []string{prefix + renderTaskLine(row.task, i == v.cursor, max(10, width-2))}
		var context []string
		for _, text := range row.task.Context {
			context = append(context, wrapLine(text, max(15, width-8))...)
		}
		for _, text := range context[:min(len(context), 4)] {
			lines = append(lines, dimStyle.Render("      "+text))
		}
		if len(context) > 4 {
			lines = append(lines, dimStyle.Render("      … Enter opens all context"))
		}
		return lines
	}
	for v.offset < v.cursor {
		n := 0
		for i := v.offset; i <= v.cursor; i++ {
			n += len(rowLines(i))
		}
		if n <= avail {
			break
		}
		v.offset++
	}
	var b strings.Builder
	used := 0
	for i := v.offset; i < len(v.rows) && used < avail; i++ {
		lines := rowLines(i)
		for _, line := range lines {
			if used >= avail {
				break
			}
			b.WriteString(line + "\n")
			used++
		}
	}
	b.WriteString(dimStyle.Render(" m mark · M mark all · a review · Enter note · D cancel/delete") + "\n")
	if bar := v.find.bar(); bar != "" {
		b.WriteString(" " + bar + "\n")
	}
	if v.prompt.active() {
		b.WriteString(" " + v.prompt.view() + "\n")
	}
	if v.del.active {
		b.WriteString(v.del.bar() + "\n")
	}
	if v.hide.active {
		b.WriteString(v.hide.bar() + "\n")
	}
	return b.String()
}

func renderTaskLine(t *tasks.Task, selected bool, width int) string {
	marker := "[ ]"
	if t.Status == tasks.StatusDone {
		marker = "[x]"
	} else if t.Status == tasks.StatusCancelled {
		marker = "[-]"
	}
	prefix := "  "
	if selected {
		prefix = cursorStyle.Render("▸ ")
	}
	text := t.Text
	if maxText := width - 30; maxText > 10 && len(text) > maxText {
		text = text[:maxText-1] + "…"
	}
	line := marker + " " + text
	if t.Status == tasks.StatusDone || t.Status == tasks.StatusCancelled {
		line = doneStyle.Render(line)
	} else if selected {
		line = cursorStyle.Render(line)
	}
	var meta []string
	if t.Due != "" {
		meta = append(meta, dueStyle.Render("due "+t.Due))
	}
	if t.Recurrence != "" {
		meta = append(meta, dimStyle.Render("🔁 "+t.Recurrence))
	}
	if badge := taskPriorityBadge(t.Priority); badge != "" {
		meta = append(meta, badge)
	}
	meta = append(meta, dimStyle.Render(t.Path))
	return prefix + line + "  " + strings.Join(meta, " ")
}

// taskPriorityBadge maps the tasks scale (0 highest … 5 lowest, 3 = none).
func taskPriorityBadge(p int) string {
	switch p {
	case 0:
		return priorityStyles["p0"].Render("[!!]")
	case 1:
		return priorityStyles["p1"].Render("[!]")
	case 2:
		return priorityStyles["p2"].Render("[+]")
	case 4, 5:
		return dimStyle.Render("[low]")
	default:
		return ""
	}
}

func taskMarkKey(t tasks.Task) string { return fmt.Sprintf("%s:%d:%s", t.Slug, t.Line, t.Text) }

type taskNoteOpened struct {
	note   *apiclient.Note
	target tasks.Task
}
