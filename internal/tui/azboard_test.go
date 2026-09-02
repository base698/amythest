package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/base698/amythest/internal/kanban/board"
	"github.com/base698/amythest/internal/source/azboards"
)

func azTestSource() *azboards.Source {
	return azboards.New(azboards.Config{
		Org: "https://dev.azure.com/demo", Project: "Demo",
		Boards: []azboards.BoardConfig{{
			Name: "my-team", Area: `Demo\Team`, Type: "User Story",
			Columns: []string{"New", "Active", "Resolved"},
		}},
	})
}

func azTestItems() []azboards.WorkItem {
	return []azboards.WorkItem{
		{ID: 1, Title: "First", State: "New", Assignee: "Ada Lovelace"},
		{ID: 2, Title: "Second", State: "New"},
		{ID: 3, Title: "Doing", State: "Active"},
	}
}

func TestBoardsViewListsVirtualBoards(t *testing.T) {
	v := newBoardsView(nil, azTestSource())
	next, _ := v.Update(boardsLoadedMsg{[]board.BoardSummary{{Name: "personal"}}})
	bv := next.(*boardsView)
	out := bv.View(100, 30)
	if !strings.Contains(out, "my-team") || !strings.Contains(out, "[azure]") {
		t.Fatalf("virtual board missing from listing:\n%s", out)
	}
	// Cursor walks past the local board onto the virtual one; enter opens it.
	bv.Update(keyMsg("j"))
	if bv.cursor != 1 {
		t.Fatalf("cursor = %d", bv.cursor)
	}
	_, cmd := bv.Update(keyMsg("enter"))
	if cmd == nil {
		t.Fatal("enter on the virtual board should push a view")
	}
	msg := cmd()
	push, ok := msg.(pushMsg)
	if !ok {
		t.Fatalf("msg = %#v", msg)
	}
	az, ok := push.v.(*azBoardView)
	if !ok || az.cfg.Name != "my-team" {
		t.Fatalf("pushed view = %#v", push.v)
	}
}

func TestBoardsViewWithoutAZSource(t *testing.T) {
	v := newBoardsView(nil, nil)
	next, _ := v.Update(boardsLoadedMsg{[]board.BoardSummary{{Name: "personal"}}})
	bv := next.(*boardsView)
	if got := bv.rowCount(); got != 1 {
		t.Fatalf("rowCount = %d", got)
	}
	if out := bv.View(100, 30); strings.Contains(out, "[azure]") {
		t.Fatalf("unexpected azure row:\n%s", out)
	}
}

func TestAZBoardViewColumnsAndNavigation(t *testing.T) {
	v := newAZBoardView(azTestSource(), azTestSource().Boards()[0])
	next, _ := v.Update(azItemsMsg{board: "my-team", items: azTestItems()})
	av := next.(*azBoardView)
	if !av.loaded || len(av.cols) != 3 {
		t.Fatalf("cols = %v", av.cols)
	}
	if it := av.currentItem(); it == nil || it.ID != 1 {
		t.Fatalf("current = %+v", av.currentItem())
	}
	av.Update(keyMsg("j"))
	if av.currentItem().ID != 2 {
		t.Fatalf("after j: %+v", av.currentItem())
	}
	av.Update(keyMsg("l"))
	if av.currentItem().ID != 3 {
		t.Fatalf("active column current = %+v", av.currentItem())
	}
	out := av.View(120, 30)
	for _, want := range []string{"New (2)", "Active (1)", "Resolved (0)", "#1", "Ada L."} {
		if !strings.Contains(out, want) {
			t.Fatalf("view missing %q:\n%s", want, out)
		}
	}
}

func TestAZBoardViewIgnoresOtherBoards(t *testing.T) {
	v := newAZBoardView(azTestSource(), azTestSource().Boards()[0])
	next, _ := v.Update(azItemsMsg{board: "other", items: azTestItems()})
	av := next.(*azBoardView)
	if av.loaded {
		t.Fatal("accepted items for a different board")
	}
}

func TestAZBoardViewLoggedOutBanner(t *testing.T) {
	v := newAZBoardView(azTestSource(), azTestSource().Boards()[0])
	next, _ := v.Update(azItemsMsg{board: "my-team", err: azboards.ErrNotLoggedIn})
	av := next.(*azBoardView)
	out := av.View(100, 30)
	if !strings.Contains(out, "not logged in to Azure DevOps") ||
		!strings.Contains(out, "az devops login --organization https://dev.azure.com/demo") {
		t.Fatalf("banner missing:\n%s", out)
	}
}

