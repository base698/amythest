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

	html := renderTaskTriage(all, "Projects/Work.md", taskTriageOptions{}, "/")
	if strings.Contains(html, "<script>") {
		t.Fatalf("unescaped task text in %q", html)
	}
	for _, want := range []string{`data-task-triage`, `data-action="backlog"`, `data-action="due"`, `data-action="reference"`, `data-action="cancel"`, `data-file-action="backlog"`, `data-file-action="reference"`, `Projects/Work.md`} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q in %q", want, html)
		}
	}
}

func TestLoadTaskTriageContextsShowsSurroundingBodyLines(t *testing.T) {
	root := t.TempDir()
	rel := "Projects/Work.md"
	src := "---\ntitle: Work\n---\n# Work\n## Options\n- [ ] Choose a direction\nDecision notes live here.\n"
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, rel), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	contexts := loadTaskTriageContexts(root, []tasks.Task{{
		Slug: "work", Path: rel, Line: 3, Text: "Choose a direction", Status: tasks.StatusOpen,
	}}, rel)
	got := contexts[taskTriageContextKey("work", 3)]
	for _, want := range []string{"2 · ## Options", "3 · - [ ] Choose a direction", "4 · Decision notes live here."} {
		if !strings.Contains(got, want) {
			t.Errorf("context %q missing %q", got, want)
		}
	}
}
