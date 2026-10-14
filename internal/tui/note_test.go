package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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

func TestNoteViewPropertiesExpandAndShowTypedFalse(t *testing.T) {
	note := fixtureNote()
	note.Properties = []apiclient.NoteProperty{
		{Key: "status", Type: "string", Value: "active"},
		{Key: "tasks", Type: "bool", Value: false},
	}
	v := newNoteView(nil, note)
	collapsed := stripANSI(v.View(100, 30))
	if !strings.Contains(collapsed, "Properties (2)") || strings.Contains(collapsed, "tasks  false") {
		t.Fatalf("collapsed properties block:\n%s", collapsed)
	}
	v.Update(keyMsg("p"))
	expanded := stripANSI(v.View(100, 30))
	if !strings.Contains(expanded, "status") || !strings.Contains(expanded, "tasks") || !strings.Contains(expanded, "false") {
		t.Fatalf("expanded properties block:\n%s", expanded)
	}
	if !strings.Contains(expanded, "e edit") || !strings.Contains(expanded, "empty clears") {
		t.Fatalf("property keyboard help missing:\n%s", expanded)
	}
	v.Update(keyMsg("e"))
	if !v.Capturing() {
		t.Fatal("e on an expanded scalar property must open the property editor")
	}
	if editor := stripANSI(v.View(100, 30)); !strings.Contains(editor, "set status") {
		t.Fatalf("property editor missing selected key:\n%s", editor)
	}
}

func TestNoteViewEscapesTerminalControlsInPropertyValues(t *testing.T) {
	note := fixtureNote()
	note.Properties = []apiclient.NoteProperty{{Key: "cap\x1b[32mtion", Type: "string", Value: "safe\x1b[31mred"}}
	v := newNoteView(nil, note)
	v.Update(keyMsg("p"))
	out := v.View(100, 30)
	// Styling adds its own ANSI sequences, so inspect the stripped frame: a
	// frontmatter escape must remain visible text rather than become styling.
	plain := stripANSI(out)
	if !strings.Contains(plain, `cap\x1b[32mtion`) || !strings.Contains(plain, `safe\x1b[31mred`) {
		t.Fatalf("terminal control was not escaped:\n%s", plain)
	}
}

func TestNoteViewClearsFalsePropertyThroughSetNoteProperty(t *testing.T) {
	var got struct {
		Slug            string `json:"slug"`
		Key             string `json:"key"`
		Value           string `json:"value"`
		Type            string `json:"type"`
		ExpectedVersion string `json:"expectedVersion"`
	}
	var gotCSRF string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /kanban/api/login", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "amythest_kanban_session", Value: "token", Path: "/"})
		json.NewEncoder(w).Encode(map[string]string{"user": "test", "csrf": "csrf-token"})
	})
	mux.HandleFunc("POST /api/notes/property", func(w http.ResponseWriter, r *http.Request) {
		gotCSRF = r.Header.Get("X-CSRF-Token")
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	t.Setenv("KANBAN_USERNAME", "test")
	t.Setenv("KANBAN_PASSWORD", "test-password")
	client := apiclient.New(apiclient.Config{Endpoint: server.URL, SessionFile: filepath.Join(t.TempDir(), "session.json")})
	note := fixtureNote()
	note.Version = strings.Repeat("e", 64)
	note.Properties = []apiclient.NoteProperty{{Key: "tasks", Type: "bool", Value: false}}
	v := newNoteView(client, note)
	v.Update(keyMsg("p"))
	v.Update(keyMsg("e"))
	v.propInput.SetValue("")
	_, cmd := v.Update(enterMsg())
	if cmd == nil {
		t.Fatal("clearing a property must issue a request")
	}
	if msg, ok := cmd().(notePropertySavedMsg); !ok || msg.slug != note.Slug {
		t.Fatalf("save result = %#v", msg)
	}
	if got.Slug != note.Slug || got.Key != "tasks" || got.Value != "" || got.Type != "bool" || got.ExpectedVersion != note.Version {
		t.Fatalf("property payload = %#v", got)
	}
	if gotCSRF != "csrf-token" {
		t.Fatalf("property CSRF = %q", gotCSRF)
	}
}

func TestNoteViewBusyGatesPropertyKeys(t *testing.T) {
	note := fixtureNote()
	note.Properties = []apiclient.NoteProperty{
		{Key: "alpha", Type: "string", Value: "one"},
		{Key: "beta", Type: "string", Value: "two"},
	}
	v := newNoteView(nil, note)
	v.propsOpen = true
	v.busy = true

	v.Update(keyMsg("j"))
	v.Update(keyMsg("e"))
	v.Update(keyMsg("p"))
	if v.propAt != 0 || v.propping || !v.propsOpen {
		t.Fatalf("busy property state changed: at=%d propping=%v open=%v", v.propAt, v.propping, v.propsOpen)
	}
}

func TestNoteViewReloadRebindsOpenPropertyEditorByKey(t *testing.T) {
	note := fixtureNote()
	note.Version = strings.Repeat("a", 64)
	note.Properties = []apiclient.NoteProperty{
		{Key: "status", Type: "string", Value: "active"},
		{Key: "zeta", Type: "string", Value: "last"},
	}
	v := newNoteView(nil, note)
	v.propsOpen = true
	v.propAt = 0
	v.openPropertyEditor()

	reloaded := fixtureNote()
	reloaded.Version = strings.Repeat("b", 64)
	reloaded.Properties = []apiclient.NoteProperty{
		{Key: "alpha", Type: "string", Value: "first"},
		{Key: "status", Type: "string", Value: "active"},
	}
	v.Update(noteReloadedMsg{note: reloaded})
	if !v.propping || v.propAt != 1 || v.note.Properties[v.propAt].Key != "status" {
		t.Fatalf("editor was not rebound: propping=%v at=%d properties=%#v", v.propping, v.propAt, v.note.Properties)
	}
	if v.propVersion != note.Version {
		t.Fatalf("editor version changed from %q to %q during reload", note.Version, v.propVersion)
	}
}

func TestNoteViewReloadCancelsEditorWhenPropertyWasRemoved(t *testing.T) {
	note := fixtureNote()
	note.Properties = []apiclient.NoteProperty{{Key: "status", Type: "string", Value: "active"}}
	v := newNoteView(nil, note)
	v.propsOpen = true
	v.openPropertyEditor()

	reloaded := fixtureNote()
	reloaded.Version = strings.Repeat("c", 64)
	v.Update(noteReloadedMsg{note: reloaded})
	if v.propping || v.Capturing() || v.propsOpen {
		t.Fatalf("removed property left editor active: propping=%v open=%v", v.propping, v.propsOpen)
	}
	// A queued enter key from the old editor must be harmless, not index an
	// empty properties slice.
	if _, cmd := v.Update(enterMsg()); cmd != nil {
		t.Fatalf("queued enter produced command %#v", cmd)
	}
}

func TestNoteViewWithoutFrontmatterHasNoPropertyAffordance(t *testing.T) {
	v := newNoteView(nil, fixtureNote())
	before := stripANSI(v.View(100, 30))
	v.Update(keyMsg("p"))
	after := stripANSI(v.View(100, 30))
	if strings.Contains(after, "Properties") || strings.Contains(after, "p properties") {
		t.Fatalf("empty frontmatter exposed a property affordance:\n%s", after)
	}
	if before != after {
		t.Fatal("p should be a no-op when the note has no frontmatter")
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
