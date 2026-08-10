package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/base698/amythest/internal/kanban/board"
	"github.com/base698/amythest/internal/tasks"
)

func loadedToday() todayLoadedMsg {
	overdueTask := tasks.Task{Slug: "chores", Path: "chores.md", Line: 2, Text: "water the ferns", Status: tasks.StatusOpen, Due: "2026-08-08", Priority: 3, Version: strings.Repeat("a", 64)}
	todayTask := tasks.Task{Slug: "chores", Path: "chores.md", Line: 3, Text: "sweep the porch", Status: tasks.StatusOpen, Due: "2026-08-10", Priority: 3, Version: strings.Repeat("a", 64)}
	focusCard := board.Card{ID: "f1", Title: "Ship the release", Status: board.InProgress}
	dueCard := board.Card{ID: "d1", Title: "Renew domain", Status: board.Ready, DueDate: "2026-08-10"}
	items := []todayItem{
		{section: "Due today", task: &todayTask},
		{section: "Overdue", task: &overdueTask},
		{section: "Focus", card: &focusCard, board: "work"},
		{section: "Due today", card: &dueCard, board: "personal"},
	}
	sortTodayItems(items)
	return todayLoadedMsg{items}
}

func TestTodayViewSectionsOrderFocusOverdueThenDueToday(t *testing.T) {
	v := newTodayView(nil)
	next, _ := v.Update(loadedToday())
	tv := next.(*todayView)
	if len(tv.items) != 4 {
		t.Fatalf("items = %d", len(tv.items))
	}
	order := []string{tv.items[0].section, tv.items[1].section, tv.items[2].section, tv.items[3].section}
	want := []string{"Focus", "Overdue", "Due today", "Due today"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("section order = %v", order)
		}
	}
	if tv.items[0].card == nil || tv.items[0].card.ID != "f1" {
		t.Fatalf("focus first: %+v", tv.items[0])
	}
}

func TestTodayViewRendersSectionsAndKinds(t *testing.T) {
	v := newTodayView(nil)
	next, _ := v.Update(loadedToday())
	tv := next.(*todayView)
	out := tv.View(80, 30) // narrow: no gem
	for _, want := range []string{"Focus", "Overdue", "Due today", "[task]", "[card]", "water the ferns", "Ship the release"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	wide := tv.View(140, 40) // wide: gem appears
	if !strings.Contains(wide, "amythest") {
		t.Fatalf("gem caption missing in wide render")
	}
}

func TestTodayViewSlashSearchJumpsAndNCycles(t *testing.T) {
	v := newTodayView(nil)
	next, _ := v.Update(loadedToday())
	tv := next.(*todayView)
	if tv.cursor != 0 {
		t.Fatalf("cursor = %d", tv.cursor)
	}
	tv.Update(keyMsg("/"))
	if !tv.Capturing() {
		t.Fatal("search prompt should capture keys")
	}
	for _, r := range "sweep" {
		tv.Update(keyMsg(string(r)))
	}
	tv.Update(enterMsg())
	if tv.Capturing() {
		t.Fatal("prompt should close on enter")
	}
	if cur := tv.current(); cur == nil || cur.text() != "sweep the porch" {
		t.Fatalf("cursor after search: %+v", cur)
	}
	// n wraps around to the same single match.
	tv.Update(keyMsg("n"))
	if tv.current().text() != "sweep the porch" {
		t.Fatalf("after n: %+v", tv.current())
	}
}

func TestTodayViewDKeyCompletesCardAndSpaceUndoes(t *testing.T) {
	v := newTodayView(nil)
	next, _ := v.Update(loadedToday())
	tv := next.(*todayView)
	// Move to the focus card (first row).
	if tv.current().card == nil {
		t.Fatalf("expected card first: %+v", tv.current())
	}
	_, cmd := tv.Update(keyMsg("d"))
	if cmd == nil || !tv.Busy() {
		t.Fatal("d on a card must start the archive request")
	}
	// Server confirms: item stays in the list, marked done.
	archived := *tv.current().card
	archived.Status = board.Done
	tv.Update(cardArchivedMsg{board: "work", card: &archived, prevStatus: board.InProgress})
	if !tv.current().isDone() {
		t.Fatal("card should be marked done in place")
	}
	out := tv.View(80, 30)
	if !strings.Contains(out, "Ship the release ✓") {
		t.Fatalf("done marker missing:\n%s", out)
	}
	// Space now restores to the recorded previous column.
	_, cmd = tv.Update(keyMsg(" "))
	if cmd == nil || !tv.Busy() {
		t.Fatal("space on a done card must start the restore request")
	}
	tv.Update(cardRestoredMsg{board: "work", cardID: "f1", status: board.InProgress})
	if tv.current().isDone() {
		t.Fatal("card should be unmarked after restore")
	}
}

func TestTodayViewTaskToggleMarksInPlace(t *testing.T) {
	v := newTodayView(nil)
	next, _ := v.Update(loadedToday())
	tv := next.(*todayView)
	tv.Update(keyMsg("j")) // onto the overdue task
	task := tv.current().task
	if task == nil || task.Text != "water the ferns" {
		t.Fatalf("cursor on %+v", tv.current())
	}
	_, cmd := tv.Update(keyMsg(" "))
	if cmd == nil {
		t.Fatal("space must start the toggle")
	}
	tv.Update(taskToggledMsg{slug: task.Slug, text: task.Text, done: true})
	if !tv.current().isDone() {
		t.Fatal("task should be marked done in place, not vanish")
	}
	out := tv.View(80, 30)
	if !strings.Contains(out, "water the ferns ✓") {
		t.Fatalf("done marker missing:\n%s", out)
	}
}

func TestGemShimmerChangesBetweenPhasesButKeepsShape(t *testing.T) {
	// Test runners have no TTY, so lipgloss would strip all color and both
	// phases would collapse to the same string; force a profile.
	restore := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(restore)

	a, b := renderGem(0), renderGem(3)
	if a == b {
		t.Fatal("shimmer phases render identically")
	}
	stripA := stripANSI(a)
	stripB := stripANSI(b)
	if stripA != stripB {
		t.Fatal("shimmer must only change colors, not characters")
	}
	if !strings.Contains(stripA, "amythest") {
		t.Fatal("caption missing")
	}
}

func TestParseDueInput(t *testing.T) {
	now := time.Date(2026, 8, 10, 15, 0, 0, 0, time.UTC)
	cases := []struct{ in, want string }{
		{"", ""},
		{"today", "2026-08-10"},
		{"tomorrow", "2026-08-11"},
		{"+3", "2026-08-13"},
		{"2026-09-01", "2026-09-01"},
	}
	for _, c := range cases {
		got, err := parseDueInput(c.in, now)
		if err != nil || got != c.want {
			t.Fatalf("parseDueInput(%q) = %q, %v", c.in, got, err)
		}
	}
	if _, err := parseDueInput("someday", now); err == nil {
		t.Fatal("expected error for junk input")
	}
	if _, err := parseDueInput("2026-13-40", now); err == nil {
		t.Fatal("expected error for invalid date")
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		switch {
		case inEsc:
			if r == 'm' {
				inEsc = false
			}
		case r == '\x1b':
			inEsc = true
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
