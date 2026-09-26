package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/base698/amythest/internal/apiclient"
	"github.com/base698/amythest/internal/tasks"
)

func fixtureNote() *apiclient.Note {
	return &apiclient.Note{
		Slug:  "Projects/Garden",
		Title: "Garden",
		Path:  "Projects/Garden.md",
		Markdown: "# Garden\n\nSee [[Watering Schedule]] and [[Projects/Compost|the compost note]].\n\n" +
			strings.Repeat("filler line\n", 5) + "Also [[Seed Inventory#spring]] here.\n",
	}
}

func TestNoteViewExtractsWikilinksWithAliasesAndFragments(t *testing.T) {
	v := newNoteView(nil, fixtureNote())
	if len(v.links) != 3 {
		t.Fatalf("links = %+v", v.links)
	}
	if v.links[0].target != "Watering Schedule" || v.links[0].label != "Watering Schedule" {
		t.Fatalf("link0 = %+v", v.links[0])
	}
	if v.links[1].target != "Projects/Compost" || v.links[1].label != "the compost note" {
		t.Fatalf("link1 = %+v", v.links[1])
	}
	if v.links[2].target != "Seed Inventory" {
		t.Fatalf("link2 = %+v", v.links[2])
	}
}

func TestNoteViewTabCyclesLinksAndEnterFollows(t *testing.T) {
	v := newNoteView(nil, fixtureNote())
	next, _ := v.Update(tea.KeyMsg{Type: tea.KeyTab})
	nv := next.(*noteView)
	if nv.linkAt != 0 {
		t.Fatalf("linkAt = %d", nv.linkAt)
	}
	nv.Update(tea.KeyMsg{Type: tea.KeyTab})
	if nv.linkAt != 1 {
		t.Fatalf("linkAt after 2 tabs = %d", nv.linkAt)
	}
	_, cmd := nv.Update(enterMsg())
	if cmd == nil || !nv.Busy() {
		t.Fatal("enter on a focused link must fetch the note")
	}
	out := nv.View(100, 30)
	if !strings.Contains(out, "link 2/3") {
		t.Fatalf("hint missing link position:\n%s", out)
	}
}

func TestNoteViewRendersLinksAndWraps(t *testing.T) {
	note := fixtureNote()
	note.Markdown = "one " + strings.Repeat("word ", 60) + "\n[[Target]]"
	v := newNoteView(nil, note)
	out := stripANSI(v.View(50, 40))
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, " tab links") {
			continue // hint bar is chrome, not note content
		}
		if lipgloss.Width(line) > 52 {
			t.Fatalf("unwrapped line: %q", line)
		}
	}
	if !strings.Contains(out, "[[Target]]") {
		t.Fatalf("link not rendered:\n%s", out)
	}
}

func TestAgentContextPromptFramesAndTruncates(t *testing.T) {
	note := fixtureNote()
	prompt := agentContextPrompt(note, "https://host/notes")
	if !strings.Contains(prompt, `note "Garden"`) || !strings.Contains(prompt, "https://host/notes/Projects/Garden") {
		t.Fatalf("prompt framing:\n%s", prompt[:120])
	}
	if !strings.Contains(prompt, "# Garden") {
		t.Fatal("note body missing")
	}
	note.Markdown = strings.Repeat("x", maxAgentContextBytes+100)
	long := agentContextPrompt(note, "https://host/notes")
	if len(long) > maxAgentContextBytes+300 || !strings.Contains(long, "[truncated") {
		t.Fatalf("truncation failed: len=%d", len(long))
	}
}

func TestNotesViewSearchFlow(t *testing.T) {
	v := newNotesView(nil)
	v.Init()
	if !v.Capturing() {
		t.Fatal("notes view should start in the search input")
	}
	for _, r := range "garden" {
		v.Update(keyMsg(string(r)))
	}
	next, cmd := v.Update(enterMsg())
	nv := next.(*notesView)
	if cmd == nil || !nv.Busy() {
		t.Fatal("enter must run the search")
	}
	next, _ = nv.Update(notesFoundMsg{query: "garden", results: []apiclient.SearchResult{
		{Slug: "Projects/Garden", Title: "Garden", Excerpt: "the <b>garden</b> &amp; beds"},
	}})
	nv = next.(*notesView)
	out := nv.View(100, 30)
	if !strings.Contains(out, "Garden") || !strings.Contains(out, "the garden & beds") {
		t.Fatalf("results render:\n%s", out)
	}
	_, cmd = nv.Update(enterMsg())
	if cmd == nil || !nv.Busy() {
		t.Fatal("enter on a result must open the note")
	}
}

