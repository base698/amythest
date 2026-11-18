package tui

import (
	"io"
	"reflect"
	"strings"

	"github.com/base698/amythest/internal/apiclient"
	"github.com/base698/amythest/internal/find"
	"github.com/base698/amythest/internal/source"
	srcamy "github.com/base698/amythest/internal/source/amythest"
	"github.com/base698/amythest/internal/wall"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type WorkspaceOptions struct {
	Scope      apiclient.Scope
	Wall       wall.Options
	Find       find.Options
	Open       func(*find.Choice) tea.Cmd
	AllSources *source.Registry
}

// Workspace keeps normal Amythest navigation beside a live project wall.
// Each asynchronous load belongs to the panel and scope that started it.
type Workspace struct {
	allClient, scopedClient   *apiclient.Client
	o                         WorkspaceOptions
	app                       *App
	search, wall              tea.Model
	all, searching, wallFocus bool
	width, height, generation int
	wallGeneration            int
}
type workspaceMsg struct {
	panel      string
	generation int
	msg        tea.Msg
}
type WorkspaceOpened struct{ Err error }

type workspaceChoice struct{ choice *find.Choice }

func NewWorkspace(client *apiclient.Client, o WorkspaceOptions) *Workspace {
	w := &Workspace{allClient: client, scopedClient: client.Scoped(o.Scope), o: o}
	w.resetApp()
	o.Wall.Project = o.Scope.Project
	o.Wall.CanvasWorkspace = o.Scope.Name
	// Kitty's Unicode placeholders are ordinary cells: box clips them with
	// their panel, including when the terminal switches to a single column.
	w.o.Wall = o.Wall
	w.wall = wall.New(client, o.Wall)
	return w
}
func (w *Workspace) client() *apiclient.Client {
	if w.all {
		return w.allClient
	}
	return w.scopedClient
}
func (w *Workspace) resetApp() {
	c := w.client()
	w.app = NewApp(c, source.NewRegistry(srcamy.New(c)))
	if w.all && w.o.AllSources != nil {
		w.app = NewApp(c, w.o.AllSources)
	}
}
func (w *Workspace) Init() tea.Cmd {
	return tea.Batch(w.command("app", w.app.Init()), w.command("wall", w.wall.Init()))
}
func (w *Workspace) command(panel string, cmd tea.Cmd) tea.Cmd {
	generation := w.generation
	if panel == "wall" {
		generation = w.wallGeneration
	}
	return workspaceCommand(panel, generation, cmd)
}
func workspaceCommand(panel string, generation int, cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		msg := cmd()
		if batch, ok := msg.(tea.BatchMsg); ok {
			out := make(tea.BatchMsg, len(batch))
			for i, c := range batch {
				out[i] = workspaceCommand(panel, generation, c)
			}
			return out
		}
		// Bubble Tea owns process execution, sequences and terminal control messages.
		// Keep those intact so editors, clipboard and exit continue to work.
		if t := reflect.TypeOf(msg); t != nil && t.PkgPath() == "github.com/charmbracelet/bubbletea" {
			return msg
		}
		return workspaceMsg{panel, generation, msg}
	}
}
func (w *Workspace) sizes() (int, int, int) {
	h := max(1, w.height-2)
	if w.width < 105 {
		return max(1, w.width), max(1, w.width), h
	}
	right := max(38, w.width*38/100)
	return max(1, w.width-right-1), right, h
}
func (w *Workspace) resize() tea.Cmd {
	left, right, h := w.sizes()
	_, a := w.app.Update(tea.WindowSizeMsg{Width: left, Height: h})
	var s tea.Cmd
	if w.search != nil {
		w.search, s = w.search.Update(tea.WindowSizeMsg{Width: left, Height: h})
	}
	var b tea.Cmd
	w.wall, b = w.wall.Update(tea.WindowSizeMsg{Width: right, Height: h})
	return tea.Batch(w.command("app", a), w.command("search", s), w.command("wall", b))
}
func (w *Workspace) navigate(key string) tea.Cmd {
	w.wallFocus = false
	if key == "7" {
		w.searching = true
		o := w.o.Find
		o.Finish = func(c *find.Choice) tea.Cmd { return func() tea.Msg { return workspaceChoice{c} } }
		if w.search == nil {
			w.search = find.New(w.client(), o)
			return tea.Batch(w.resize(), w.command("search", w.search.Init()))
		}
		return w.resize()
	}
	w.searching = false
	return tea.Batch(w.command("app", w.app.Navigate(key)), w.resize())
}
func (w *Workspace) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case WorkspaceOpened:
		if m.Err != nil {
			return w, w.deliver("app", errMsg{m.Err})
		}
		return w, nil
	case tea.WindowSizeMsg:
		w.width, w.height = m.Width, m.Height
		return w, w.resize()
	case tea.MouseMsg:
		if m.Y < 1 || m.Y >= w.height-1 {
			return w, nil
		}
		left, _, _ := w.sizes()
		inWall := w.wallFocus
		if w.width >= 105 {
			if m.X == left {
				return w, nil
			}
			inWall = m.X > left
			if inWall {
				m.X -= left + 1
			}
		}
		m.Y-- // the dashboard header is outside both child panels
		if m.Button == tea.MouseButtonLeft && m.Action == tea.MouseActionPress {
			w.wallFocus = inWall
		}
		if inWall {
			return w, w.deliver("wall", m)
		}
		if w.searching {
			return w, w.deliver("search", m)
		}
		return w, w.deliver("app", m)
	case workspaceMsg:
		if (m.panel == "wall" && m.generation != w.wallGeneration) || (m.panel != "wall" && m.generation != w.generation) {
			return w, nil
		}
		if choice, ok := m.msg.(workspaceChoice); ok {
			w.searching = false
			if choice.choice == nil {
				return w, nil
			}
			if v := choice.choice.View; v != nil {
				o := w.o.Wall
				o.Project = v.Project
				if !strings.EqualFold(v.Project, w.o.Scope.Project) {
					o.CanvasWorkspace = ""
				}
				o.Show = &wall.Want{Kind: v.Kind, Title: v.Title, Ref: v.Ref}
				w.Close()
				w.wall = wall.New(w.allClient, o)
				w.wallGeneration++
				w.wallFocus = true
				return w, tea.Batch(w.resize(), w.command("wall", w.wall.Init()))
			}
			if w.o.Open != nil {
				return w, w.o.Open(choice.choice)
			}
			return w, nil
		}
		return w, w.deliver(m.panel, m.msg)
	case tea.KeyMsg:
		key := m.String()
		if key == "ctrl+c" {
			return w, tea.Quit
		}
		if key == "f8" || key == "ctrl+w" || ((key == "tab" || key == "shift+tab") && w.tabSwitchesPanels()) {
			w.wallFocus = !w.wallFocus
			return w, nil
		}
		if key == "f9" {
			// Avoid discarding a typed edit; Escape first returns to navigation.
			if !w.searching && w.app.top().Capturing() {
				if _, ok := w.app.top().(*notesView); !ok {
					return w, nil
				}
			}
			w.all = !w.all
			w.generation++
			w.searching = false
			w.search = nil
			w.resetApp()
			return w, tea.Batch(w.command("app", w.app.Init()), w.resize())
		}
		if key == "f1" || key == "f2" || key == "f3" || key == "f4" || key == "f7" {
			if !w.searching && w.app.top().Capturing() {
				// Search fields may be left without saving; editing prompts may not.
				if _, ok := w.app.top().(*notesView); !ok {
					return w, nil
				}
			}
			return w, w.navigate(strings.TrimPrefix(key, "f"))
		}
		if !w.wallFocus && !w.searching && !w.app.top().Capturing() && (key == "1" || key == "2" || key == "3" || key == "4" || key == "7") {
			return w, w.navigate(key)
		}
		if w.wallFocus {
			return w, w.deliver("wall", m)
		}
		if w.searching {
			return w, w.deliver("search", m)
		}
		return w, w.deliver("app", m)
	}
	// Results from Bubble Tea-managed editor/process callbacks go to the main UI.
	return w, w.deliver("app", msg)
}

