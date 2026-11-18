package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/base698/amythest/internal/kanban/board"
)

func TestCardPagesPastChecklistToCommentsAndWrapsHeader(t *testing.T) {
	card := testCard()
	card.Title = strings.Repeat("Readable title with context ", 5)
	card.Labels = []string{strings.Repeat("long-label ", 15)}
	card.Description += "\n" + strings.Repeat("Long explanation of the next step.\n", 40)
	card.Comments = []board.Comment{{Author: strings.Repeat("Long author name ", 6), Body: "Comment at the end"}}
	v := newCardView(nil, "example", card)
	for _, width := range []int{40, 80, 160} {
		for _, height := range []int{10, 30} {
			v.offset = 0
			out := v.View(width, height)
			if len(strings.Split(out, "\n")) != height {
				t.Fatalf("card height: %d × %d\n%s", width, height, out)
			}
			v.Update(keyMsg("G"))
			out = v.View(width, height)
			if !strings.Contains(out, "Comment at the end") || v.focusVisible {
				t.Fatalf("could not reach comments past checklist:\n%s", out)
			}
			for _, row := range strings.Split(out, "\n") {
				if ansi.StringWidth(row) > width {
					t.Fatalf("card overflow: %q", row)
				}
			}
			end := v.offset
			v.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
			v.View(width, height)
			if v.offset >= end {
				t.Fatal("half-page up did not scroll")
			}
			_, cmd := v.Update(keyMsg(" "))
			if cmd != nil || v.busy {
				t.Fatal("Space toggled an offscreen checkbox")
			}
			v.View(width, height)
			if !v.focusVisible {
				t.Fatal("Space did not reveal the checklist")
			}
		}
	}
}

func TestWrapLongFirstWordPreservesEveryCharacter(t *testing.T) {
	word := strings.Repeat("界abcdefgh", 8)
	rows := wrapLine(word, 15)
	var joined strings.Builder
	for _, row := range rows {
		if ansi.StringWidth(row) > 15 {
			t.Fatalf("unwrapped long word: %q", row)
		}
		joined.WriteString(strings.TrimSpace(row))
	}
	if joined.String() != word {
		t.Fatal("wrapping lost text")
	}
}

func testCard() board.Card {
	return board.Card{
		ID:          "c1",
		Title:       "Weekend chores",
		Status:      board.Ready,
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
