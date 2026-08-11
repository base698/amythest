package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/base698/amythest/internal/apiclient"
)

func TestAddTaskViewDefaultsToDailyNote(t *testing.T) {
	client := apiclient.New(apiclient.Config{Endpoint: "http://test.example"})
	v := newAddTaskView(client)
	v.Init()
	if !v.Capturing() {
		t.Fatal("text input should capture keys")
	}
	for _, r := range "water the ferns" {
		v.Update(keyMsg(string(r)))
	}
	next, _ := v.Update(enterMsg())
	av := next.(*addTaskView)
	if av.step != 1 || av.cursor != 0 {
		t.Fatalf("step=%d cursor=%d", av.step, av.cursor)
	}
	out := av.View(100, 30)
	if !strings.Contains(out, "Today's daily note") {
		t.Fatalf("daily option missing:\n%s", out)
	}
	_, cmd := av.Update(enterMsg())
	if cmd == nil || !av.Busy() {
		t.Fatal("enter on the daily option must submit")
	}
	// Success pops the view.
	_, cmd = av.Update(taskAddedMsg{path: "Daily Notes/2026-08-11.md"})
	if cmd == nil {
		t.Fatal("expected pop after add")
	}
	if _, ok := cmd().(popMsg); !ok {
		t.Fatalf("cmd msg = %#v", cmd())
	}
}

func TestAddTaskViewSearchPicksAnotherNote(t *testing.T) {
	client := apiclient.New(apiclient.Config{Endpoint: "http://test.example"})
	v := newAddTaskView(client)
	v.Init()
	for _, r := range "call plumber" {
		v.Update(keyMsg(string(r)))
	}
	v.Update(enterMsg()) // to destination step
	v.Update(keyMsg("/"))
	if !v.Capturing() {
		t.Fatal("search input should capture keys")
	}
	for _, r := range "inbox" {
		v.Update(keyMsg(string(r)))
	}
	next, cmd := v.Update(enterMsg())
	av := next.(*addTaskView)
	if cmd == nil || !av.Busy() {
		t.Fatal("enter must run the search")
	}
	av.Update(addTaskResultsMsg{results: []apiclient.SearchResult{
		{Slug: "Tasks/Task-Inbox", Title: "Task Inbox"},
	}})
	if av.cursor != 1 {
		t.Fatalf("cursor should land on the first result, got %d", av.cursor)
	}
	out := av.View(100, 30)
	if !strings.Contains(out, "Task Inbox") {
		t.Fatalf("result missing:\n%s", out)
	}
	_, cmd = av.Update(enterMsg())
	if cmd == nil || !av.Busy() {
		t.Fatal("enter on a result must submit")
	}
	// k moves back up to the daily-note default.
	av.busy = false
	av.Update(keyMsg("k"))
	if av.cursor != 0 {
		t.Fatalf("cursor after k = %d", av.cursor)
	}
}

func TestAddTaskViewEscStepsBackToDefaultSelectionNotCancel(t *testing.T) {
	client := apiclient.New(apiclient.Config{Endpoint: "http://test.example"})
	v := newAddTaskView(client)
	v.Init()
	for _, r := range "call plumber" {
		v.Update(keyMsg(string(r)))
	}
	v.Update(enterMsg()) // destination step
	v.Update(keyMsg("/"))
	for _, r := range "inbox" {
		v.Update(keyMsg(string(r)))
	}
	v.Update(enterMsg())
	v.Update(addTaskResultsMsg{results: []apiclient.SearchResult{{Slug: "Tasks/Task-Inbox", Title: "Task Inbox"}}})
	if v.cursor != 1 {
		t.Fatalf("cursor = %d", v.cursor)
	}
	if !v.Capturing() {
		t.Fatal("modal view must always capture so esc never pops it")
	}
	// esc: back to the default (daily) selection with results cleared…
	v.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if v.cursor != 0 || len(v.results) != 0 || v.step != 1 {
		t.Fatalf("after esc: cursor=%d results=%d step=%d", v.cursor, len(v.results), v.step)
	}
	// …and only the next esc returns to editing the task text.
	v.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if v.step != 0 {
		t.Fatalf("second esc should return to text step, step=%d", v.step)
	}
	if v.text.Value() != "call plumber" {
		t.Fatalf("task text lost: %q", v.text.Value())
	}
}

func TestBoardViewPlusOpensCardPromptInFocusedColumn(t *testing.T) {
	client := apiclient.New(apiclient.Config{Endpoint: "http://test.example"})
	v := newBoardView(client, "personal")
	next, _ := v.Update(loadedBoard())
	bv := next.(*boardView)
	bv.Update(keyMsg("l")) // Backlog column
	bv.Update(keyMsg("+"))
	if !bv.addingCard || !bv.Capturing() {
		t.Fatal("+ should open the new-card prompt")
	}
	if !strings.Contains(bv.newCard.Prompt, "Backlog") {
		t.Fatalf("prompt = %q", bv.newCard.Prompt)
	}
	for _, r := range "Try the hose" {
		bv.Update(keyMsg(string(r)))
	}
	_, cmd := bv.Update(enterMsg())
	if cmd == nil || !bv.Busy() {
		t.Fatal("enter must create the card")
	}
	// esc cancels a fresh prompt without creating.
	bv.busy = false
	bv.Update(keyMsg("+"))
	bv.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if bv.addingCard || bv.Busy() {
		t.Fatal("esc must close the prompt")
	}
}
