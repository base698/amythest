package tasks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var cancelNow = time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)

func TestCancelTasksMarksOpenTasksCancelledWithDate(t *testing.T) {
	root := tempVaultDir(t)
	src := []byte("---\ntitle: P\n---\n# P\n- [ ] Ship it 📅 2026-08-15\n- [ ] Keep me\n")
	path := filepath.Join(root, "P.md")
	if err := os.WriteFile(path, src, 0o644); err != nil {
		t.Fatal(err)
	}
	err := CancelTasksInFileAndReindex(root, "P.md",
		[]CancelItem{{Line: 2, ExpectedText: "Ship it"}}, FileVersion(src), cancelNow, nil)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(path)
	if !strings.Contains(string(out), "- [-] Ship it 📅 2026-08-15 ❌ 2026-08-02") {
		t.Fatalf("out=%q", out)
	}
	if !strings.Contains(string(out), "- [ ] Keep me") {
		t.Fatalf("untouched task mutated: %q", out)
	}
}

func TestCancelTasksRejectsStaleVersionAndChangedText(t *testing.T) {
	root := tempVaultDir(t)
	src := []byte("- [ ] Task\n")
	path := filepath.Join(root, "P.md")
	if err := os.WriteFile(path, src, 0o644); err != nil {
		t.Fatal(err)
	}
	stale := FileVersion([]byte("something else"))
	if err := CancelTasksInFileAndReindex(root, "P.md",
		[]CancelItem{{Line: 1, ExpectedText: "Task"}}, stale, cancelNow, nil); err == nil {
		t.Fatal("stale version accepted")
	}
	if err := CancelTasksInFileAndReindex(root, "P.md",
		[]CancelItem{{Line: 1, ExpectedText: "Other"}}, FileVersion(src), cancelNow, nil); err == nil {
		t.Fatal("mismatched text accepted")
	}
	if out, _ := os.ReadFile(path); string(out) != string(src) {
		t.Fatalf("file mutated by rejected requests: %q", out)
	}
}

func TestCancelTasksRejectsDoneAndCancelledTasks(t *testing.T) {
	root := tempVaultDir(t)
	src := []byte("- [x] Done ✅ 2026-08-01\n- [-] Gone ❌ 2026-08-01\n")
	if err := os.WriteFile(filepath.Join(root, "P.md"), src, 0o644); err != nil {
		t.Fatal(err)
	}
	for line, text := range map[int]string{1: "Done", 2: "Gone"} {
		if err := CancelTasksInFileAndReindex(root, "P.md",
			[]CancelItem{{Line: line, ExpectedText: text}}, FileVersion(src), cancelNow, nil); err == nil {
			t.Fatalf("non-open task on line %d accepted", line)
		}
	}
}

func TestCancelTasksBatchIsAllOrNothing(t *testing.T) {
	root := tempVaultDir(t)
	src := []byte("- [ ] First\n- [x] Second ✅ 2026-08-01\n")
	path := filepath.Join(root, "P.md")
	if err := os.WriteFile(path, src, 0o644); err != nil {
		t.Fatal(err)
	}
	err := CancelTasksInFileAndReindex(root, "P.md", []CancelItem{
		{Line: 1, ExpectedText: "First"},
		{Line: 2, ExpectedText: "Second"}, // done → must fail the whole batch
	}, FileVersion(src), cancelNow, nil)
	if err == nil {
		t.Fatal("batch with a non-open task accepted")
	}
	if out, _ := os.ReadFile(path); string(out) != string(src) {
		t.Fatalf("partial batch applied: %q", out)
	}
}