func TestAZBoardViewMovePicker(t *testing.T) {
	v := newAZBoardView(azTestSource(), azTestSource().Boards()[0])
	next, _ := v.Update(azItemsMsg{board: "my-team", items: azTestItems()})
	av := next.(*azBoardView)
	av.Update(keyMsg("m"))
	if !av.picker.active || !av.Capturing() {
		t.Fatal("m should open the column picker")
	}
	// Options are the columns; the current state is marked and selected.
	labels := make([]string, len(av.picker.options))
	current := ""
	for i, o := range av.picker.options {
		labels[i] = strings.TrimSuffix(o.label, "  (current)")
		if o.current {
			current = labels[i]
		}
	}
	if strings.Join(labels, ",") != "New,Active,Resolved" || current != "New" {
		t.Fatalf("picker options = %v (current %q)", labels, current)
	}
}

func TestAZItemViewDetailRendering(t *testing.T) {
	v := newAZItemView(azTestSource(), azTestSource().Boards()[0], 55)
	next, _ := v.Update(azItemMsg{board: "my-team", item: azboards.WorkItem{
		ID: 55, Title: "Story", State: "Active", Assignee: "Ada Lovelace",
		CommentCount: 2, Description: "<div>Hello <b>world</b></div>",
	}})
	iv := next.(*azItemView)
	next, _ = iv.Update(azCommentsMsg{board: "my-team", id: 55, comments: []azboards.WorkItemComment{
		{Author: "Grace Hopper", Date: "2026-08-30", Text: "Ship it"},
		{Author: "Ada Lovelace", Date: "2026-08-29", Text: "Needs a rebase first"},
	}})
	iv = next.(*azItemView)
	out := iv.View(100, 30)
	for _, want := range []string{"#55", "Story", "Active", "Ada Lovelace", "Hello world",
		"_workitems/edit/55", "Comments (2)", "Grace Hopper", "2026-08-30", "Ship it", "Needs a rebase first"} {
		if !strings.Contains(out, want) {
			t.Fatalf("detail missing %q:\n%s", want, out)
		}
	}
}

func TestAZItemViewCommentsErrorIsNotFatal(t *testing.T) {
	v := newAZItemView(azTestSource(), azTestSource().Boards()[0], 55)
	next, _ := v.Update(azItemMsg{board: "my-team", item: azboards.WorkItem{ID: 55, Title: "Story", State: "New", CommentCount: 3}})
	iv := next.(*azItemView)
	next, _ = iv.Update(azCommentsMsg{board: "my-team", id: 55, err: fmt.Errorf("comments API: 404")})
	iv = next.(*azItemView)
	out := iv.View(100, 30)
	if !strings.Contains(out, "Story") || !strings.Contains(out, "comments unavailable") ||
		!strings.Contains(out, "Comments (3)") {
		t.Fatalf("comments failure should degrade, not blank the card:\n%s", out)
	}
}

