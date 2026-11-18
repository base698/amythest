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
