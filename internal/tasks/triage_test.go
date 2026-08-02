package tasks

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

var triageNow = time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)

func TestTriageLineMarksIntentionalBacklog(t *testing.T) {
	body := []byte("# Project\n- [ ] Investigate options\n")

	out, err := TriageLine(body, 2, TriageMutation{
		Action:       TriageBacklog,
		ExpectedText: "Investigate options",
	}, triageNow)
	if err != nil {
		t.Fatal(err)
	}
	want := "# Project\n- [ ] Investigate options #backlog\n"
	if string(out) != want {
		t.Fatalf("out = %q, want %q", out, want)
	}
}

func TestTriageLineSetsDueDate(t *testing.T) {
	body := []byte("- [ ] Send application ⏫\n")

	out, err := TriageLine(body, 1, TriageMutation{
		Action:       TriageDue,
		ExpectedText: "Send application",
		Due:          "2026-08-15",
	}, triageNow)
	if err != nil {
		t.Fatal(err)
	}
	want := "- [ ] Send application ⏫ 📅 2026-08-15\n"
	if string(out) != want {
		t.Fatalf("out = %q, want %q", out, want)
	}
}

func TestTriageLineConvertsReferenceChecklistItem(t *testing.T) {
	body := []byte("## Packing\n  * [ ] Passport copy\n")

	out, err := TriageLine(body, 2, TriageMutation{
		Action:       TriageReference,
		ExpectedText: "Passport copy",
	}, triageNow)
	if err != nil {
		t.Fatal(err)
	}
	want := "## Packing\n  * Passport copy\n"
	if string(out) != want {
		t.Fatalf("out = %q, want %q", out, want)
	}
}

func TestTriageLineCancelsObsoleteTask(t *testing.T) {
	body := []byte("- [ ] Replace retired service\n")

	out, err := TriageLine(body, 1, TriageMutation{
		Action:       TriageCancel,
		ExpectedText: "Replace retired service",
	}, triageNow)
	if err != nil {
		t.Fatal(err)
	}
	want := "- [-] Replace retired service ❌ 2026-08-02\n"
	if string(out) != want {
		t.Fatalf("out = %q, want %q", out, want)
	}
}

func TestTriageLineRejectsTaskThatGainedDueDate(t *testing.T) {
	body := []byte("- [ ] Investigate options 📅 2026-08-15\n")

	_, err := TriageLine(body, 1, TriageMutation{
		Action:       TriageBacklog,
		ExpectedText: "Investigate options",
	}, triageNow)
	if err == nil {
		t.Fatal("expected no-date triage to reject a task that gained a due date")
	}
}

func TestTriageInFilePreservesFrontmatter(t *testing.T) {
	root := t.TempDir()
	rel := "Project.md"
	original := "---\ntitle: Project\n---\n\n- [ ] Ship prototype\n"
	if err := os.WriteFile(filepath.Join(root, rel), []byte(original), 0o640); err != nil {
		t.Fatal(err)
	}

	err := TriageInFile(root, rel, 2, TriageMutation{
		Action:       TriageBacklog,
		ExpectedText: "Ship prototype",
	}, triageNow)
	if err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	want := "---\ntitle: Project\n---\n\n- [ ] Ship prototype #backlog\n"
	if string(out) != want {
		t.Fatalf("out = %q, want %q", out, want)
	}
}

func TestTriageBatchInFileAppliesOneAtomicFileDecision(t *testing.T) {
	root := t.TempDir()
	rel := "Checklist.md"
	original := "# Checklist\n- [ ] First item\n- [ ] Second item\n"
	if err := os.WriteFile(filepath.Join(root, rel), []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	err := TriageBatchInFile(root, rel, []TriageItem{
		{Line: 2, Mutation: TriageMutation{Action: TriageReference, ExpectedText: "First item"}},
		{Line: 3, Mutation: TriageMutation{Action: TriageReference, ExpectedText: "Second item"}},
	}, triageNow)
	if err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	want := "# Checklist\n- First item\n- Second item\n"
	if string(out) != want {
		t.Fatalf("out = %q, want %q", out, want)
	}
}
