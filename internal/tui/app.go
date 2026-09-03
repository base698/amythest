// Package tui is the amy terminal UI: a view stack over the apiclient.
// Views are pure bubbletea-style models; all I/O happens in tea.Cmd
// goroutines that resolve to the typed messages below.
package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/base698/amythest/internal/apiclient"
	"github.com/base698/amythest/internal/kanban/board"
	"github.com/base698/amythest/internal/source"
	"github.com/base698/amythest/internal/source/azboards"
)

// view is one screen in the navigation stack. Update returns the replacement
// view (usually itself) plus any command; View renders into the given box.
type view interface {
	Init() tea.Cmd
	Update(msg tea.Msg) (view, tea.Cmd)
	View(width, height int) string
	Title() string
	Busy() bool
	// Capturing reports that the view has a text input open (search or a
	// prompt) and must receive every key, including ones the root App
	// would otherwise treat as global.
	Capturing() bool
}

// Navigation and data messages. Non-key messages are delivered to every view
// in the stack so a parent (e.g. the board view) can refresh itself when a
// child's mutation lands.
type (
	pushMsg  struct{ v view }
	popMsg   struct{}
	errMsg   struct{ err error }
	flashMsg struct{ text string }

	tasksLoadedMsg struct{ groups []apiclient.TaskGroup }
	taskToggledMsg struct {
		slug     string
		text     string
		done     bool
		recurred bool
		nextDue  string // due date of the spawned occurrence, when recurred
	}
	boardsLoadedMsg  struct{ boards []board.BoardSummary }
	boardLoadedMsg   struct{ b *board.Board }
	archiveLoadedMsg struct {
		board string
		cards []board.Card
	}
	cardSavedMsg struct{ card *board.Card }
	// cardArchivedMsg / cardRestoredMsg are completion state changes, kept
	// distinct from cardSavedMsg (description edits) so list views can mark
	// items in place and the root can announce them.
	cardArchivedMsg struct {
		board      string
		card       *board.Card
		prevStatus board.Status
	}
	cardRestoredMsg struct {
		board  string
		cardID string
		status board.Status
	}
)

func pushView(v view) tea.Cmd  { return func() tea.Msg { return pushMsg{v} } }
func popView() tea.Cmd         { return func() tea.Msg { return popMsg{} } }
func fail(err error) tea.Msg   { return errMsg{err} }
func flash(text string) tea.Cmd {
	return func() tea.Msg { return flashMsg{text} }
}

type App struct {
	client *apiclient.Client
	reg    *source.Registry
	stack  []view
	width  int
	height int
	spin   spinner.Model
	status string
	help   bool
}

func NewApp(client *apiclient.Client, reg *source.Registry) *App {
	sp := spinner.New()
	sp.Spinner = spinner.MiniDot
	return &App{
		client: client,
		reg:    reg,
		stack:  []view{newTodayView(client, reg)},
		spin:   sp,
	}
}

// shimmerMsg drives the gem animation; the root re-arms the tick only while
// the today view is on top so other screens stay idle. The ttfx intro runs
// at a faster cadence than the built-in shimmer.
type shimmerMsg struct{}

func shimmerTick() tea.Cmd {
	return tea.Tick(140*time.Millisecond, func(time.Time) tea.Msg { return shimmerMsg{} })
}

func fxTick() tea.Cmd {
	return tea.Tick(40*time.Millisecond, func(time.Time) tea.Msg { return shimmerMsg{} })
}

func (a *App) Init() tea.Cmd {
	return tea.Batch(a.stack[0].Init(), a.spin.Tick, shimmerTick())
}

func (a *App) top() view { return a.stack[len(a.stack)-1] }

// azSource unwraps a registered Azure Boards source so the boards screen can
// list its virtual boards; nil when cli.yaml has no sources.azboards.
func (a *App) azSource() *azboards.Source {
	if s, ok := a.reg.Get("azboards"); ok {
		if az, ok := s.(*azboards.Source); ok {
			return az
		}
	}
	return nil
}

// restartShimmer re-arms the gem animation when navigation lands back on the
// today view. Duplicate ticks from rapid nav collapse harmlessly: extras stop
// on the next non-today frame.
func (a *App) restartShimmer() tea.Cmd {
	if _, ok := a.top().(*todayView); ok {
		return shimmerTick()
	}
	return nil
}

