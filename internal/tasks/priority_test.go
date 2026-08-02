package tasks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdatePriorityLinePlacesMarkerAfterDescription(t *testing.T) {
	for _, tc := range []struct {
		name             string
		line             string
		expectedPriority int
		priority         int
		want             string
	}{
		{"add to bare task", "- [ ] Ship it", PriorityNone, 1, "- [ ] Ship it ⏫"},
		{"insert before metadata", "- [ ] Ship it 📅 2026-08-15", PriorityNone, 0, "- [ ] Ship it 🔺 📅 2026-08-15"},
		{"replace existing", "- [ ] Ship it 🔼 📅 2026-08-15", 2, 5, "- [ ] Ship it ⏬ 📅 2026-08-15"},
		{"clear to none", "- [ ] Ship it ⏫ 📅 2026-08-15", 1, PriorityNone, "- [ ] Ship it 📅 2026-08-15"},
		{"keeps indentation and bullet", "    * [ ] Nested ⏬", 5, 2, "    * [ ] Nested 🔼"},
		{"preserves trailing tag", "- [ ] Ship it #proj", PriorityNone, 1, "- [ ] Ship it #proj ⏫"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task, ok := ParseLine(tc.line, "P", "P.md", 1, "v")
			if !ok {
				t.Fatalf("fixture is not a task: %q", tc.line)
			}
			out, err := UpdatePriorityLine([]byte(tc.line), 1, task.Text, task.Status, tc.expectedPriority, tc.priority)
			if err != nil {
				t.Fatal(err)
			}
			if string(out) != tc.want {
				t.Fatalf("got  %q\nwant %q", out, tc.want)
			}
			// The rewritten line must parse back to the requested priority.
			round, ok := ParseLine(string(out), "P", "P.md", 1, "v")
			if !ok || round.Priority != tc.priority {
				t.Fatalf("round trip priority = %d (ok=%v) for %q", round.Priority, ok, out)
			}
			if round.Text != task.Text {
				t.Fatalf("description changed: %q -> %q", task.Text, round.Text)
			}
		})
	}
}

func TestUpdatePriorityLineRejectsStaleExpectations(t *testing.T) {
	line := []byte("- [ ] Ship it ⏫")
	if _, err := UpdatePriorityLine(line, 1, "Ship it", "open", PriorityNone, 2); err == nil {
		t.Fatal("stale expectedPriority accepted")
	}
	if _, err := UpdatePriorityLine(line, 1, "Other", "open", 1, 2); err == nil {
		t.Fatal("stale expectedText accepted")
	}
	if _, err := UpdatePriorityLine(line, 1, "Ship it", "open", 1, 9); err == nil {
		t.Fatal("out-of-range priority accepted")
	}
	if _, err := UpdatePriorityLine([]byte("not a task"), 1, "", "open", 3, 1); err == nil {
		t.Fatal("non-task line accepted")
	}
}

func TestUpdatePriorityInFileGuardsFileVersion(t *testing.T) {
	root := tempVaultDir(t)
	src := []byte("---\ntitle: P\n---\n# P\n- [ ] Ship it 📅 2026-08-15\n")
	path := filepath.Join(root, "P.md")
	if err := os.WriteFile(path, src, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := UpdatePriorityInFileAndReindex(root, "P.md", 2, "Ship it", "open",
		PriorityNone, 1, FileVersion([]byte("stale")), nil); err == nil {
		t.Fatal("stale version accepted")
	}
	if out, _ := os.ReadFile(path); string(out) != string(src) {
		t.Fatalf("rejected write mutated the file: %q", out)
	}
	if err := UpdatePriorityInFileAndReindex(root, "P.md", 2, "Ship it", "open",
		PriorityNone, 1, FileVersion(src), nil); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(path)
	if !strings.Contains(string(out), "- [ ] Ship it ⏫ 📅 2026-08-15") {
		t.Fatalf("out=%q", out)
	}
	// Frontmatter must survive (line numbers are body-relative).
	if !strings.HasPrefix(string(out), "---\ntitle: P\n---\n") {
		t.Fatalf("frontmatter damaged: %q", out)
	}
}
