package markdown_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/base698/amythest/internal/markdown"
	"github.com/base698/amythest/internal/vault"
)

func TestNoteTasksCarryInlineActions(t *testing.T) {
	root := t.TempDir()
	src := "- [ ] Ship it 📅 2026-08-15\n- [-] Old idea ❌ 2026-08-01\n- [x] Done ✅ 2026-08-01\n"
	if err := os.WriteFile(filepath.Join(root, "Task.md"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	v, err := vault.Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	res, err := markdown.New("/").Render(v, v.Notes[0])
	if err != nil {
		t.Fatal(err)
	}
	html := string(res.HTML)
	// Only open (and done) boxes are goldmark checkboxes; cancelled [-] lines
	// render as plain text, so purge stays a /tasks-page affordance.
	for _, want := range []string{
		`data-task-due-editor`,           // due editor on the open task
		`data-expected-due="2026-08-15"`, // carrying the rendered due date
		`data-task-cancel`,               // cancel on the open task
		`data-expected-version="` + v.Notes[0].Hash + `"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("note task actions missing %s in:\n%s", want, html)
		}
	}
	if strings.Count(html, "data-task-cancel") != 1 {
		t.Fatalf("cancel must appear only on the open task:\n%s", html)
	}

	// A note line prints its own metadata, so the controls must not echo it:
	// no value badges, just one neutral disclosure per task.
	for _, banned := range []string{`class="task-prio"`, `class="task-date task-due"`} {
		if strings.Contains(html, banned) {
			t.Fatalf("note view duplicates metadata already in the line (%s):\n%s", banned, html)
		}
	}
	if got := strings.Count(html, `class="task-actions-toggle"`); got != 2 {
		t.Fatalf("expected one actions disclosure per task line, got %d:\n%s", got, html)
	}
}

func TestNoteTaskActionsDoNotRepeatDueDateOrPriority(t *testing.T) {
	root := t.TempDir()
	// Both metadata kinds present: the line renders "🔺" and "📅 2026-08-15"
	// itself, so neither may be repeated by the control summary.
	if err := os.WriteFile(filepath.Join(root, "T.md"), []byte("- [ ] Ship it 🔺 📅 2026-08-15\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	v, err := vault.Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	res, err := markdown.New("/").Render(v, v.Notes[0])
	if err != nil {
		t.Fatal(err)
	}
	html := string(res.HTML)
	summary := html[strings.Index(html, "<summary"):]
	summary = summary[:strings.Index(summary, "</summary>")]
	if !strings.Contains(summary, "⋯") {
		t.Fatalf("control summary should be neutral, got %q", summary)
	}
	if strings.Contains(summary, "🔺") || strings.Contains(summary, "2026-08-15") {
		t.Fatalf("summary repeats metadata already in the line: %q", summary)
	}
	// "Due date" must not be printed as a visible label next to the summary.
	if strings.Contains(html, "<span>Due date</span>") {
		t.Fatalf("redundant visible due-date label:\n%s", html)
	}
}

func TestTranscludedTasksStayToggleOnly(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Inner.md"), []byte("- [ ] Embedded task\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Outer.md"), []byte("![[Inner]]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	v, err := vault.Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	var outer *vault.Note
	for _, n := range v.Notes {
		if n.Slug == "Outer" {
			outer = n
		}
	}
	if outer == nil {
		t.Fatal("Outer note not scanned")
	}
	res, err := markdown.New("/").Render(v, outer)
	if err != nil {
		t.Fatal(err)
	}
	html := string(res.HTML)
	if !strings.Contains(html, `class="task-toggle"`) {
		t.Fatalf("transcluded checkbox lost its toggle:\n%s", html)
	}
	if strings.Contains(html, "data-task-cancel") || strings.Contains(html, "data-task-due-editor") {
		t.Fatalf("transcluded tasks must not grow inline actions:\n%s", html)
	}
}
