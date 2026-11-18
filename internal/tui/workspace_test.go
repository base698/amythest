package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/base698/amythest/internal/apiclient"
	"github.com/base698/amythest/internal/find"
	"github.com/base698/amythest/internal/wall"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func workspaceFixture() *Workspace {
	return NewWorkspace(apiclient.New(apiclient.Config{Endpoint: "https://example.test"}), WorkspaceOptions{Scope: apiclient.Config{}.Workspace("Garden")})
}

func TestWorkspaceNavigationAndSearchEscape(t *testing.T) {
	w := workspaceFixture()
	w.Update(tea.WindowSizeMsg{Width: 140, Height: 35})
	w.Update(tea.KeyMsg{Type: tea.KeyF2})
	w.Update(workspaceMsg{"app", w.generation, loadedTasks()})
	if !strings.Contains(w.View(), "water the ferns") {
		t.Fatal("tasks missing")
	}
	w.Update(tea.KeyMsg{Type: tea.KeyF4})
	if !w.app.top().Capturing() {
		t.Fatal("notes search should capture text")
	}
	w.Update(tea.KeyMsg{Type: tea.KeyF7})
	if !w.searching {
		t.Fatal("F7 did not leave notes input")
	}
	_, cmd := w.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("search did not close")
	}
	if _, quit := cmd().(tea.QuitMsg); quit {
		t.Fatal("embedded search quit dashboard")
	}
	w.Update(cmd())
	if w.searching {
		t.Fatal("search remained open")
	}
	w.Update(tea.KeyMsg{Type: tea.KeyF2})
	w.Update(tea.KeyMsg{Type: tea.KeyF9})
	if !w.all || !strings.Contains(w.View(), "All workspaces") {
		t.Fatal("scope toggle missing")
	}
	w.Update(workspaceMsg{"app", w.generation - 1, loadedTasks()})
	if strings.Contains(w.View(), "water the ferns") {
		t.Fatal("stale scoped load entered new view")
	}
}

func TestWorkspaceLayoutFitsAndWallGeneration(t *testing.T) {
	w := workspaceFixture()
	for _, size := range []tea.WindowSizeMsg{{Width: 140, Height: 35}, {Width: 80, Height: 24}, {Width: 40, Height: 10}} {
		w.Update(size)
		for _, focus := range []bool{false, true} {
			w.wallFocus = focus
			lines := strings.Split(w.View(), "\n")
			if len(lines) != size.Height {
				t.Fatalf("height: got %d want %d", len(lines), size.Height)
			}
			for _, line := range lines {
				if ansi.StringWidth(line) != size.Width {
					t.Fatalf("line does not fill %d columns: %q", size.Width, line)
				}
			}
			if strings.Count(w.View(), " amy · ") != 1 {
				t.Fatal("dashboard repeats the app header")
			}
		}
	}
	w.Update(workspaceMsg{"search", w.generation, workspaceChoice{&find.Choice{View: &find.View{Project: "Botany", Kind: "commit", Ref: "example"}}}})
	if w.wallGeneration != 1 || !w.wallFocus {
		t.Fatal("selected artifact did not replace/focus wall")
	}
	_, cmd := w.Update(workspaceMsg{"wall", 0, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}}})
	if cmd != nil {
		t.Fatal("old wall response affected new wall")
	}
}

// Kitty placeholders consist of one cell plus combining row/column marks.
// Cropping and joining panels must preserve those marks and their colour.
type workspaceImagePanel struct{ workspacePanelRecorder }

func (p *workspaceImagePanel) Update(msg tea.Msg) (tea.Model, tea.Cmd) { return p, nil }

func (*workspaceImagePanel) View() string {
	row := "\x1b[38;2;1;2;3m" + strings.Repeat("\U0010EEEE\u0305\u030d", 200) + "\x1b[39m"
	return strings.Repeat(row+"\n", 50)
}

