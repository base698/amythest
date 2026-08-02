package server

import (
	"encoding/base64"
	"html/template"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/base698/amythest/internal/tasks"
)

var tasksBlockRe = regexp.MustCompile(`<div class="tasks-block" data-tasks-query="([A-Za-z0-9+/=]*)"></div>`)

// expandDynamicBlocks replaces cached placeholders (tasks queries, base
// views) with freshly computed HTML at serve time.
func (s *Server) expandDynamicBlocks(html template.HTML) template.HTML {
	out := string(html)
	if strings.Contains(out, `class="tasks-block"`) {
		all, err := s.db.AllTasks()
		if err != nil {
			return html
		}
		out = tasksBlockRe.ReplaceAllStringFunc(out, func(m string) string {
			sub := tasksBlockRe.FindStringSubmatch(m)
			raw, err := base64.StdEncoding.DecodeString(sub[1])
			if err != nil {
				return m
			}
			q := tasks.ParseQuery(string(raw))
			if q.Err() != nil {
				return `<div class="callout" data-callout="warning"><div class="callout-title">Tasks query error</div><div class="callout-content"><p>` +
					template.HTMLEscapeString(q.Err().Error()) + `</p></div></div>`
			}
			return tasks.RenderHTML(q.Run(all), s.base())
		})
	}
	out = s.expandBaseBlocks(out)
	return template.HTML(out)
}

// handleTasksPage renders the /tasks board: the sectioned dashboard by
// default, or a flat filtered/sorted list when board controls are used.
// Everything is plain links (GET params), so it works in any browser and
// the underlying tasks stay ordinary markdown any other app can read.
func (s *Server) handleTasksPage(w http.ResponseWriter, r *http.Request) {
	all, err := s.db.AllTasks()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var boardNames []string
	if s.kanban != nil {
		if summaries, listErr := s.kanban.ListBoards(); listErr == nil {
			boardNames = make([]string, 0, len(summaries))
			for _, summary := range summaries {
				boardNames = append(boardNames, summary.Name)
			}
		}
	}

	statusFilter := r.URL.Query().Get("status") // "", open, done, all
	sortKey := r.URL.Query().Get("sort")        // "", due, priority, path, description, done
	groupKey := r.URL.Query().Get("group")      // "", due, path, priority, status

	var b strings.Builder
	b.WriteString(s.tasksToolbar(statusFilter, sortKey, groupKey))
	b.WriteString(`<div class="tasks-bulkbar" data-task-bulkbar hidden>` +
		`<span class="muted" data-task-selcount></span> ` +
		`<button type="button" data-task-bulk-cancel>Cancel selected</button> ` +
		`<button type="button" class="danger" data-task-bulk-purge>Purge selected</button> ` +
		`<button type="button" data-task-select-none>Clear selection</button></div>`)

	if statusFilter == "" && sortKey == "" && groupKey == "" {
		section := func(title, query string) {
			q := tasks.ParseQuery(query)
			groups := q.Run(all)
			n := 0
			for _, g := range groups {
				n += len(g.Tasks)
			}
			if n == 0 {
				return
			}
			b.WriteString(`<h2>` + template.HTMLEscapeString(title) + `</h2>`)
			b.WriteString(tasks.RenderHTMLWithOptions(groups, s.base(), tasks.RenderOptions{Boards: boardNames, Selectable: true}))
		}
		section("Overdue", "not done\ndue before today\nsort by due, priority")
		section("Today", "not done\ndue on today\nsort by priority")
		section("Upcoming", "not done\ndue after today\nsort by due, priority\nlimit 30")
		section("No due date", "not done\nno due date\nsort by path\nlimit 100")
		section("Recently completed", "done\nsort by done\nlimit 15")
	} else {
		var lines []string
		switch statusFilter {
		case "done":
			lines = append(lines, "done")
		case "all", "":
		default: // "open" and anything unrecognized show only open tasks
			lines = append(lines, "not done")
		}
		switch sortKey {
		case "due", "priority", "path", "description", "done", "scheduled":
			lines = append(lines, "sort by "+sortKey)
		}
		switch groupKey {
		case "due", "path", "priority", "status", "folder":
			lines = append(lines, "group by "+groupKey)
		}
		q := tasks.ParseQuery(strings.Join(lines, "\n"))
		if q.Err() != nil {
			http.Error(w, q.Err().Error(), http.StatusBadRequest)
			return
		}
		groups := q.Run(all)
		total := 0
		for _, g := range groups {
			total += len(g.Tasks)
		}
		b.WriteString(`<p class="muted">` + itoa(total) + ` tasks</p>`)
		b.WriteString(tasks.RenderHTMLWithOptions(groups, s.base(), tasks.RenderOptions{Boards: boardNames, Selectable: true}))
	}

	s.renderPage(w, pageData{
		SiteName:    s.cfg.SiteName,
		Base:        s.base(),
		Title:       "Tasks",
		HTML:        template.HTML(b.String()),
		Breadcrumbs: []crumb{{Name: "Home", URL: s.base()}},
		Tree:        s.tree.Load(),
		Slug:        "tasks",
	})
}

