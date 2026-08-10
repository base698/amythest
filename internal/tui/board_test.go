package tui

import (
	"testing"

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

func TestBoardViewMovePromptThenStatusKeyStartsMove(t *testing.T) {
	v := newBoardView(nil, "personal")
	next, _ := v.Update(loadedBoard())
	bv := next.(*boardView)
	bv.Update(keyMsg("m"))
	if !bv.moving {
		t.Fatal("m should arm the move prompt")
	}
	// A cross-board test client isn't wired here; verify arming resets and
	// that an unknown key cancels cleanly instead of moving.
	bv.Update(keyMsg("x"))
	if bv.moving || bv.Busy() {
		t.Fatal("unknown key should cancel the move prompt")
	}
}