func TestWorkspaceClipsImageCellsToVisiblePanel(t *testing.T) {
	w := NewWorkspace(apiclient.New(apiclient.Config{Endpoint: "https://example.test"}), WorkspaceOptions{
		Scope: apiclient.Config{}.Workspace("Garden"), Wall: wall.Options{Graphics: "kitty"},
	})
	if w.o.Wall.Graphics != "kitty" {
		t.Fatal("dashboard disabled sharp images")
	}
	w.wall = &workspaceImagePanel{}
	for _, width := range []int{140, 105, 80, 160} {
		w.Update(tea.WindowSizeMsg{Width: width, Height: 24})
		left, right, h := w.sizes()
		for _, focus := range []bool{false, true} {
			w.wallFocus = focus
			frame := w.View()
			visible := width >= 105 || focus
			for y, line := range strings.Split(frame, "\n") {
				if ansi.StringWidth(line) != width {
					t.Fatalf("image overflow at width %d, row %d", width, y)
				}
				n := strings.Count(line, "\U0010EEEE\u0305\u030d")
				want := 0
				if visible && y > 0 && y <= h {
					want = right
				}
				if n != want {
					t.Fatalf("row %d: %d intact image cells, want %d; %q", y, n, want, line)
				}
				if visible && width >= 105 && y > 0 && y <= h {
					if strings.ContainsRune(ansi.Truncate(line, left+1, ""), '\U0010EEEE') {
						t.Fatal("image crossed the divider into navigation")
					}
				}
			}
		}
	}
}

func TestWorkspaceCommandsTagBatchResults(t *testing.T) {
	cmd := workspaceCommand("app", 3, tea.Batch(func() tea.Msg { return loadedTasks() }, func() tea.Msg { return flashMsg{"ready"} }))
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatal("batch was swallowed")
	}
	for _, child := range batch {
		m, ok := child().(workspaceMsg)
		if !ok || m.panel != "app" || m.generation != 3 {
			t.Fatal("batch child lost destination")
		}
	}
}

type workspacePanelRecorder struct{ messages []tea.Msg }

func (p *workspacePanelRecorder) Init() tea.Cmd { return nil }
func (p *workspacePanelRecorder) View() string  { return "side wall" }
func (p *workspacePanelRecorder) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	p.messages = append(p.messages, msg)
	return p, nil
}

type expandingWall struct {
	workspacePanelRecorder
	expanded bool
	width    int
}

func (p *expandingWall) Fullscreen() bool { return p.expanded }
func (p *expandingWall) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	p.messages = append(p.messages, msg)
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		p.width = m.Width
	case tea.KeyMsg:
		if m.Type == tea.KeyEnter {
			p.expanded = true
		}
		if m.Type == tea.KeyEsc {
			p.expanded = false
		}
	}
	return p, nil
}