func (s *Server) handleTasksTriagePage(w http.ResponseWriter, r *http.Request) {
	all, err := s.db.AllTasks()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	showAll := r.URL.Query().Get("scope") == "all"
	opts := taskTriageOptions{IncludeBacklog: showAll, IncludeIgnored: showAll}
	candidates := taskTriageCandidates(all, opts)
	selectedPath := selectedTaskTriagePath(candidates, r.URL.Query().Get("path"))
	opts.Contexts, opts.Versions = loadTaskTriageContexts(s.cfg.Vault, candidates, selectedPath)
	html := renderTaskTriage(all, selectedPath, opts, s.base())
	s.renderPage(w, pageData{
		SiteName:    s.cfg.SiteName,
		Base:        s.base(),
		Title:       "Task triage",
		HTML:        template.HTML(html),
		Breadcrumbs: []crumb{{Name: "Home", URL: s.base()}, {Name: "Tasks", URL: s.base() + "tasks"}},
		Tree:        s.tree.Load(),
		Slug:        "tasks/triage",
	})
}

// tasksToolbar renders the status/sort/group controls as plain links. An
// active chip links to the same view with its own dimension cleared, so
// clicking it again toggles that filter off.
func (s *Server) tasksToolbar(status, sortKey, group string) string {
	var b strings.Builder
	link := func(label, st, so, gr string, active bool) {
		cls := "chip"
		if active {
			cls = "chip active"
		}
		params := []string{}
		if st != "" {
			params = append(params, "status="+st)
		}
		if so != "" {
			params = append(params, "sort="+so)
		}
		if gr != "" {
			params = append(params, "group="+gr)
		}
		href := s.base() + "tasks"
		if len(params) > 0 {
			href += "?" + strings.Join(params, "&")
		}
		b.WriteString(`<a class="` + cls + `" href="` + template.HTMLEscapeString(href) + `">` +
			template.HTMLEscapeString(label) + `</a>`)
	}
	toggle := func(current, k string) string {
		if current == k {
			return ""
		}
		return k
	}
	b.WriteString(`<div class="tasks-toolbar"><span class="eyebrow">Show</span>`)
	link("Dashboard", "", "", "", status == "" && sortKey == "" && group == "")
	link("Open", toggle(status, "open"), sortKey, group, status == "open")
	link("Done", toggle(status, "done"), sortKey, group, status == "done")
	link("All", toggle(status, "all"), sortKey, group, status == "all")
	b.WriteString(`<a class="chip triage-chip" href="` + template.HTMLEscapeString(s.base()+"tasks/triage") + `">Triage no dates</a>`)
	b.WriteString(`<span class="eyebrow">Sort</span>`)
	for _, k := range []string{"due", "priority", "path", "description"} {
		st := status
		if st == "" {
			st = "open"
		}
		link(k, st, toggle(sortKey, k), group, sortKey == k)
	}
	b.WriteString(`<span class="eyebrow">Group</span>`)
	for _, k := range []string{"folder", "priority", "status"} {
		st := status
		if st == "" {
			st = "open"
		}
		link(k, st, sortKey, toggle(group, k), group == k)
	}
	b.WriteString(`</div>`)
	return b.String()
}

// handleTasksAPI executes a tasks query (newline- or semicolon-separated
// instructions) and returns grouped JSON.
func (s *Server) handleTasksAPI(w http.ResponseWriter, r *http.Request) {
	// Go's query parser drops parameters containing raw semicolons, which
	// are natural in "a;b;c" instruction lists — parse around that.
	rawQuery := strings.ReplaceAll(r.URL.RawQuery, ";", "%3B")
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	queryText := strings.ReplaceAll(values.Get("query"), ";", "\n")
	q := tasks.ParseQuery(queryText)
	if q.Err() != nil {
		http.Error(w, q.Err().Error(), http.StatusBadRequest)
		return
	}
	all, err := s.db.AllTasks()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.writeJSON(w, q.Run(all))
}
