package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/base698/amythest/internal/apiclient"
	"github.com/base698/amythest/internal/tasks"
)

func keyMsg(s string) tea.KeyMsg {
	if s == " " {
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func enterMsg() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyEnter} }

func loadedTasks() tasksLoadedMsg {
	return tasksLoadedMsg{groups: []apiclient.TaskGroup{
		{Name: "chores.md", Tasks: []tasks.Task{
			{Slug: "chores", Path: "chores.md", Line: 2, Text: "water the ferns", Status: tasks.StatusOpen, Priority: 3, Version: strings.Repeat("a", 64)},
			{Slug: "chores", Path: "chores.md", Line: 3, Text: "sweep the porch", Status: tasks.StatusDone, Priority: 3, Version: strings.Repeat("a", 64)},
		}},
		{Name: "kanban/personal.md", Tasks: []tasks.Task{
			{Slug: "kanban-personal", Path: "kanban/personal.md", Line: 9, Text: "card checkbox", Status: tasks.StatusOpen, Priority: 3, Version: strings.Repeat("b", 64)},
		}},
	}}
}

func TestTasksViewCursorSkipsGroupHeaders(t *testing.T) {
	v := newTasksView(nil)
	next, _ := v.Update(loadedTasks())
	tv := next.(*tasksView)
	if tv.current() == nil || tv.current().Text != "water the ferns" {
		t.Fatalf("initial cursor on %+v", tv.current())
	}
	next, _ = tv.Update(keyMsg("j"))
	tv = next.(*tasksView)
	if tv.current().Text != "sweep the porch" {
		t.Fatalf("after j: %+v", tv.current())
	}
	// Next j crosses the kanban group header onto its task.
	next, _ = tv.Update(keyMsg("j"))
	tv = next.(*tasksView)
	if tv.current().Text != "card checkbox" {
		t.Fatalf("after jj: %+v", tv.current())
	}
}

func TestTasksViewSpaceTogglesOpenTaskAndSetsBusy(t *testing.T) {
	v := newTasksView(nil)
	next, _ := v.Update(loadedTasks())
	tv := next.(*tasksView)
	next, cmd := tv.Update(keyMsg(" "))
	tv = next.(*tasksView)
	if !tv.Busy() || cmd == nil {
		t.Fatalf("busy = %v, cmd = %v", tv.Busy(), cmd)
	}
	// While busy, another space is ignored.
	if _, cmd := tv.Update(keyMsg(" ")); cmd != nil {
		t.Fatal("expected no command while busy")
	}
}

func TestTasksViewRefusesKanbanPathToggles(t *testing.T) {
	v := newTasksView(nil)
	next, _ := v.Update(loadedTasks())
	tv := next.(*tasksView)
	tv.Update(keyMsg("j"))
	tv.Update(keyMsg("j")) // onto the kanban/ task
	if tv.current().Path != "kanban/personal.md" {
		t.Fatalf("cursor on %+v", tv.current())
	}
	_, cmd := tv.Update(keyMsg(" "))
	if tv.Busy() {
		t.Fatal("kanban task toggle must not start a request")
	}
	if cmd == nil {
		t.Fatal("expected a flash message command")
	}
	if msg, ok := cmd().(flashMsg); !ok || !strings.Contains(msg.text, "board view") {
		t.Fatalf("cmd msg = %#v", cmd())
	}
}

func TestTasksViewRenderShowsDoneStrikethroughAndDue(t *testing.T) {
	v := newTasksView(nil)
	next, _ := v.Update(tasksLoadedMsg{groups: []apiclient.TaskGroup{{
		Tasks: []tasks.Task{{Slug: "s", Path: "p.md", Line: 1, Text: "pay bills", Status: tasks.StatusOpen, Due: "2026-08-12", Priority: 3, Version: strings.Repeat("a", 64)}},
	}}})
	tv := next.(*tasksView)
	out := tv.View(100, 20)
	if !strings.Contains(out, "pay bills") || !strings.Contains(out, "due 2026-08-12") {
		t.Fatalf("render:\n%s", out)
	}
}
