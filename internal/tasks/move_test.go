package tasks

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMoveTaskToBoardReplacesOpenCheckboxWithReferenceAfterCardCreation(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "Project.md")
	original := []byte("# Project\n\n  - [ ] Ship release 📅 2026-08-20 #launch\n")
	if err := os.WriteFile(path, original, 0o640); err != nil {
		t.Fatal(err)
	}
	created := 0
	var received Task
	err := MoveTaskToBoardInFileAndReindex(root, "Project.md", MoveTaskInput{
		Line: 3, ExpectedText: "Ship release", ExpectedStatus: StatusOpen, ExpectedVersion: FileVersion(original),
	}, func(task Task) (CreatedTaskReference, error) {
		created++
		received = task
		return CreatedTaskReference{Reference: "[[kanban/project/board#^card-abc123|Ship release (abc123)]]"}, nil
	}, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if created != 1 || received.Text != "Ship release" || received.Due != "2026-08-20" || received.Status != StatusOpen {
		t.Fatalf("created=%d task=%#v", created, received)
	}
	got, _ := os.ReadFile(path)
	want := "# Project\n\n  - Ship release 📅 2026-08-20 #launch → [[kanban/project/board#^card-abc123|Ship release (abc123)]]\n"
	if string(got) != want {
		t.Fatalf("source=%q want=%q", got, want)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("mode=%v", info.Mode().Perm())
	}
	if err := MoveTaskToBoardInFileAndReindex(root, "Project.md", MoveTaskInput{
		Line: 3, ExpectedText: "Ship release", ExpectedStatus: StatusOpen, ExpectedVersion: FileVersion(original),
	}, func(Task) (CreatedTaskReference, error) {
		created++
		return CreatedTaskReference{}, nil
	}, nil); err == nil {
		t.Fatal("retry unexpectedly succeeded")
	}
	if created != 1 {
		t.Fatalf("retry created duplicate card: %d", created)
	}
}

func TestMoveTaskToBoardValidatesVersionTextAndOpenStatusBeforeCreatingCard(t *testing.T) {
	root := t.TempDir()
	original := []byte("- [x] Finished\n")
	if err := os.WriteFile(filepath.Join(root, "Project.md"), original, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, input := range []MoveTaskInput{
		{Line: 1, ExpectedText: "Finished", ExpectedStatus: StatusOpen, ExpectedVersion: FileVersion(original)},
		{Line: 1, ExpectedText: "Wrong", ExpectedStatus: StatusDone, ExpectedVersion: FileVersion(original)},
		{Line: 1, ExpectedText: "Finished", ExpectedStatus: StatusDone, ExpectedVersion: strings.Repeat("a", 64)},
	} {
		created := false
		err := MoveTaskToBoardInFileAndReindex(root, "Project.md", input, func(Task) (CreatedTaskReference, error) {
			created = true
			return CreatedTaskReference{}, nil
		}, nil)
		if err == nil || created {
			t.Fatalf("input=%#v err=%v created=%v", input, err, created)
		}
	}
}

func TestMoveTaskToBoardKeepsCardWhenSourceCommitReportsCleanupError(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "Project.md")
	original := []byte("- [ ] Keep safe\n")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	oldReplace := replaceTaskFileForMove
	replaceTaskFileForMove = func(parentFD int, leaf string, expected, updated []byte, mode os.FileMode) (bool, error) {
		if err := replaceTaskFileAt(parentFD, leaf, expected, updated, mode); err != nil {
			return false, err
		}
		return true, errors.New("cleanup displaced source")
	}
	defer func() { replaceTaskFileForMove = oldReplace }()
	rolledBack := 0
	err := MoveTaskToBoardInFileAndReindex(root, "Project.md", MoveTaskInput{
		Line: 1, ExpectedText: "Keep safe", ExpectedStatus: StatusOpen, ExpectedVersion: FileVersion(original),
	}, func(Task) (CreatedTaskReference, error) {
		return CreatedTaskReference{
			Reference: "[[kanban/project/board#^card-safe|Keep safe (safe)]]",
			Rollback:  func() error { rolledBack++; return nil },
		}, nil
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "cleanup displaced source") {
		t.Fatalf("error=%v", err)
	}
	if rolledBack != 0 {
		t.Fatalf("committed source caused card deletion: rollback calls=%d", rolledBack)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), "^card-safe") || strings.Contains(string(got), "- [ ]") {
		t.Fatalf("committed reference was not preserved: %q", got)
	}
}

func TestMoveTaskToBoardReappliesReferenceWhenCardRollbackFails(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "Project.md")
	original := []byte("- [ ] Keep safe\n")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	created := 0
	rollbackErr := errors.New("card delete failed")
	move := func() error {
		return MoveTaskToBoardInFileAndReindex(root, "Project.md", MoveTaskInput{
			Line: 1, ExpectedText: "Keep safe", ExpectedStatus: StatusOpen, ExpectedVersion: FileVersion(original),
		}, func(Task) (CreatedTaskReference, error) {
			created++
			return CreatedTaskReference{
				Reference: "[[kanban/project/board#^card-safe|Keep safe (safe)]]",
				Rollback:  func() error { return rollbackErr },
			}, nil
		}, func() error { return errors.New("index unavailable") })
	}
	if err := move(); !errors.Is(err, rollbackErr) {
		t.Fatalf("error=%v", err)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), "^card-safe") || strings.Contains(string(got), "- [ ]") {
		t.Fatalf("failed card rollback reopened a duplicateable task: %q", got)
	}
	if err := move(); err == nil {
		t.Fatal("retry unexpectedly succeeded")
	}
	if created != 1 {
		t.Fatalf("retry created duplicate card: %d", created)
	}
}

func TestMoveTaskToBoardRollsBackCardAndSourceWhenReindexFails(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "Project.md")
	original := []byte("- [ ] Keep safe\n")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	rolledBack := 0
	reindexErr := errors.New("index unavailable")
	err := MoveTaskToBoardInFileAndReindex(root, "Project.md", MoveTaskInput{
		Line: 1, ExpectedText: "Keep safe", ExpectedStatus: StatusOpen, ExpectedVersion: FileVersion(original),
	}, func(Task) (CreatedTaskReference, error) {
		return CreatedTaskReference{
			Reference: "[[kanban/project/board#^card-safe|Keep safe (safe)]]",
			Rollback:  func() error { rolledBack++; return nil },
		}, nil
	}, func() error { return reindexErr })
	if !errors.Is(err, reindexErr) {
		t.Fatalf("error=%v", err)
	}
	if rolledBack != 1 {
		t.Fatalf("rollback calls=%d", rolledBack)
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(original) {
		t.Fatalf("source task was lost: %q", got)
	}
}
