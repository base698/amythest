package tui

import (
	"strings"
	"testing"

	"github.com/base698/amythest/internal/apiclient"
	"github.com/base698/amythest/internal/find"
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
				if ansi.StringWidth(line) > size.Width {
					t.Fatalf("line wider than %d: %s", size.Width, line)
				}
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
