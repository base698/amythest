package tasks

import (
	"html/template"
	"regexp"
	"strconv"
	"strings"
)

var priorityBadge = [6]string{"🔺", "⏫", "🔼", "", "🔽", "⏬"}

var (
	mdLinkRe  = regexp.MustCompile(`\[([^\]]+)\]\((https?://[^)\s]+)\)`)
	bareURLRe = regexp.MustCompile(`https?://[^\s<>()\[\]]+`)
)

// renderTaskText escapes a task description and then reintroduces the two
// inline shapes worth keeping live: links (markdown and bare URLs) and #tag
// pills, mirroring how the note reading view renders the same line.
func renderTaskText(text string) string {
	var b strings.Builder
	rest := text
	for {
		loc := mdLinkRe.FindStringSubmatchIndex(rest)
		if loc == nil {
			break
		}
		writeLinkifiedText(&b, rest[:loc[0]])
		label, href := rest[loc[2]:loc[3]], rest[loc[4]:loc[5]]
		b.WriteString(`<a href="` + template.HTMLEscapeString(href) + `" rel="noopener">` +
			template.HTMLEscapeString(label) + `</a>`)
		rest = rest[loc[1]:]
	}
	writeLinkifiedText(&b, rest)
	return b.String()
}

func writeLinkifiedText(b *strings.Builder, text string) {
	rest := text
	for {
		loc := bareURLRe.FindStringIndex(rest)
		if loc == nil {
			break
		}
		writeTaggedText(b, rest[:loc[0]])
		href := rest[loc[0]:loc[1]]
		b.WriteString(`<a href="` + template.HTMLEscapeString(href) + `" rel="noopener">` +
			template.HTMLEscapeString(href) + `</a>`)
		rest = rest[loc[1]:]
	}
	writeTaggedText(b, rest)
}

func writeTaggedText(b *strings.Builder, text string) {
	rest := text
	for {
		loc := tagRe.FindStringIndex(rest)
		if loc == nil {
			break
		}
		b.WriteString(template.HTMLEscapeString(rest[:loc[0]]))
		b.WriteString(`<span class="task-tag">` + template.HTMLEscapeString(rest[loc[0]:loc[1]]) + `</span>`)
		rest = rest[loc[1]:]
	}
	b.WriteString(template.HTMLEscapeString(rest))
}

// RenderHTML renders query result groups as a task list. Each task links
// back to its source note.
func RenderHTML(groups []Group, base string) string {
	return RenderHTMLWithBoards(groups, base, nil)
}

// RenderHTMLWithBoards renders regular task rows with optional move-to-board
// controls. Embedded task-query blocks call RenderHTML and remain read-only.
func RenderHTMLWithBoards(groups []Group, base string, boards []string) string {
	return RenderHTMLWithOptions(groups, base, RenderOptions{Boards: boards})
}

// RenderOptions selects which mutation affordances rows carry. The zero
// value renders read-only rows (plus the completion toggle), which is what
// embedded ```tasks``` blocks use.
type RenderOptions struct {
	Boards     []string // non-empty enables move-to-board on open tasks
	Selectable bool     // selection checkboxes plus cancel/purge row actions
}

func RenderHTMLWithOptions(groups []Group, base string, opts RenderOptions) string {
	var b strings.Builder
	b.WriteString(`<div class="tasks-list">`)
	total := 0
	for _, g := range groups {
		if len(g.Tasks) == 0 {
			continue
		}
		total += len(g.Tasks)
		if g.Name != "" {
			b.WriteString(`<h4 class="tasks-group">` + template.HTMLEscapeString(g.Name) + `</h4>`)
		}
		b.WriteString("<ul>")
		for _, t := range g.Tasks {
			renderTask(&b, t, base, opts)
		}
		b.WriteString("</ul>")
	}
	if total == 0 {
		b.WriteString(`<p class="tasks-empty">No matching tasks.</p>`)
	}
	b.WriteString("</div>")
	return b.String()
}

func renderTask(b *strings.Builder, t Task, base string, opts RenderOptions) {
	cls := "task-" + t.Status
	checked := ""
	if t.Status == StatusDone {
		checked = " checked"
	}
	b.WriteString(`<li class="task ` + cls + `">`)
	if opts.Selectable {
		b.WriteString(`<input type="checkbox" class="task-select" data-task-select data-slug="` +
			template.HTMLEscapeString(t.Slug) + `" data-line="` + strconv.Itoa(t.Line) +
			`" data-text="` + template.HTMLEscapeString(t.Text) +
			`" data-status="` + template.HTMLEscapeString(t.Status) +
			`" data-version="` + template.HTMLEscapeString(t.Version) +
			`" aria-label="Select task"> `)
	}
	b.WriteString(`<input type="checkbox" class="task-toggle" data-slug="` +
		template.HTMLEscapeString(t.Slug) + `" data-line="` + strconv.Itoa(t.Line) + `" data-version="` +
		template.HTMLEscapeString(t.Version) + `"` + checked + `> `)
	b.WriteString(`<span class="task-text">` + renderTaskText(t.Text) + `</span>`)
	renderTrailingTags(b, t)
	if p := priorityBadge[t.Priority]; p != "" {
		b.WriteString(` <span class="task-prio">` + p + `</span>`)
	}
	renderDueDateEditor(b, t)
	renderMoveToBoardEditor(b, t, opts.Boards)
	if opts.Selectable {
		renderCancelPurgeButtons(b, t)
	}
	if t.Scheduled != "" {
		b.WriteString(` <span class="task-date">⏳ ` + template.HTMLEscapeString(t.Scheduled) + `</span>`)
	}
	if t.Recurrence != "" {
		b.WriteString(` <span class="task-date">🔁 ` + template.HTMLEscapeString(t.Recurrence) + `</span>`)
	}
	if t.DoneDate != "" {
		b.WriteString(` <span class="task-date">✅ ` + template.HTMLEscapeString(t.DoneDate) + `</span>`)
	}
	b.WriteString(` <a class="task-src" href="` + template.HTMLEscapeString(base+t.Slug) + `" title="` +
		template.HTMLEscapeString(t.Path) + `">↗</a>`)
	b.WriteString("</li>")
}

