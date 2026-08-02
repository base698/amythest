package tasks

import (
	"strings"
	"testing"
)

func TestRenderHTMLIncludesDueDateEditor(t *testing.T) {
	html := RenderHTML([]Group{{Tasks: []Task{{
		Slug: "Projects/Launch", Path: "Projects/Launch.md", Line: 7,
		Text: "Ship release", Status: StatusOpen, Due: "2026-08-15", Priority: 3,
		Version: strings.Repeat("a", 64),
	}}}}, "/notes/")

	for _, want := range []string{
		`data-task-due-editor`,
		`data-slug="Projects/Launch"`,
		`data-line="7"`,
		`data-version="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`,
		`data-expected-text="Ship release"`,
		`data-expected-status="open"`,
		`data-expected-due="2026-08-15"`,
		`data-expected-version="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`,
		`type="date"`,
		`value="2026-08-15"`,
		`data-task-due-save`,
		`data-task-due-clear`,
		`Save`,
		`Clear`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("rendered task missing %q:\n%s", want, html)
		}
	}
}

func TestRenderHTMLWithBoardsIncludesStaleSafeMoveControlForOpenTasks(t *testing.T) {
	version := strings.Repeat("b", 64)
	html := RenderHTMLWithBoards([]Group{{Tasks: []Task{{
		Slug: "Projects/Launch", Path: "Projects/Launch.md", Line: 7,
		Text: "Ship release", Status: StatusOpen, Due: "2026-08-15", Version: version,
	}}}}, "/notes/", []string{"personal", `team<&`})

	for _, want := range []string{
		`data-task-move-editor`, `data-slug="Projects/Launch"`, `data-line="7"`,
		`data-expected-text="Ship release"`, `data-expected-status="open"`,
		`data-expected-version="` + version + `"`, `data-task-board`,
		`value="personal"`, `value="team&lt;&amp;"`, `data-task-move-submit`, `Move`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("rendered task missing %q:\n%s", want, html)
		}
	}
}

func TestRenderHTMLWithBoardsOmitsMoveControlWithoutBoardsOrForDoneTasks(t *testing.T) {
	open := []Group{{Tasks: []Task{{Slug: "A", Line: 1, Text: "Open", Status: StatusOpen}}}}
	if html := RenderHTMLWithBoards(open, "/", nil); strings.Contains(html, `data-task-move-editor`) {
		t.Fatalf("move control rendered without boards: %s", html)
	}
	done := []Group{{Tasks: []Task{{Slug: "A", Line: 1, Text: "Done", Status: StatusDone}}}}
	if html := RenderHTMLWithBoards(done, "/", []string{"personal"}); strings.Contains(html, `data-task-move-editor`) {
		t.Fatalf("move control rendered for done task: %s", html)
	}
}