func TestNoteViewRendersTasksBlocksAsLiveResults(t *testing.T) {
	note := &apiclient.Note{
		Slug: "Tasks/Dashboard", Title: "Dashboard", Path: "Tasks/Dashboard.md",
		Markdown: "# Dashboard\n\n```tasks\nnot done\ndue before tomorrow\n```\n\nAfter text.\n",
	}
	v := newNoteView(nil, note)
	if len(v.blocks) != 1 {
		t.Fatalf("blocks = %+v", v.blocks)
	}
	if v.blocks[0].query != "not done\ndue before tomorrow" {
		t.Fatalf("query = %q", v.blocks[0].query)
	}
	// Before results arrive the block shows loading, not the raw query.
	out := stripANSI(v.View(100, 30))
	if strings.Contains(out, "not done") || !strings.Contains(out, "loading") {
		t.Fatalf("pre-load render:\n%s", out)
	}
	// Results replace the fence.
	v.Update(noteTasksMsg{slug: "Tasks/Dashboard", block: 0, groups: []apiclient.TaskGroup{{
		Tasks: []tasks.Task{{Slug: "chores", Line: 1, Text: "water the ferns", Status: tasks.StatusOpen, Due: "2026-08-25", Priority: 3, Version: strings.Repeat("a", 64)}},
	}}})
	out = stripANSI(v.View(100, 30))
	if !strings.Contains(out, "1 result") || !strings.Contains(out, "water the ferns") {
		t.Fatalf("post-load render:\n%s", out)
	}
	if strings.Contains(out, "```") || strings.Contains(out, "due before tomorrow") {
		t.Fatalf("raw fence leaked:\n%s", out)
	}
	if !strings.Contains(out, "After text.") {
		t.Fatalf("content after block missing:\n%s", out)
	}
	// Query errors render inline.
	v.Update(noteTasksMsg{slug: "Tasks/Dashboard", block: 0, err: "unknown instruction"})
	out = stripANSI(v.View(100, 30))
	if !strings.Contains(out, "unknown instruction") {
		t.Fatalf("error render:\n%s", out)
	}
}

// A daily note opened from the notes browser: checkbox lines are
// actionable there, not just rendered text.
func fixtureTaskNote() *apiclient.Note {
	return &apiclient.Note{
		Slug:     "Daily Notes/2026-09-25",
		Title:    "2026-09-25",
		Path:     "Daily Notes/2026-09-25.md",
		Version:  strings.Repeat("a", 64),
		Markdown: "# Today\n\n- [ ] water the plants\n- [x] feed the cat\n\nsome prose\n\n- [ ] call the vet\n",
	}
}

func TestNoteViewParsesTasksWithTheNoteVersionLock(t *testing.T) {
	v := newNoteView(nil, fixtureTaskNote())
	if len(v.tasks) != 3 {
		t.Fatalf("tasks = %+v", v.tasks)
	}
	if v.tasks[0].Text != "water the plants" || v.tasks[0].Status != tasks.StatusOpen {
		t.Fatalf("task0 = %+v", v.tasks[0])
	}
	if v.tasks[1].Status != tasks.StatusDone {
		t.Fatalf("task1 status = %q", v.tasks[1].Status)
	}
	// Line numbers are 1-based and must address the real file line.
	if v.tasks[2].Line != 8 {
		t.Fatalf("task2 line = %d, want 8", v.tasks[2].Line)
	}
	for i, task := range v.tasks {
		if task.Version != strings.Repeat("a", 64) {
			t.Fatalf("task %d carries version %q, not the note's", i, task.Version)
		}
		if task.Slug != "Daily Notes/2026-09-25" {
			t.Fatalf("task %d slug = %q", i, task.Slug)
		}
	}
	if v.taskAt != -1 {
		t.Fatalf("taskAt = %d, want no focus on open", v.taskAt)
	}
}

