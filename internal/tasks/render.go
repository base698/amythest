package tasks

import (
	"html/template"
	"strconv"
	"strings"
)

var priorityBadge = [6]string{"🔺", "⏫", "🔼", "", "🔽", "⏬"}

// RenderHTML renders query result groups as a task list. Each task links
// back to its source note.
func RenderHTML(groups []Group, base string) string {
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
			renderTask(&b, t, base)
		}
		b.WriteString("</ul>")
	}
	if total == 0 {
		b.WriteString(`<p class="tasks-empty">No matching tasks.</p>`)
	}
	b.WriteString("</div>")
	return b.String()
}

func renderTask(b *strings.Builder, t Task, base string) {
	cls := "task-" + t.Status
	checked := ""
	if t.Status == StatusDone {
		checked = " checked"
	}
	b.WriteString(`<li class="task ` + cls + `"><input type="checkbox" class="task-toggle" data-slug="` +
		template.HTMLEscapeString(t.Slug) + `" data-line="` + strconv.Itoa(t.Line) + `" data-version="` +
		template.HTMLEscapeString(t.Version) + `"` + checked + `> `)
	b.WriteString(`<span class="task-text">` + template.HTMLEscapeString(t.Text) + `</span>`)
	if p := priorityBadge[t.Priority]; p != "" {
		b.WriteString(` <span class="task-prio">` + p + `</span>`)
	}
	renderDueDateEditor(b, t)
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
		`" data-expected-due="` + template.HTMLEscapeString(t.Due) + `">`)
	b.WriteString(`<summary class="task-date task-due">` + template.HTMLEscapeString(summary) + `</summary>`)
	b.WriteString(`<div class="task-due-controls"><label><span>Due date</span><input type="date" data-task-due-input value="` +
		template.HTMLEscapeString(t.Due) + `"></label><button type="button" data-task-due-save>Save</button>`)
	b.WriteString(`<button type="button" data-task-due-clear` + clearDisabled + `>Clear</button></div></details>`)
}
