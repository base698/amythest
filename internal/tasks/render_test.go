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
