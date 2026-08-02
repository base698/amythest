package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/base698/amythest/internal/tasks"
)

func TestTaskTriageCandidatesDefaultToActiveUnclassifiedNoDateTasks(t *testing.T) {
	all := []tasks.Task{
		{Slug: "work", Path: "Projects/Work.md", Line: 3, Text: "Choose next step", Status: tasks.StatusOpen},
		{Slug: "dated", Path: "Projects/Work.md", Line: 4, Text: "Already planned", Status: tasks.StatusOpen, Due: "2026-08-10"},
		{Slug: "backlog", Path: "Projects/Work.md", Line: 5, Text: "Intentional backlog", Status: tasks.StatusOpen, Tags: []string{"backlog"}},
		{Slug: "archive", Path: "Archive/Old.md", Line: 2, Text: "Historical", Status: tasks.StatusOpen},
		{Slug: "done", Path: "Projects/Work.md", Line: 6, Text: "Finished", Status: tasks.StatusDone},
	}

	got := taskTriageCandidates(all, taskTriageOptions{})
	if len(got) != 1 || got[0].Text != "Choose next step" {
		t.Fatalf("candidates = %#v", got)
	}
}

func TestTaskTriageIgnoredPathsMatchSegmentsNotPrefixes(t *testing.T) {
	all := []tasks.Task{{
		Slug: "research", Path: "ArchiveResearch/Active.md", Line: 1,
		Text: "Review current archive research", Status: tasks.StatusOpen,
	}}

	got := taskTriageCandidates(all, taskTriageOptions{})
	if len(got) != 1 {
		t.Fatalf("candidates = %#v", got)
	}
}

func TestRenderTaskTriageEscapesTaskTextAndProvidesActions(t *testing.T) {
	all := []tasks.Task{{
		Slug: "project", Path: "Projects/Work.md", Line: 3,
		Text: `<script>alert("x")</script>`, Status: tasks.StatusOpen,
	}}

	version := strings.Repeat("a", 64)
	html := renderTaskTriage(all, "Projects/Work.md", taskTriageOptions{Versions: map[string]string{"Projects/Work.md": version}}, "/")
	if strings.Contains(html, "<script>") {
		t.Fatalf("unescaped task text in %q", html)
	}
	for _, want := range []string{`data-task-triage`, `data-action="backlog"`, `data-action="due"`, `data-action="reference"`, `data-action="cancel"`, `data-file-action="backlog"`, `data-file-action="reference"`, `data-file-hide`, `data-file-disposition="archive"`, `data-file-disposition="trash"`, `data-file-version="` + version + `"`, `Hide file from tasks`, `Archive file`, `Move file to trash`, `class="triage-due-label">Due date`, `Projects/Work.md`} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q in %q", want, html)
		}
	}
}

func TestRenderTaskTriageUsesConfiguredBaseURL(t *testing.T) {
	all := []tasks.Task{{Slug: "project", Path: "Project.md", Line: 1, Text: "Task", Status: tasks.StatusOpen}}
	html := renderTaskTriage(all, "Project.md", taskTriageOptions{}, "/notes/")
	for _, want := range []string{`href="/notes/tasks"`, `href="/notes/project"`} {
		if !strings.Contains(html, want) {
			t.Fatalf("missing base-prefixed link %q in %q", want, html)
		}
	}
}

func TestRenderTaskTriageDisablesFileActionsOverBatchLimit(t *testing.T) {
	all := make([]tasks.Task, 0, 5001)
	for i := 1; i <= 5001; i++ {
		all = append(all, tasks.Task{Slug: "large", Path: "Large.md", Line: i, Text: "Item", Status: tasks.StatusOpen})
	}
	html := renderTaskTriage(all, "Large.md", taskTriageOptions{}, "/")
	if strings.Contains(html, `data-file-action=`) {
		t.Fatal("file-wide actions should be disabled over the API batch limit")
	}
	if !strings.Contains(html, "File actions unavailable for more than 5000 tasks") {
		t.Fatal("missing clear batch-limit explanation")
	}
}

func TestSelectedTaskTriagePathUsesRequestedOrLargestFile(t *testing.T) {
	candidates := []tasks.Task{
		{Path: "A.md"},
		{Path: "B.md"}, {Path: "B.md"},
	}
	if got := selectedTaskTriagePath(candidates, "A.md"); got != "A.md" {
		t.Fatalf("requested selection = %q", got)
	}
	if got := selectedTaskTriagePath(candidates, "missing.md"); got != "B.md" {
		t.Fatalf("fallback selection = %q", got)
	}
}

func TestLoadTaskTriageContextsShowsSurroundingBodyLines(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rel := "Projects/Work.md"
	src := "---\ntitle: Work\n---\n# Work\n## Options\n- [ ] Choose a direction\nDecision notes live here.\n"
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, rel), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	contexts, versions := loadTaskTriageContexts(root, []tasks.Task{
		{Slug: "work", Path: rel, Line: 3, Text: "Choose a direction", Status: tasks.StatusOpen},
		{Slug: "missing", Path: "Missing.md", Line: 1, Text: "Must not load", Status: tasks.StatusOpen},
	}, rel)
	got := contexts[taskTriageContextKey("work", 3)]
	for _, want := range []string{"2 · ## Options", "3 · - [ ] Choose a direction", "4 · Decision notes live here."} {
		if !strings.Contains(got, want) {
			t.Errorf("context %q missing %q", got, want)
		}
	}
	if versions[rel] != tasks.FileVersion([]byte(src)) {
		t.Fatalf("version=%q", versions[rel])
	}
	if _, loaded := versions["Missing.md"]; loaded {
		t.Fatal("unselected file was loaded")
	}
}
