package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/base698/amythest/internal/kanban/board"
)

func loadedBoard() boardLoadedMsg {
	return boardLoadedMsg{b: &board.Board{
		Name: "personal",
		Cards: []board.Card{
			{ID: "a", Title: "First", Status: board.Triage},
			{ID: "b", Title: "Second", Status: board.Triage},
			{ID: "c", Title: "Doing", Status: board.InProgress},
		},
	}}
}

func TestBoardViewColumnNavigationAndCurrentCard(t *testing.T) {
	v := newBoardView(nil, "personal")
	next, _ := v.Update(loadedBoard())
	bv := next.(*boardView)
	if bv.currentCard() == nil || bv.currentCard().ID != "a" {
		t.Fatalf("current = %+v", bv.currentCard())
	}
	bv.Update(keyMsg("j"))
	if bv.currentCard().ID != "b" {
		t.Fatalf("after j: %+v", bv.currentCard())
	}
	// Move focus to the in_progress column (index 3 in boardColumns).
	bv.Update(keyMsg("l"))
	bv.Update(keyMsg("l"))
	bv.Update(keyMsg("l"))
	if bv.currentCard() == nil || bv.currentCard().ID != "c" {
		t.Fatalf("in_progress current = %+v", bv.currentCard())
	}
}

func TestBoardViewIgnoresBoardLoadForOtherBoard(t *testing.T) {
	v := newBoardView(nil, "personal")
	other := loadedBoard()
	other.b.Name = "work"
	next, _ := v.Update(other)
	bv := next.(*boardView)
	if bv.loaded {
		t.Fatal("loaded a board payload for a different board")
	}
}

func TestBoardViewFocusingDoneColumnRequestsArchive(t *testing.T) {
	v := newBoardView(nil, "personal")
	next, _ := v.Update(loadedBoard())
	bv := next.(*boardView)
	var cmd interface{}
	for i := 0; i < len(boardColumns)-1; i++ {
		_, c := bv.Update(keyMsg("l"))
		cmd = c
	}
	if boardColumns[bv.col] != board.Done {
		t.Fatalf("col = %v", boardColumns[bv.col])
	}
	if cmd == nil {
		t.Fatal("expected archive load command when focusing Done")
	}
	if !bv.Busy() {
		t.Fatal("expected busy while archive loads")
	}
}

func TestBoardViewMoveOpensPickerWithLanesAndBoards(t *testing.T) {
	v := newBoardView(nil, "personal")
	next, _ := v.Update(loadedBoard())
	bv := next.(*boardView)
	_, cmd := bv.Update(keyMsg("m"))
	if cmd == nil || !bv.Busy() {
		t.Fatal("m must fetch the board list for the picker")
	}
	bv.Update(boardMovePickerMsg{board: "personal", cardID: "a", boards: []board.BoardSummary{
		{Name: "personal", DisplayName: "Personal"},
		{Name: "work", DisplayName: "Work"},
		{Name: "old", Archived: true},
	}})
	if !bv.picker.active || !bv.Capturing() {
		t.Fatal("picker should be open and capturing")
	}
	// 6 lanes + only the non-archived, non-current board.
	if len(bv.picker.options) != 7 {
		t.Fatalf("options = %+v", bv.picker.options)
	}
	out := bv.View(120, 40)
	if !strings.Contains(out, "→ board: Work") || !strings.Contains(out, "(current)") {
		t.Fatalf("picker render:\n%s", out)
	}
	// Cursor starts on the current lane (Triage for card "a"); j + enter
	// chooses Backlog and closes the picker.
	next2, cmd := bv.Update(keyMsg("j"))
	bv = next2.(*boardView)
	next2, cmd = bv.Update(enterMsg())
	bv = next2.(*boardView)
	if bv.picker.active || cmd == nil || !bv.Busy() {
		t.Fatal("enter on a lane must start the move")
	}
}

func TestBoardViewPickerEscCancels(t *testing.T) {
	v := newBoardView(nil, "personal")
	next, _ := v.Update(loadedBoard())
	bv := next.(*boardView)
	bv.Update(keyMsg("m"))
	bv.Update(boardMovePickerMsg{board: "personal", cardID: "a", boards: nil})
	if !bv.picker.active {
		t.Fatal("picker should be open")
	}
	bv.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if bv.picker.active || bv.Busy() {
		t.Fatal("esc must close the picker without moving")
	}
}

func TestBoardViewShiftDConfirmsBeforeDeleting(t *testing.T) {
	v := newBoardView(nil, "personal")
	next, _ := v.Update(loadedBoard())
	bv := next.(*boardView)
	bv.Update(keyMsg("D"))
	if !bv.del.active || !bv.Capturing() {
		t.Fatal("D should open the delete confirm")
	}
	out := bv.View(120, 40)
	if !strings.Contains(out, `delete card "First"`) {
		t.Fatalf("confirm render:\n%s", out)
	}
	// Any key but y backs out without deleting.
	_, cmd := bv.Update(keyMsg("n"))
	if bv.del.active || bv.Busy() || cmd != nil {
		t.Fatal("n must cancel the delete")
	}
	// y proceeds.
	bv.Update(keyMsg("D"))
	_, cmd = bv.Update(keyMsg("y"))
	if cmd == nil || !bv.Busy() {
		t.Fatal("y must start the delete")
	}
}
