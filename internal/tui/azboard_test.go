package tui

import (
	"strings"
	"testing"

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
	out := iv.View(100, 30)
	for _, want := range []string{"#55", "Story", "Active", "Ada Lovelace", "2 comment(s)", "Hello world", "_workitems/edit/55"} {
		if !strings.Contains(out, want) {
			t.Fatalf("detail missing %q:\n%s", want, out)
		}
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
