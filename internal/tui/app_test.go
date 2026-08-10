package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/base698/amythest/internal/apiclient"
	"github.com/base698/amythest/internal/kanban/board"
)

// drive the real App model without a terminal: size it, feed data and keys,
// and assert on rendered frames.
func TestAppRendersTasksNavigatesToBoardsAndBack(t *testing.T) {
	client := apiclient.New(apiclient.Config{Endpoint: "http://test.example"})
	app := NewApp(client)
	app.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	// Home is the today view.
	frame := app.View()
	if !strings.Contains(frame, "amy · today") {
		t.Fatalf("home frame:\n%s", frame)
	}

	// "2" swaps to the tasks view.
	app.Update(keyMsg("2"))
	app.Update(loadedTasks())
	frame = app.View()
	if !strings.Contains(frame, "amy · tasks") || !strings.Contains(frame, "water the ferns") {
		t.Fatalf("tasks frame:\n%s", frame)
	}
	if !strings.Contains(frame, "http://test.example") {
		t.Fatalf("status bar missing endpoint:\n%s", frame)
	}

	// "3" swaps to the boards view and kicks off its load.
	_, cmd := app.Update(keyMsg("3"))
	if cmd == nil {
		t.Fatal("expected boards load command")
	}
	app.Update(boardsLoadedMsg{boards: []board.BoardSummary{{
		Name: "personal", DisplayName: "Personal",
		Counts: map[board.Status]int{board.Triage: 2, board.Done: 5},
	}}})
	frame = app.View()
	if !strings.Contains(frame, "amy · boards") || !strings.Contains(frame, "Personal") {
		t.Fatalf("boards frame:\n%s", frame)
	}

	// enter pushes the board view; the push command must be executed by the
	// runtime, so simulate that dispatch loop here.
	_, cmd = app.Update(enterMsg())
	if cmd == nil {
		t.Fatal("expected push command")
	}
	if _, isPush := cmd().(pushMsg); !isPush {
		t.Fatalf("expected pushMsg, got %#v", cmd())
	}
	app.Update(cmd())
	app.Update(loadedBoard())
	frame = app.View()
	if !strings.Contains(frame, "amy · boards › personal") || !strings.Contains(frame, "First") {
		t.Fatalf("board frame:\n%s", frame)
	}

	// esc pops back to boards.
	app.Update(tea.KeyMsg{Type: tea.KeyEsc})
	frame = app.View()
	if !strings.Contains(frame, "amy · boards") || strings.Contains(frame, "› personal") {
		t.Fatalf("after esc:\n%s", frame)
	}

	// q quits.
	_, cmd = app.Update(keyMsg("q"))
	if cmd == nil {
		t.Fatal("expected quit command")
	}
}

func TestHelpOverlayClosesOnEscape(t *testing.T) {
	client := apiclient.New(apiclient.Config{Endpoint: "http://test.example"})
	app := NewApp(client)
	app.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	app.Update(keyMsg("?"))
	if !strings.Contains(app.View(), "Keys") {
		t.Fatal("help should be open")
	}
	app.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if strings.Contains(app.View(), "j/k or arrows") {
		t.Fatal("esc must close help")
	}
	// Any other key closes it too, without triggering its normal action.
	app.Update(keyMsg("?"))
	_, cmd := app.Update(keyMsg("q"))
	if cmd != nil {
		t.Fatal("q while help is open must close help, not quit")
	}
	if strings.Contains(app.View(), "j/k or arrows") {
		t.Fatal("q must close help")
	}
}

func TestCoalescedRunesReplayAsIndividualKeys(t *testing.T) {
	client := apiclient.New(apiclient.Config{Endpoint: "http://test.example"})
	app := NewApp(client)
	app.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	app.Update(keyMsg("2")) // tasks view
	app.Update(loadedTasks())
	// One coalesced "jj" must move the cursor two rows, not zero.
	app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("jj")})
	tv := app.top().(*tasksView)
	if tv.current() == nil || tv.current().Text != "card checkbox" {
		t.Fatalf("cursor after coalesced jj: %+v", tv.current())
	}
}

func TestCompletionFeedbackAppearsInStatusBar(t *testing.T) {
	client := apiclient.New(apiclient.Config{Endpoint: "http://test.example"})
	app := NewApp(client)
	app.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	app.Update(cardArchivedMsg{board: "personal", card: &board.Card{ID: "c1"}})
	if !strings.Contains(app.View(), "card archived ✓") {
		t.Fatalf("no archive feedback in:\n%s", app.View())
	}
	app.Update(taskToggledMsg{slug: "s", text: "x", done: true})
	if !strings.Contains(app.View(), "task completed ✓") {
		t.Fatal("no toggle feedback")
	}
}
