package tui

import (
	"strings"
	"testing"

	"github.com/base698/amythest/internal/kanban/board"
)

func testCard() board.Card {
	return board.Card{
		ID:     "c1",
		Title:  "Weekend chores",
		Status: board.Ready,
		Description: "Some intro text\n\n- [ ] water the ferns\n- [x] sweep ✅ 2026-08-01\nplain line\n- [ ] last item",
	}
}

func TestCardViewFindsCheckboxLines(t *testing.T) {
	v := newCardView(nil, "personal", testCard())
	if len(v.checkIdxs) != 3 {
		t.Fatalf("checkIdxs = %v", v.checkIdxs)
	}
	// 0-based description lines: 2, 3, and 5.
	if v.checkIdxs[0] != 2 || v.checkIdxs[1] != 3 || v.checkIdxs[2] != 5 {
		t.Fatalf("checkIdxs = %v", v.checkIdxs)
	}
}

func TestCardViewFocusMovesOnlyBetweenCheckboxes(t *testing.T) {
	v := newCardView(nil, "personal", testCard())
	next, _ := v.Update(keyMsg("j"))
	cv := next.(*cardView)
	if cv.checkIdxs[cv.focus] != 3 {
		t.Fatalf("focus line = %d", cv.checkIdxs[cv.focus])
	}
	next, _ = cv.Update(keyMsg("j"))
	cv = next.(*cardView)
	if cv.checkIdxs[cv.focus] != 5 { // skipped the plain line
		t.Fatalf("focus line = %d", cv.checkIdxs[cv.focus])
	}
}

func TestCardViewSpaceStartsToggleOfFocusedLine(t *testing.T) {
	v := newCardView(nil, "personal", testCard())
	next, cmd := v.Update(keyMsg(" "))
	cv := next.(*cardView)
	if !cv.Busy() || cmd == nil {
		t.Fatalf("busy = %v", cv.Busy())
	}
}

func TestCardViewSavedCardUpdatesDescription(t *testing.T) {
	v := newCardView(nil, "personal", testCard())
	updated := testCard()
	updated.Description = strings.Replace(updated.Description, "- [ ] water the ferns", "- [x] water the ferns ✅ 2026-08-10", 1)
	next, _ := v.Update(cardSavedMsg{&updated})
	cv := next.(*cardView)
	if cv.Busy() {
		t.Fatal("still busy after save")
	}
	if !strings.Contains(cv.card.Description, "✅ 2026-08-10") {
		t.Fatalf("description = %q", cv.card.Description)
	}
	out := cv.View(100, 30)
	if !strings.Contains(out, "water the ferns") {
		t.Fatalf("render:\n%s", out)
	}
}

func TestCardViewArchivedCardPopsView(t *testing.T) {
	v := newCardView(nil, "personal", testCard())
	archived := testCard()
	archived.Status = board.Done
	next, cmd := v.Update(cardSavedMsg{&archived})
	if cmd == nil {
		t.Fatal("expected pop command")
	}
	if _, ok := cmd().(popMsg); !ok {
		t.Fatalf("cmd msg = %#v", cmd())
	}
	_ = next
}