func (a *App) busy() bool {
	for _, v := range a.stack {
		if v.Busy() {
			return true
		}
	}
	return false
}

func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
		return a, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		a.spin, cmd = a.spin.Update(msg)
		return a, cmd

	case shimmerMsg:
		if tv, ok := a.top().(*todayView); ok {
			if tv.fxActive() {
				tv.fxIdx++
				if tv.fxActive() {
					return a, fxTick()
				}
			}
			tv.phase++
			return a, shimmerTick()
		}
		return a, nil

	case pushMsg:
		a.stack = append(a.stack, msg.v)
		a.status = ""
		return a, msg.v.Init()

	case popMsg:
		if len(a.stack) > 1 {
			a.stack = a.stack[:len(a.stack)-1]
		}
		return a, a.restartShimmer()

	case errMsg:
		a.status = "error: " + msg.err.Error()
		return a, nil

	case flashMsg:
		a.status = msg.text
		return a, nil

	case tea.KeyMsg:
		if a.status != "" {
			a.status = "" // any key dismisses the last error/notice
		}
		// The help overlay swallows the next keypress to close itself.
		if a.help {
			if msg.String() == "ctrl+c" {
				return a, tea.Quit
			}
			a.help = false
			return a, nil
		}
		// A view with an open text input gets every key except ctrl+c —
		// including multi-rune messages, which are pastes there.
		if a.top().Capturing() && msg.String() != "ctrl+c" {
			next, cmd := a.top().Update(msg)
			a.stack[len(a.stack)-1] = next
			return a, cmd
		}
		// Outside text inputs, fast input (key repeat, automation) can
		// coalesce several runes into one KeyMsg ("jjj"), which would match
		// no binding; replay them as individual keypresses.
		if msg.Type == tea.KeyRunes && len(msg.Runes) > 1 {
			var cmds []tea.Cmd
			for _, r := range msg.Runes {
				_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
			return a, tea.Batch(cmds...)
		}
		switch msg.String() {
		case "ctrl+c", "q":
			return a, tea.Quit
		case "?":
			a.help = !a.help
			return a, nil
		case "esc", "backspace":
			if len(a.stack) > 1 {
				a.stack = a.stack[:len(a.stack)-1]
				return a, a.restartShimmer()
			}
			return a, nil
		case "1":
			a.stack = []view{newTodayView(a.client, a.reg)}
			return a, tea.Batch(a.top().Init(), shimmerTick())
		case "2":
			a.stack = []view{newTasksView(a.client)}
			return a, a.top().Init()
		case "3":
			a.stack = []view{newBoardsView(a.client, a.azSource())}
			return a, a.top().Init()
		case "4":
			a.stack = []view{newNotesView(a.client)}
			return a, a.top().Init()
		case "5":
			if src, ok := a.reg.Get("jira"); ok {
				a.stack = []view{newSourceView(a.client, src)}
				return a, a.top().Init()
			}
			return a, flash("no jira source configured — run: amy source init jira")
		case "6":
			a.stack = []view{newBasesView(a.client)}
			return a, a.top().Init()
		case "0":
			a.stack = []view{newSourcesView(a.reg)}
			return a, a.top().Init()
		case "T":
			if _, open := a.top().(*themePickerView); !open {
				v := newThemePickerView()
				a.stack = append(a.stack, v)
				return a, v.Init()
			}
		case "+":
			// On a board, + creates a card there; everywhere else it's
			// the quick-add task flow.
			if _, onBoard := a.top().(*boardView); !onBoard {
				v := newAddTaskView(a.client)
				a.stack = append(a.stack, v)
				return a, v.Init()
			}
		}
		next, cmd := a.top().Update(msg)
		a.stack[len(a.stack)-1] = next
		return a, cmd
	}

	// A resolved note is a navigation event for whichever view asked for
	// it; delivering it to the whole stack would push duplicates.
	if _, ok := msg.(openNoteMsg); ok {
		next, cmd := a.top().Update(msg)
		a.stack[len(a.stack)-1] = next
		return a, cmd
	}

	// Completion changes get announced in the status bar so a keystroke
	// like "d" always has visible feedback, whatever the view does.
	switch msg := msg.(type) {
	case taskToggledMsg:
		today := time.Now().Format("2006-01-02")
		switch {
		case msg.recurred && msg.nextDue != "" && msg.nextDue <= today:
			// Overdue recurrence catching up: the spawned occurrence is
			// itself already due, so the task will look "still open".
			a.status = fmt.Sprintf("completed ✓ — next occurrence (due %s) is already pending: space again to catch up, or e → repeat 'every day when done'", msg.nextDue)
		case msg.recurred && msg.nextDue != "":
			a.status = fmt.Sprintf("task completed ✓ — recurring: next occurrence due %s", msg.nextDue)
		case msg.recurred:
			a.status = "task completed ✓ — next occurrence created"
		case msg.done:
			a.status = "task completed ✓ — space toggles it back"
		default:
			a.status = "task reopened"
		}
	case cardArchivedMsg:
		a.status = "card archived ✓ — space on it in today (1) restores"
	case cardRestoredMsg:
		a.status = fmt.Sprintf("card restored to %s", msg.status)
	case cardMovedBoardMsg:
		a.status = fmt.Sprintf("card moved to %s ✓", msg.to)
	case taskAddedMsg:
		a.status = fmt.Sprintf("task added to %s ✓", msg.path)
	case cardCreatedMsg:
		a.status = fmt.Sprintf("card %q created ✓", msg.card.Title)
	case taskCancelledMsg:
		a.status = "task cancelled ❌ — recoverable in the note; D again deletes permanently"
	case taskPurgedMsg:
		a.status = "cancelled task deleted permanently"
	case cardDeletedMsg:
		a.status = fmt.Sprintf("card %q deleted permanently", msg.title)
	}

	// Data messages go to every view so parents can react to child mutations.
	var cmds []tea.Cmd
	for i, v := range a.stack {
		next, cmd := v.Update(msg)
		a.stack[i] = next
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return a, tea.Batch(cmds...)
}