func TestAZShortName(t *testing.T) {
	for name, want := range map[string]string{
		"Ada Lovelace":   "Ada L.",
		"Ada":            "Ada",
		"Ada Byron King": "Ada K.",
		"":               "",
	} {
		if got := azShortName(name); got != want {
			t.Fatalf("azShortName(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestFuzzyMatch(t *testing.T) {
	cases := []struct {
		text, query string
		want        bool
	}{
		{"Ship the deploy pipeline", "dply pipln", true},
		{"Ship the deploy pipeline", "SHIP", true},
		{"Ship the deploy pipeline", "pipeline deploy", true}, // token order is free
		{"Fix login redirect", "xyzzy", false},
		{"anything", "", true},
		{"#12345 Add smoke tests", "12345", true},
		// Tokens match within one word — long titles must not match noise.
		{"Create observability blueprint for ephemeral workspaces", "trrfrm", false},
		{"club-actions-terraform", "trrfrm", true},
	}
	for _, c := range cases {
		if got := fuzzyMatch(c.text, c.query); got != c.want {
			t.Fatalf("fuzzyMatch(%q, %q) = %v, want %v", c.text, c.query, got, c.want)
		}
	}
}

func TestColumnWindow(t *testing.T) {
	// cursor below the window scrolls down; above scrolls up; offset clamps.
	if got := columnWindow(100, 0, 0, 10); got != 0 {
		t.Fatalf("top: %d", got)
	}
	if got := columnWindow(100, 25, 0, 10); got != 16 {
		t.Fatalf("cursor below window: %d", got)
	}
	if got := columnWindow(100, 5, 16, 10); got != 5 {
		t.Fatalf("cursor above window: %d", got)
	}
	if got := columnWindow(8, 7, 20, 10); got != 0 {
		t.Fatalf("clamp when everything fits: %d", got)
	}
	if got := columnWindow(100, 99, 0, 10); got != 90 {
		t.Fatalf("bottom: %d", got)
	}
}

func TestAZBoardViewScrollsLongColumns(t *testing.T) {
	many := make([]azboards.WorkItem, 40)
	for i := range many {
		many[i] = azboards.WorkItem{ID: 1000 + i, Title: fmt.Sprintf("Item number %d", i), State: "New"}
	}
	v := newAZBoardView(azTestSource(), azTestSource().Boards()[0])
	next, _ := v.Update(azItemsMsg{board: "my-team", items: many})
	av := next.(*azBoardView)
	out := av.View(120, 20)
	if !strings.Contains(out, "↓") {
		t.Fatalf("long column should show a below marker:\n%s", out)
	}
	if strings.Contains(out, "#1039") {
		t.Fatalf("items past the window should be clipped:\n%s", out)
	}
	// Every rendered line must fit the terminal: no wrapped rows.
	if h := strings.Count(out, "\n"); h > 20 {
		t.Fatalf("view is %d lines for a height of 20", h)
	}
	// Walk the cursor deep: the window follows and an above marker appears.
	for i := 0; i < 30; i++ {
		av.Update(keyMsg("j"))
	}
	out = av.View(120, 20)
	if !strings.Contains(out, "↑") || !strings.Contains(out, "#1030") {
		t.Fatalf("window did not follow the cursor:\n%s", out)
	}
}

func TestAZBoardViewFuzzyFilterNarrowsColumns(t *testing.T) {
	v := newAZBoardView(azTestSource(), azTestSource().Boards()[0])
	next, _ := v.Update(azItemsMsg{board: "my-team", items: azTestItems()})
	av := next.(*azBoardView)
	av.Update(keyMsg("/"))
	for _, r := range "scnd" { // fuzzy for "Second"
		av.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if got := av.columnItems(0); len(got) != 1 || got[0].ID != 2 {
		t.Fatalf("live filter items = %+v", got)
	}
	av.Update(tea.KeyMsg{Type: tea.KeyEnter}) // commit: filter stays
	if got := av.columnItems(0); len(got) != 1 || got[0].ID != 2 {
		t.Fatalf("committed filter items = %+v", got)
	}
	if out := av.View(120, 30); !strings.Contains(out, "filtering") {
		t.Fatalf("hint missing filter state:\n%s", out)
	}
	// / then esc clears the filter.
	av.Update(keyMsg("/"))
	av.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if got := av.columnItems(0); len(got) != 2 {
		t.Fatalf("cleared filter items = %+v", got)
	}
}

func TestAZBoardViewMineToggleReloads(t *testing.T) {
	v := newAZBoardView(azTestSource(), azTestSource().Boards()[0])
	next, _ := v.Update(azItemsMsg{board: "my-team", items: azTestItems()})
	av := next.(*azBoardView)
	av.Update(keyMsg("f"))
	if !av.mine || !av.busy {
		t.Fatalf("f should toggle mine and reload (mine=%v busy=%v)", av.mine, av.busy)
	}
	// A stale all-items response must not clobber the mine view.
	av.Update(azItemsMsg{board: "my-team", mine: false, items: azTestItems()})
	if !av.busy {
		t.Fatal("stale response accepted")
	}
	av.Update(azItemsMsg{board: "my-team", mine: true, items: azTestItems()[:1]})
	if av.busy || len(av.items) != 1 {
		t.Fatalf("mine response not applied: busy=%v items=%d", av.busy, len(av.items))
	}
}

func TestBoardViewFuzzyFilterNarrowsColumns(t *testing.T) {
	v := newBoardView(nil, "personal")
	next, _ := v.Update(loadedBoard())
	bv := next.(*boardView)
	bv.Update(keyMsg("/"))
	for _, r := range "frst" {
		bv.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	cards := bv.columnCards(0)
	if len(cards) != 1 || cards[0].ID != "a" {
		t.Fatalf("filtered cards = %+v", cards)
	}
}
