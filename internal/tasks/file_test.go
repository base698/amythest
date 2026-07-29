package tasks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ToggleInFile is the only write path into the vault — shared by the web UI's
// HTTP handler and the MCP toggle_task tool — so it is covered directly rather
// than only through ToggleLine.
func TestToggleInFileCompletesAndRecurs(t *testing.T) {
	root := t.TempDir()
	rel := "Chores.md"
	original := "# Chores\n\n- [ ] Water the plants 🔁 every week 📅 2026-07-01\n- [ ] Buy milk\n"
	if err := os.WriteFile(filepath.Join(root, rel), []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 7, 29, 9, 0, 0, 0, time.UTC)
	recurred, err := ToggleInFile(root, rel, 3, true, now)
	if err != nil {
		t.Fatalf("toggle: %v", err)
	}
	if !recurred {
		t.Fatal("a 🔁 task must report recurred")
	}

	out, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)

	if !strings.Contains(got, "[x] Water the plants") {
		t.Errorf("task was not completed:\n%s", got)
	}
	if !strings.Contains(got, "✅ 2026-07-29") {
		t.Errorf("missing done-date:\n%s", got)
	}
	// The next occurrence is inserted unchecked, so the task never disappears
	// from the vault just because this week's was ticked off.
	if strings.Count(got, "Water the plants") != 2 {
		t.Errorf("expected the next occurrence to be inserted:\n%s", got)
	}
	if !strings.HasPrefix(got, "# Chores\n") {
		t.Errorf("content above the task was disturbed:\n%s", got)
	}
	if !strings.Contains(got, "- [ ] Buy milk") {
		t.Errorf("an unrelated task was modified:\n%s", got)
	}
	if strings.Contains(got, ".amythest-tmp") {
		t.Error("temp file leaked into the note")
	}
}

// Line numbers are BODY-relative, not file-relative: the indexer parses
// v.ReadBody (frontmatter stripped) and ToggleInFile strips it the same way, so
// a line from query_tasks addresses the same task here. If the two ever
// disagreed, completing a task in any note with frontmatter would silently tick
// a different one — this pins the contract.
func TestToggleInFileLinesAreBodyRelative(t *testing.T) {
	root := t.TempDir()
	rel := "note.md"
	original := "---\ntitle: Example\ntags: [a]\n---\n\n- [ ] First\n- [ ] Second\n"
	if err := os.WriteFile(filepath.Join(root, rel), []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	// Body is "\n- [ ] First\n- [ ] Second\n" — so "Second" is body line 3,
	// which is file line 7. Addressing it by the file line would hit nothing.
	if _, err := ToggleInFile(root, rel, 3, true, time.Now()); err != nil {
		t.Fatalf("toggle: %v", err)
	}

	out, _ := os.ReadFile(filepath.Join(root, rel))
	got := string(out)
	if !strings.HasPrefix(got, "---\ntitle: Example\ntags: [a]\n---\n") {
		t.Errorf("frontmatter was altered:\n%s", got)
	}
	if !strings.Contains(got, "- [ ] First") {
		t.Errorf("wrong task toggled:\n%s", got)
	}
	if !strings.Contains(got, "- [x] Second") {
		t.Errorf("target task not completed:\n%s", got)
	}
}

func TestToggleInFileRejectsNonTaskLine(t *testing.T) {
	root := t.TempDir()
	rel := "note.md"
	if err := os.WriteFile(filepath.Join(root, rel),
		[]byte("just prose\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(root, rel))

	if _, err := ToggleInFile(root, rel, 1, true, time.Now()); err == nil {
		t.Fatal("expected an error for a non-task line")
	}
	after, _ := os.ReadFile(filepath.Join(root, rel))
	if string(before) != string(after) {
		t.Error("file must be left untouched when the toggle is rejected")
	}
}
