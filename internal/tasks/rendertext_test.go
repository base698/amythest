package tasks

import (
	"strings"
	"testing"
)

func TestRenderTaskTextEscapesAndLinkifies(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"hostile html escaped",
			`<img src=x onerror=alert(1)>`,
			`&lt;img src=x onerror=alert(1)&gt;`},
		{"markdown link",
			`read [the docs](https://example.com/a?b=1) today`,
			`read <a href="https://example.com/a?b=1" rel="noopener">the docs</a> today`},
		{"bare url",
			`see https://github.com/TheRobotStudio/SO-ARM100 ordered`,
			`see <a href="https://github.com/TheRobotStudio/SO-ARM100" rel="noopener">https://github.com/TheRobotStudio/SO-ARM100</a> ordered`},
		{"tag pill",
			`fender v2 #3dprint/project`,
			`fender v2 <span class="task-tag">#3dprint/project</span>`},
		{"hostile link label escaped",
			`[<b>x</b>](https://e.com/)`,
			`<a href="https://e.com/" rel="noopener">&lt;b&gt;x&lt;/b&gt;</a>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := renderTaskText(tc.in); got != tc.want {
				t.Fatalf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
}

func TestRenderTaskShowsEveryTagExactlyOnce(t *testing.T) {
	// "#lead" stays in the description; "#trailing" sits after 📅 so the
	// parser strips it from Text but keeps it in Tags.
	html := RenderHTML([]Group{{Tasks: []Task{{
		Slug: "P", Line: 1, Text: "#lead fender v2", Status: StatusOpen,
		Due: "2026-08-15", Tags: []string{"lead", "trailing"}, Priority: 3,
	}}}}, "/")
	for _, want := range []string{
		`<span class="task-tag">#lead</span>`,
		`<span class="task-tag">#trailing</span>`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("missing %s in:\n%s", want, html)
		}
	}
	if got := strings.Count(html, `class="task-tag"`); got != 2 {
		t.Fatalf("expected 2 tag pills, got %d:\n%s", got, html)
	}
}

func TestRenderInlineActionsMatchesRowMarkup(t *testing.T) {
	open := Task{Slug: "P", Line: 3, Text: "Ship", Status: StatusOpen, Due: "2026-08-15", Version: strings.Repeat("a", 64)}
	html := RenderInlineActions(open)
	for _, want := range []string{"data-task-due-editor", "data-task-cancel", `data-expected-due="2026-08-15"`} {
		if !strings.Contains(html, want) {
			t.Fatalf("open-task actions missing %s: %s", want, html)
		}
	}
	cancelled := Task{Slug: "P", Line: 4, Text: "Old", Status: StatusCancelled, Version: strings.Repeat("a", 64)}
	if html := RenderInlineActions(cancelled); !strings.Contains(html, "data-task-purge") {
		t.Fatalf("cancelled-task actions missing purge: %s", html)
	}
}