func (a *App) View() string {
	if a.width == 0 {
		return "loading…"
	}
	body := a.top().View(a.width, a.height-2)
	if a.help {
		body = helpText
	}
	crumbs := make([]string, len(a.stack))
	for i, v := range a.stack {
		crumbs[i] = v.Title()
	}
	header := headerStyle.Width(a.width).Render(" amy · " + strings.Join(crumbs, " › "))

	status := a.status
	if status == "" {
		user := a.client.User()
		if user != "" {
			user = " · " + user
		}
		status = fmt.Sprintf("%s%s · ? help · q quit", a.client.Endpoint(), user)
	}
	if a.busy() {
		status = a.spin.View() + " " + status
	}
	bar := statusStyle.Width(a.width).Render(" " + status)

	lines := strings.Split(body, "\n")
	max := a.height - 2
	if len(lines) > max {
		lines = lines[:max]
	}
	for len(lines) < max {
		lines = append(lines, "")
	}
	return header + "\n" + strings.Join(lines, "\n") + "\n" + bar
}

const helpText = `
  Keys

  j/k or arrows   move cursor
  h/l             switch board column
  enter           open board / card
  space           toggle task, checkbox, or complete card
  +               add: task (daily note or searched note),
                  or a card when viewing a board
  d               mark card done (archives it)
  D               delete with confirm: task → cancel,
                  again → purge; card → permanent delete
  m               move card: picker with lanes and
                  other boards (t/b/y/i/v/d shortcuts)
  e               edit: task due date + 🔁 repeat rule
                  (two steps), or card in $EDITOR
  T               theme picker (moving previews live;
                  persist with 'theme: <name>' in cli.yaml)
  x               show/hide "Done today" (today view)
  /               search lists; on boards: fuzzy filter
                  (enter keeps it, / then esc clears)
  n / N           next / previous match
  p               cycle task query preset (tasks view)
  r               refresh current view
  1 / 2 / 3 / 4   today / tasks / boards / notes
  5 / 6 / 0       jira / bases (dataview) / sources
  boards: [azure] rows are Azure Boards virtual boards
                  (m move column · c comment · o browser ·
                  f only your work items)
  notes: tab      browse (folders, tags, recent);
                  p preview pane · s sort · / filter
                  reader: b backlinks · e edit in $EDITOR
                  (jira view: o open · c comment ·
                  p pull into a board · a to agent)
  tab / enter     cycle & follow note links (note view)
  a               send note to a herdr agent (note view)
  c               comment on a card (card view)
  esc             back
  ?               close help
  q               quit
`