// renderTrailingTags shows tags the parser stripped from the description
// because they sit after the first metadata emoji. Tags still inside the
// description are already pilled in place by renderTaskText.
func renderTrailingTags(b *strings.Builder, t Task) {
	inText := make(map[string]bool, len(t.Tags))
	for _, m := range tagRe.FindAllStringSubmatch(t.Text, -1) {
		inText[strings.ToLower(m[1])] = true
	}
	for _, tag := range t.Tags {
		if inText[tag] {
			continue
		}
		b.WriteString(` <span class="task-tag">#` + template.HTMLEscapeString(tag) + `</span>`)
	}
}

func renderCancelPurgeButtons(b *strings.Builder, t Task) {
	switch t.Status {
	case StatusOpen:
		b.WriteString(` <button type="button" class="task-cancel" data-task-cancel data-slug="` +
			template.HTMLEscapeString(t.Slug) + `" data-line="` + strconv.Itoa(t.Line) +
			`" data-text="` + template.HTMLEscapeString(t.Text) +
			`" data-version="` + template.HTMLEscapeString(t.Version) +
			`" title="Mark cancelled (❌); purge later to remove">Cancel</button>`)
	case StatusCancelled:
		b.WriteString(` <button type="button" class="task-purge danger" data-task-purge data-slug="` +
			template.HTMLEscapeString(t.Slug) + `" data-line="` + strconv.Itoa(t.Line) +
			`" data-version="` + template.HTMLEscapeString(t.Version) +
			`" title="Permanently remove this cancelled task line">Purge</button>`)
	}
}

// RenderInlineActions renders the row affordances (due-date editor plus
// cancel/purge) for one task, for embedding at the end of a task line inside
// a rendered note. The markup matches /tasks rows, so the same delegated
// frontend handlers drive both surfaces.
func RenderInlineActions(t Task) string {
	var b strings.Builder
	renderDueDateEditor(&b, t)
	renderCancelPurgeButtons(&b, t)
	return b.String()
}

func renderMoveToBoardEditor(b *strings.Builder, t Task, boards []string) {
	if t.Status != StatusOpen || len(boards) == 0 {
		return
	}
	b.WriteString(` <details class="task-move-editor" data-task-move-editor data-slug="` +
		template.HTMLEscapeString(t.Slug) + `" data-line="` + strconv.Itoa(t.Line) +
		`" data-expected-text="` + template.HTMLEscapeString(t.Text) +
		`" data-expected-status="` + template.HTMLEscapeString(t.Status) +
		`" data-expected-version="` + template.HTMLEscapeString(t.Version) + `">`)
	b.WriteString(`<summary>Move to board</summary><div class="task-move-controls"><label><span>Board</span><select data-task-board>`)
	for _, board := range boards {
		escaped := template.HTMLEscapeString(board)
		b.WriteString(`<option value="` + escaped + `">` + escaped + `</option>`)
	}
	b.WriteString(`</select></label><button type="button" data-task-move-submit>Move</button></div></details>`)
}

func renderDueDateEditor(b *strings.Builder, t Task) {
	summary := "Due date"
	if t.Due != "" {
		summary = "📅 " + t.Due
	}
	clearDisabled := ""
	if t.Due == "" {
		clearDisabled = " disabled"
	}
	b.WriteString(` <details class="task-due-editor" data-task-due-editor data-slug="` +
		template.HTMLEscapeString(t.Slug) + `" data-line="` + strconv.Itoa(t.Line) +
		`" data-expected-text="` + template.HTMLEscapeString(t.Text) +
		`" data-expected-status="` + template.HTMLEscapeString(t.Status) +
		`" data-expected-due="` + template.HTMLEscapeString(t.Due) +
		`" data-expected-version="` + template.HTMLEscapeString(t.Version) + `">`)
	b.WriteString(`<summary class="task-date task-due">` + template.HTMLEscapeString(summary) + `</summary>`)
	b.WriteString(`<div class="task-due-controls"><label><span>Due date</span><input type="date" data-task-due-input value="` +
		template.HTMLEscapeString(t.Due) + `"></label><button type="button" data-task-due-save>Save</button>`)
	b.WriteString(`<button type="button" data-task-due-clear` + clearDisabled + `>Clear</button></div></details>`)
}
