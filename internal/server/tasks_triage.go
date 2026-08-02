package server

import (
	"html/template"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/base698/amythest/internal/tasks"
	"github.com/base698/amythest/internal/vault"
)

type taskTriageOptions struct {
	IncludeBacklog bool
	IncludeIgnored bool
	Contexts       map[string]string
}

var taskTriageIgnoredSegments = map[string]struct{}{
	"Archive":          {},
	"Sources":          {},
	"Watchlists":       {},
	"Templates":        {},
	"Daily Notes":      {},
	".trash":           {},
	"Recurring_Chores": {},
}

func taskTriageCandidates(all []tasks.Task, opts taskTriageOptions) []tasks.Task {
	out := make([]tasks.Task, 0, len(all))
	for _, task := range all {
		if task.Status != tasks.StatusOpen || task.Due != "" {
			continue
		}
		if !opts.IncludeBacklog && hasTaskTag(task, "backlog") {
			continue
		}
		if !opts.IncludeIgnored && isTaskTriageIgnoredPath(task.Path) {
			continue
		}
		out = append(out, task)
	}
	return out
}

func taskTriageContextKey(slug string, line int) string {
	return slug + "\x00" + strconv.Itoa(line)
}

func loadTaskTriageContexts(vaultRoot string, candidates []tasks.Task, selectedPath string) map[string]string {
	contexts := make(map[string]string)
	bodies := make(map[string][]string)
	failed := make(map[string]bool)
	for _, task := range candidates {
		if selectedPath != "" && task.Path != selectedPath {
			continue
		}
		bodyLines, loaded := bodies[task.Path]
		if !loaded && !failed[task.Path] {
			src, err := os.ReadFile(filepath.Join(vaultRoot, filepath.FromSlash(task.Path)))
			if err != nil {
				failed[task.Path] = true
				continue
			}
			_, body := vault.ParseFrontmatter(src)
			bodyLines = strings.Split(string(body), "\n")
			bodies[task.Path] = bodyLines
		}
		if failed[task.Path] {
			continue
		}
		index := task.Line - 1
		if index < 0 || index >= len(bodyLines) {
			continue
		}
		start, end := max(0, index-1), min(len(bodyLines), index+2)
		var snippet strings.Builder
		for i := start; i < end; i++ {
			if snippet.Len() > 0 {
				snippet.WriteByte('\n')
			}
			snippet.WriteString(strconv.Itoa(i + 1))
			snippet.WriteString(" · ")
			snippet.WriteString(bodyLines[i])
		}
		contexts[taskTriageContextKey(task.Slug, task.Line)] = snippet.String()
	}
	return contexts
}