func TestNoteViewTGrabsTasksAndJKWalkThem(t *testing.T) {
	v := newNoteView(nil, fixtureTaskNote())
	v.Update(keyMsg("t"))
	if v.taskAt != 0 {
		t.Fatalf("t should grab the first visible task, got %d", v.taskAt)
	}
	// While tasks are held, j/k step between them instead of scrolling.
	before := v.offset
	v.Update(keyMsg("j"))
	if v.taskAt != 1 {
		t.Fatalf("j should move to the next task, got %d", v.taskAt)
	}
	if v.offset != before {
		t.Fatalf("j scrolled the body (%d → %d) instead of moving the task cursor", before, v.offset)
	}
	v.Update(keyMsg("k"))
	if v.taskAt != 0 {
		t.Fatalf("k should step back, got %d", v.taskAt)
	}
	// j stops at the last task rather than wrapping into nothing.
	v.Update(keyMsg("j"))
	v.Update(keyMsg("j"))
	v.Update(keyMsg("j"))
	if v.taskAt != 2 {
		t.Fatalf("taskAt = %d, want it clamped at the last task", v.taskAt)
	}
	// t again releases them, so j/k scroll normally.
	v.Update(keyMsg("t"))
	if v.taskAt != -1 {
		t.Fatalf("second t should release the tasks, got %d", v.taskAt)
	}
	before = v.offset
	v.Update(keyMsg("j"))
	if v.offset == before {
		t.Fatal("j should scroll again once tasks are released")
	}
}

// t grabs the task you are looking at, not the top of a long note.
func TestNoteViewTGrabsFromTheCurrentScrollPosition(t *testing.T) {
	v := newNoteView(nil, fixtureTaskNote())
	v.offset = 5 // past the first two tasks
	v.Update(keyMsg("t"))
	if v.taskAt != 2 {
		t.Fatalf("taskAt = %d, want the task below the viewport top", v.taskAt)
	}
}

func TestNoteViewDeleteOpensConfirmForFocusedTask(t *testing.T) {
	v := newNoteView(nil, fixtureTaskNote())
	// Without a focused task the key explains itself instead of doing nothing.
	_, cmd := v.Update(keyMsg("D"))
	if v.del.active {
		t.Fatal("D opened a confirm with no task focused")
	}
	if cmd == nil {
		t.Fatal("D with no focus should flash a hint")
	}
	if msg, ok := cmd().(flashMsg); !ok || !strings.Contains(msg.text, "press t") {
		t.Fatalf("hint = %#v", cmd())
	}

	v.Update(keyMsg("t"))
	v.Update(keyMsg("D"))
	if !v.del.active || !v.Capturing() {
		t.Fatalf("D should open the confirm and capture keys (active=%v)", v.del.active)
	}
	if v.delTarget.Text != "water the plants" {
		t.Fatalf("delTarget = %+v", v.delTarget)
	}
	if !strings.Contains(v.del.bar(), "cancel task") {
		t.Fatalf("confirm bar = %q", v.del.bar())
	}
}

func TestNoteViewDeleteOfCancelledTaskPurges(t *testing.T) {
	note := fixtureTaskNote()
	note.Markdown = "# Today\n\n- [-] abandoned thing ❌ 2026-09-01\n"
	v := newNoteView(nil, note)
	if len(v.tasks) != 1 || v.tasks[0].Status != tasks.StatusCancelled {
		t.Fatalf("tasks = %+v", v.tasks)
	}
	v.Update(keyMsg("t"))
	v.Update(keyMsg("D"))
	if !strings.Contains(v.del.bar(), "permanently delete") {
		t.Fatalf("cancelled task should confirm a permanent delete: %q", v.del.bar())
	}
}

func TestNoteViewSpaceTogglesFocusedTaskOnly(t *testing.T) {
	v := newNoteView(nil, fixtureTaskNote())
	_, cmd := v.Update(keyMsg(" "))
	if cmd == nil {
		t.Fatal("space with no focus should flash a hint")
	}
	if msg, ok := cmd().(flashMsg); !ok || !strings.Contains(msg.text, "press t") {
		t.Fatalf("hint = %#v", cmd())
	}
	if v.busy {
		t.Fatal("space with no focus started a request")
	}
}

func TestNoteViewHighlightsTheFocusedTaskLine(t *testing.T) {
	v := newNoteView(nil, fixtureTaskNote())
	v.Update(keyMsg("t"))
	out := v.View(80, 24)
	if !strings.Contains(stripANSI(out), "water the plants") {
		t.Fatalf("focused task line missing:\n%s", out)
	}
	if hint := stripANSI(out); !strings.Contains(hint, "space toggle") || !strings.Contains(hint, "D delete") {
		t.Fatalf("hint does not advertise the task actions:\n%s", hint)
	}
}

// A note with no checkboxes must not pretend to have task actions.
func TestNoteViewWithoutTasksSaysSo(t *testing.T) {
	v := newNoteView(nil, fixtureNote())
	_, cmd := v.Update(keyMsg("t"))
	if v.taskAt != -1 {
		t.Fatalf("taskAt = %d", v.taskAt)
	}
	if msg, ok := cmd().(flashMsg); !ok || !strings.Contains(msg.text, "no tasks") {
		t.Fatalf("flash = %#v", cmd())
	}
}