func TestWorkspaceDiffExpandsAndReturnsWithoutLosingNavigation(t *testing.T) {
	w := workspaceFixture()
	p := &expandingWall{}
	w.wall = p
	w.Update(tea.WindowSizeMsg{Width: 160, Height: 30})
	w.Update(tea.KeyMsg{Type: tea.KeyF2})
	w.Update(workspaceMsg{"app", w.generation, loadedTasks()})
	w.Update(tea.KeyMsg{Type: tea.KeyCtrlW})
	splitWidth := p.width
	w.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if p.width != 160 || !w.wallExpanded() || strings.Contains(w.View(), "water the ferns") {
		t.Fatal("diff did not take both panels")
	}
	finished := wall.FileOpened{Err: errors.New("Vim could not start")}
	w.Update(finished)
	if got, ok := p.messages[len(p.messages)-1].(wall.FileOpened); !ok || got.Err != finished.Err || !w.wallExpanded() {
		t.Fatal("editor completion did not return to the expanded wall")
	}
	w.Update(tea.MouseMsg{X: 7, Y: 5, Button: tea.MouseButtonWheelDown})
	if got := p.messages[len(p.messages)-1].(tea.MouseMsg); got.X != 7 || got.Y != 4 {
		t.Fatalf("expanded mouse coordinates: %+v", got)
	}
	w.Update(tea.KeyMsg{Type: tea.KeyCtrlW})
	if p.width != splitWidth || !strings.Contains(w.View(), "water the ferns") {
		t.Fatal("switching focus did not restore navigation")
	}
	w.Update(tea.MouseMsg{X: 159, Y: 5, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if p.width != 160 {
		t.Fatal("clicking back into the diff did not expand it")
	}
	w.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if w.wallExpanded() || p.width != splitWidth || !strings.Contains(w.View(), "water the ferns") {
		t.Fatal("Escape did not return to original split view")
	}
}

func TestWorkspacePanelNavigationPreservesInputs(t *testing.T) {
	w := workspaceFixture()
	w.Update(tea.WindowSizeMsg{Width: 140, Height: 35})
	wall := &workspacePanelRecorder{}
	w.wall = wall
	w.Update(tea.KeyMsg{Type: tea.KeyTab})
	w.Update(tea.KeyMsg{Type: tea.KeyDown})
	if !w.wallFocus || len(wall.messages) != 1 || wall.messages[0].(tea.KeyMsg).Type != tea.KeyDown {
		t.Fatal("Tab did not give the wall the navigation keys")
	}
	w.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if w.wallFocus {
		t.Fatal("Shift+Tab did not return to navigation")
	}
	w.Update(tea.KeyMsg{Type: tea.KeyF4})
	notes := w.app.top().(*notesView)
	notes.input.SetValue("unfinished search")
	w.Update(tea.KeyMsg{Type: tea.KeyCtrlW})
	if !w.wallFocus {
		t.Fatal("Ctrl+W failed while Notes had keyboard focus")
	}
	w.Update(tea.KeyMsg{Type: tea.KeyCtrlW})
	if w.wallFocus || notes.input.Value() != "unfinished search" {
		t.Fatal("switching panels discarded the input")
	}
	w.Update(tea.KeyMsg{Type: tea.KeyTab})
	if w.wallFocus || notes.typing {
		t.Fatal("Notes lost its existing Tab-to-browse key")
	}
	if !strings.Contains(w.View(), "Ctrl+W") {
		t.Fatal("panel switch hidden from the footer")
	}
}

func TestWorkspaceWallKeysDoNotReachTasks(t *testing.T) {
	w := workspaceFixture()
	w.Update(tea.WindowSizeMsg{Width: 140, Height: 35})
	w.Update(tea.KeyMsg{Type: tea.KeyF2})
	w.Update(workspaceMsg{"app", w.generation, loadedTasks()})
	tasks := w.app.top().(*tasksView)
	wall := &workspacePanelRecorder{}
	w.wall = wall
	start := tasks.cursor
	w.Update(tea.KeyMsg{Type: tea.KeyCtrlW})
	for _, key := range []tea.KeyMsg{keyMsg("j"), keyMsg(" "), keyMsg("b"), keyMsg("?"), {Type: tea.KeyCtrlD}, {Type: tea.KeyCtrlU}} {
		_, cmd := w.Update(key)
		if cmd != nil || tasks.cursor != start || tasks.busy || w.app.help {
			t.Fatalf("wall key %q affected the left panel", key.String())
		}
	}
	if len(wall.messages) != 6 {
		t.Fatal("wall did not receive its keys")
	}
	w.Update(tea.KeyMsg{Type: tea.KeyCtrlW})
	w.Update(keyMsg("j"))
	_, cmd := w.Update(keyMsg(" "))
	if tasks.cursor == start || cmd == nil || !tasks.busy || len(wall.messages) != 6 {
		t.Fatal("left panel lost its normal move/toggle keys")
	}
}

func TestWorkspaceMouseFocusAndCoordinates(t *testing.T) {
	w := workspaceFixture()
	w.Update(tea.WindowSizeMsg{Width: 140, Height: 35})
	wall := &workspacePanelRecorder{}
	w.wall = wall
	left, _, _ := w.sizes()
	w.Update(tea.MouseMsg{X: left + 5, Y: 7, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if !w.wallFocus || len(wall.messages) != 1 {
		t.Fatal("clicking the side wall did not focus it")
	}
	local := wall.messages[0].(tea.MouseMsg)
	if local.X != 4 || local.Y != 6 {
		t.Fatalf("wall received wrong coordinates: %+v", local)
	}
	w.Update(tea.MouseMsg{X: 5, Y: 7, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if w.wallFocus {
		t.Fatal("clicking navigation did not return focus")
	}
	w.Update(tea.MouseMsg{X: left + 5, Y: 7, Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	if len(wall.messages) != 2 || w.wallFocus {
		t.Fatal("wheel must scroll its hovered panel without taking typing focus")
	}
	w.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	w.Update(tea.KeyMsg{Type: tea.KeyCtrlW})
	w.Update(tea.MouseMsg{X: 7, Y: 8, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	local = wall.messages[len(wall.messages)-1].(tea.MouseMsg)
	if local.X != 7 || local.Y != 7 {
		t.Fatalf("narrow wall coordinates: %+v", local)
	}
}