func renderTaskTriage(all []tasks.Task, selectedPath string, opts taskTriageOptions, base string) string {
	candidates := taskTriageCandidates(all, opts)
	byPath := make(map[string][]tasks.Task)
	for _, task := range candidates {
		byPath[task.Path] = append(byPath[task.Path], task)
	}
	paths := make([]string, 0, len(byPath))
	for path := range byPath {
		paths = append(paths, path)
	}
	sort.Slice(paths, func(i, j int) bool {
		left, right := len(byPath[paths[i]]), len(byPath[paths[j]])
		if left != right {
			return left > right
		}
		return paths[i] < paths[j]
	})
	if _, ok := byPath[selectedPath]; !ok && len(paths) > 0 {
		selectedPath = paths[0]
	}

	var b strings.Builder
	b.WriteString(`<div class="task-triage" data-task-triage>`)
	b.WriteString(`<div class="triage-heading"><div><p class="eyebrow">Undated task review</p><h2>Triage by file</h2>`)
	b.WriteString(`<p class="muted">Choose a real deadline, mark intentional backlog, convert reference checkboxes to bullets, or cancel obsolete work. No fake dates.</p></div>`)
	b.WriteString(`<div class="triage-summary"><strong>` + strconv.Itoa(len(candidates)) + `</strong><span>tasks</span><strong>` + strconv.Itoa(len(paths)) + `</strong><span>files</span></div></div>`)
	b.WriteString(`<div class="triage-links"><a href="` + template.HTMLEscapeString(base+"tasks") + `">← Task dashboard</a>`)
	if opts.IncludeBacklog || opts.IncludeIgnored {
		b.WriteString(`<a href="` + template.HTMLEscapeString(base+"tasks/triage") + `">Active untriaged only</a>`)
	} else {
		b.WriteString(`<a href="` + template.HTMLEscapeString(base+"tasks/triage?scope=all") + `">Include backlog and excluded areas</a>`)
	}
	b.WriteString(`</div>`)

	if len(paths) == 0 {
		b.WriteString(`<div class="triage-empty"><h3>Queue clear</h3><p>No open, undated tasks need classification.</p></div></div>`)
		return b.String()
	}

	b.WriteString(`<div class="triage-layout"><aside class="triage-files"><label>Filter files<input type="search" data-triage-filter placeholder="Path or task text"></label><nav>`)
	for _, path := range paths {
		cls := "triage-file"
		if path == selectedPath {
			cls += " active"
		}
		href := triagePathURL(base, path, opts)
		searchable := path
		for _, task := range byPath[path] {
			searchable += " " + task.Text
		}
		b.WriteString(`<a class="` + cls + `" data-triage-file data-search="` + template.HTMLEscapeString(searchable) + `" href="` + template.HTMLEscapeString(href) + `"><span>` + template.HTMLEscapeString(path) + `</span><strong>` + strconv.Itoa(len(byPath[path])) + `</strong></a>`)
	}
	b.WriteString(`</nav></aside><section class="triage-panel"><div class="triage-panel-heading"><div><p class="eyebrow">Current file</p><h3>` + template.HTMLEscapeString(selectedPath) + `</h3></div>`)
	selected := byPath[selectedPath]
	if len(selected) > 0 {
		b.WriteString(`<div class="triage-file-actions"><a class="triage-source" href="` + template.HTMLEscapeString(base+selected[0].Slug) + `">Open note ↗</a>`)
		b.WriteString(`<button type="button" data-file-action="backlog">Backlog entire file</button>`)
		b.WriteString(`<button type="button" data-file-action="reference">Make file a reference list</button></div>`)
	}
	b.WriteString(`</div><div class="triage-cards">`)
	for _, task := range selected {
		renderTaskTriageCard(&b, task, opts.Contexts[taskTriageContextKey(task.Slug, task.Line)])
	}
	b.WriteString(`</div></section></div></div>`)
	return b.String()
}

func triagePathURL(base, path string, opts taskTriageOptions) string {
	values := url.Values{"path": {path}}
	if opts.IncludeBacklog || opts.IncludeIgnored {
		values.Set("scope", "all")
	}
	return base + "tasks/triage?" + values.Encode()
}

func renderTaskTriageCard(b *strings.Builder, task tasks.Task, context string) {
	b.WriteString(`<article class="triage-card" data-triage-card data-slug="` + template.HTMLEscapeString(task.Slug) + `" data-line="` + strconv.Itoa(task.Line) + `" data-text="` + template.HTMLEscapeString(task.Text) + `">`)
	b.WriteString(`<p class="triage-task-text">` + template.HTMLEscapeString(task.Text) + `</p>`)
	if context != "" {
		b.WriteString(`<pre class="triage-context" aria-label="Source context">` + template.HTMLEscapeString(context) + `</pre>`)
	}
	b.WriteString(`<p class="triage-task-meta">Line ` + strconv.Itoa(task.Line) + `</p><div class="triage-actions">`)
	b.WriteString(`<button type="button" data-action="backlog">Keep as backlog</button>`)
	b.WriteString(`<span class="triage-due"><input type="date" data-triage-due aria-label="Due date"><button type="button" data-action="due">Set due</button></span>`)
	b.WriteString(`<button type="button" data-action="reference">Make reference</button>`)
	b.WriteString(`<button type="button" class="danger" data-action="cancel">Cancel task</button>`)
	b.WriteString(`</div></article>`)
}

func hasTaskTag(task tasks.Task, want string) bool {
	for _, tag := range task.Tags {
		if strings.EqualFold(tag, want) {
			return true
		}
	}
	return false
}

func isTaskTriageIgnoredPath(path string) bool {
	path = strings.ReplaceAll(path, "\\", "/")
	for _, segment := range strings.Split(strings.Trim(path, "/"), "/") {
		name := strings.TrimSuffix(segment, ".md")
		if _, ignored := taskTriageIgnoredSegments[name]; ignored {
			return true
		}
	}
	return false
}