// Notes and search already use Tab for their own navigation. Keep that key
// there, and keep every editing prompt intact; Ctrl+W/F8 always switch panels.
func (w *Workspace) tabSwitchesPanels() bool {
	if w.wallFocus {
		return true
	}
	if w.searching || w.app.top().Capturing() {
		return false
	}
	switch w.app.top().(type) {
	case *notesView, *noteView:
		return false
	}
	return true
}
func (w *Workspace) deliver(panel string, msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	switch panel {
	case "wall":
		w.wall, cmd = w.wall.Update(msg)
	case "search":
		if w.search != nil {
			w.search, cmd = w.search.Update(msg)
		}
	default:
		_, cmd = w.app.Update(msg)
	}
	return w.command(panel, cmd)
}

// Close releases terminal images on every exit, including quitting from the
// navigation panel, and before replacing the wall with a search result.
func (w *Workspace) Close() error {
	if closer, ok := w.wall.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

func box(frame string, width, height int) string {
	lines := strings.Split(frame, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	for i, line := range lines {
		line = ansi.Truncate(line, width, "")
		lines[i] = line + strings.Repeat(" ", max(0, width-lipgloss.Width(line)))
	}
	return strings.Join(lines, "\n")
}
func (w *Workspace) View() string {
	if w.width < 1 || w.height < 3 {
		return "Opening Amythest…"
	}
	label := w.o.Scope.Name
	if w.all {
		label = "All workspaces"
	}
	active := "navigation"
	if w.wallFocus {
		active = "wall"
	}
	if w.searching && !w.wallFocus {
		active = "search"
	}
	header := headerStyle.Width(w.width).Render(ansi.Truncate(" amy · "+label+" · keyboard: "+active, w.width, "…"))
	left, right, h := w.sizes()
	frame := w.app.view(true)
	if w.searching && w.search != nil {
		frame = w.search.View()
	}
	body := box(frame, left, h)
	if w.width >= 105 {
		divider := strings.TrimSuffix(strings.Repeat("│\n", h), "\n")
		body = lipgloss.JoinHorizontal(lipgloss.Top, body, dimStyle.Render(divider), box(w.wall.View(), right, h))
	} else if w.wallFocus {
		body = box(w.wall.View(), right, h)
	}
	hint := " Ctrl+W/F8 switch panel · F1 Focus F2 Tasks F3 Boards F4 Notes F7 Search F9 Scope"
	if w.wallFocus {
		hint = " Ctrl+W/Tab return · ↑↓ move · Enter view · c canvases · o browser · F9 Scope"
	} else if w.tabSwitchesPanels() {
		hint = " Tab/Ctrl+W wall · F1 Focus F2 Tasks F3 Boards F4 Notes F7 Search F9 Scope"
	}
	footer := statusStyle.Width(w.width).Render(ansi.Truncate(hint, w.width, "…"))
	return header + "\n" + body + "\n" + footer
}
