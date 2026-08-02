package tasks

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

func TestTriageLineBacklogTagIsCaseInsensitive(t *testing.T) {
	body := []byte("- [ ] Investigate options #Backlog\n")

	out, err := TriageLine(body, 1, TriageMutation{
		Action: TriageBacklog, ExpectedText: "Investigate options #Backlog",
	}, triageNow)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(body) {
		t.Fatalf("duplicate backlog tag appended: %q", out)
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

func TestTriageInFileRejectsPathOutsideVault(t *testing.T) {
	parent := tempVaultDir(t)
	root := filepath.Join(parent, "vault")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(parent, "outside.md")
	original := "- [ ] Keep safe\n"
	if err := os.WriteFile(outside, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	err := TriageInFile(root, "../outside.md", 1, TriageMutation{
		Action: TriageBacklog, ExpectedText: "Keep safe",
	}, triageNow)
	if err == nil {
		t.Fatal("expected traversal outside the vault to be rejected")
	}
	got, readErr := os.ReadFile(outside)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != original {
		t.Fatalf("outside file changed: %q", got)
	}
}

func TestTriageInFileRejectsNoteSymlinkOutsideVault(t *testing.T) {
	parent := tempVaultDir(t)
	root := filepath.Join(parent, "vault")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(parent, "outside.md")
	original := "- [ ] Keep safe\n"
	if err := os.WriteFile(outside, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked.md")); err != nil {
		t.Fatal(err)
	}

	err := TriageInFile(root, "linked.md", 1, TriageMutation{
		Action: TriageBacklog, ExpectedText: "Keep safe",
	}, triageNow)
	if err == nil {
		t.Fatal("expected a note symlink outside the vault to be rejected")
	}
	got, readErr := os.ReadFile(outside)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != original {
		t.Fatalf("outside symlink target changed: %q", got)
	}
}

func TestTriageInFileDoesNotFollowPredictableTempSymlink(t *testing.T) {
	root := tempVaultDir(t)
	note := filepath.Join(root, "Project.md")
	victim := filepath.Join(root, "victim.txt")
	if err := os.WriteFile(note, []byte("- [ ] Ship it\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(victim, []byte("do not overwrite"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, note+".amythest-tmp"); err != nil {
		t.Fatal(err)
	}

	err := TriageInFile(root, "Project.md", 1, TriageMutation{
		Action: TriageBacklog, ExpectedText: "Ship it",
	}, triageNow)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(victim)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "do not overwrite" {
		t.Fatalf("predictable temp symlink target was overwritten: %q", got)
	}
}

func TestTriageInFilePreservesFrontmatter(t *testing.T) {
	root := tempVaultDir(t)
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

func TestTriageBatchBodyProcessesFiveThousandItemsInOnePass(t *testing.T) {
	var body strings.Builder
	items := make([]TriageItem, 0, 5_000)
	for i := 1; i <= 5_000; i++ {
		text := fmt.Sprintf("Item %d", i)
		body.WriteString("- [ ] " + text + "\n")
		items = append(items, TriageItem{Line: i, Mutation: TriageMutation{Action: TriageReference, ExpectedText: text}})
	}
	started := time.Now()
	out, err := triageBatchBody([]byte(body.String()), items, triageNow)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "[ ]") {
		t.Fatal("batch left task checkboxes behind")
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("5000-item batch took %s; expected linear processing", elapsed)
	}
}

func TestTriageBatchInFileAppliesOneAtomicFileDecision(t *testing.T) {
	root := tempVaultDir(t)
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
