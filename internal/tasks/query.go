package tasks

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Query is a parsed ```tasks``` block: line-oriented filters in the style of
// the Obsidian Tasks plugin. Supported grammar (one instruction per line):
//
//	done | not done
//	due (before|after|on) <date> | due | no due date
//	scheduled (before|after|on) <date>
//	happens before <date>
//	path includes <text> | path does not include <text>
//	description includes <text>
//	tags include <#tag> | tag includes <#tag>
//	priority is (high|medium|low|none)
//	sort by <field>[, <field>...]     (field: due|priority|path|description|done)
//	group by <field>                  (field: due|path|priority|status)
//	limit <n>
//
// Dates: YYYY-MM-DD, today, tomorrow, yesterday.
type Query struct {
	filters []func(*Task, queryEnv) bool
	sortBy  []string
	groupBy string
	limit   int
	err     error
}

type queryEnv struct {
	today string
}

// ParseQuery parses the text of a tasks block. Unknown instructions become
// an error carried on the query — rendered visibly, never fatal.
func ParseQuery(src string) *Query {
	q := &Query{}
	for rawLine := range strings.Lines(src) {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		if err := q.parseLine(line); err != nil {
			q.err = fmt.Errorf("%q: %w", line, err)
			return q
		}
	}
	return q
}

func (q *Query) parseLine(line string) error {
	l := strings.ToLower(line)
	switch {
	case l == "done":
		q.add(func(t *Task, _ queryEnv) bool { return t.Status == StatusDone })
	case l == "not done":
		q.add(func(t *Task, _ queryEnv) bool { return t.Status == StatusOpen })
	case l == "due", l == "has due date":
		q.add(func(t *Task, _ queryEnv) bool { return t.Due != "" })
	case l == "no due date":
		q.add(func(t *Task, _ queryEnv) bool { return t.Due == "" })
	case strings.HasPrefix(l, "due "):
		return q.dateFilter(strings.TrimPrefix(l, "due "), func(t *Task) string { return t.Due })
	case strings.HasPrefix(l, "scheduled "):
		return q.dateFilter(strings.TrimPrefix(l, "scheduled "), func(t *Task) string { return t.Scheduled })
	case strings.HasPrefix(l, "done before "), strings.HasPrefix(l, "done after "), strings.HasPrefix(l, "done on "):
		return q.dateFilter(strings.TrimPrefix(l, "done "), func(t *Task) string { return t.DoneDate })
	case strings.HasPrefix(l, "happens before "):
		date, err := resolveDate(strings.TrimPrefix(l, "happens before "))
		if err != nil {
			return err
		}
		q.add(func(t *Task, _ queryEnv) bool {
			return (t.Due != "" && t.Due < date) || (t.Scheduled != "" && t.Scheduled < date) ||
				(t.Start != "" && t.Start < date)
		})
	case strings.HasPrefix(l, "path does not include "):
		needle := strings.ToLower(strings.TrimSpace(line[len("path does not include "):]))
		q.add(func(t *Task, _ queryEnv) bool { return !strings.Contains(strings.ToLower(t.Path), needle) })
	case strings.HasPrefix(l, "path includes "):
		needle := strings.ToLower(strings.TrimSpace(line[len("path includes "):]))
		q.add(func(t *Task, _ queryEnv) bool { return strings.Contains(strings.ToLower(t.Path), needle) })
	case strings.HasPrefix(l, "description includes "):
		needle := strings.ToLower(strings.TrimSpace(line[len("description includes "):]))
		q.add(func(t *Task, _ queryEnv) bool { return strings.Contains(strings.ToLower(t.Text), needle) })
	case strings.HasPrefix(l, "tags include "), strings.HasPrefix(l, "tag includes "):
		needle := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(l[strings.Index(l, "include")+len("include"):]), "s "))
		needle = strings.TrimPrefix(strings.TrimSpace(needle), "#")
		q.add(func(t *Task, _ queryEnv) bool {
			for _, tag := range t.Tags {
				if strings.Contains(tag, needle) {
					return true
				}
			}
			return false
		})
	case strings.HasPrefix(l, "priority is "):
		want, err := priorityLevel(strings.TrimPrefix(l, "priority is "))
		if err != nil {
			return err
		}
		q.add(func(t *Task, _ queryEnv) bool { return t.Priority == want })
	case strings.HasPrefix(l, "sort by "):
		for _, f := range strings.Split(strings.TrimPrefix(l, "sort by "), ",") {
			field := strings.Fields(strings.TrimSpace(f))
			if len(field) == 0 {
				continue
			}
			switch field[0] {
			case "due", "priority", "path", "description", "done", "scheduled":
				q.sortBy = append(q.sortBy, field[0])
			default:
				return fmt.Errorf("unsupported sort field %q", field[0])
			}
		}
	case strings.HasPrefix(l, "group by "):
		field := strings.TrimSpace(strings.TrimPrefix(l, "group by "))
		switch field {
		case "due", "path", "priority", "status", "folder":
			q.groupBy = field
		default:
			return fmt.Errorf("unsupported group field %q", field)
		}
	case strings.HasPrefix(l, "limit "):
		n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(l, "limit ")))
		if err != nil {
			return err
		}
		q.limit = n
	case l == "hide edit button", l == "hide backlink", l == "short mode",
		strings.HasPrefix(l, "hide "), strings.HasPrefix(l, "show "):
		// presentation directives from the Obsidian plugin: accepted, ignored
	default:
		return fmt.Errorf("unsupported instruction")
	}
	return nil
}

func (q *Query) add(f func(*Task, queryEnv) bool) { q.filters = append(q.filters, f) }

func (q *Query) dateFilter(rest string, get func(*Task) string) error {
	fields := strings.Fields(rest)
	if len(fields) < 1 {
		return fmt.Errorf("missing date comparison")
	}
	op := fields[0]
	date, err := resolveDate(strings.Join(fields[1:], " "))
	if err != nil {
		return err
	}
	switch op {
	case "before":
		q.add(func(t *Task, _ queryEnv) bool { v := get(t); return v != "" && v < date })
	case "after":
		q.add(func(t *Task, _ queryEnv) bool { v := get(t); return v != "" && v > date })
	case "on":
		q.add(func(t *Task, _ queryEnv) bool { return get(t) == date })
	default:
		return fmt.Errorf("unsupported date comparison %q", op)
	}
	return nil
}

var wordNumbers = map[string]int{
	"one": 1, "two": 2, "three": 3, "four": 4, "five": 5, "six": 6,
	"seven": 7, "eight": 8, "nine": 9, "ten": 10, "eleven": 11, "twelve": 12,
	"a": 1, "an": 1,
}

func resolveDate(s string) (string, error) {
	s = strings.TrimSpace(s)
	now := time.Now()
	iso := func(t time.Time) string { return t.Format("2006-01-02") }
	switch s {
	case "today":
		return iso(now), nil
	case "tomorrow":
		return iso(now.AddDate(0, 0, 1)), nil
	case "yesterday":
		return iso(now.AddDate(0, 0, -1)), nil
	case "next week":
		return iso(now.AddDate(0, 0, 7)), nil
	case "last week":
		return iso(now.AddDate(0, 0, -7)), nil
	case "next month":
		return iso(now.AddDate(0, 1, 0)), nil
	}
	// "in two weeks", "in 3 days" / "two weeks ago", "3 days ago"
	fields := strings.Fields(s)
	relative := func(numStr, unit string) (time.Time, bool) {
		n, ok := wordNumbers[numStr]
		if !ok {
			var err error
			n, err = strconv.Atoi(numStr)
			if err != nil {
				return time.Time{}, false
			}
		}
		switch strings.TrimSuffix(unit, "s") {
		case "day":
			return now.AddDate(0, 0, n), true
		case "week":
			return now.AddDate(0, 0, 7*n), true
		case "month":
			return now.AddDate(0, n, 0), true
		case "year":
			return now.AddDate(n, 0, 0), true
		}
		return time.Time{}, false
	}
	if len(fields) == 3 && fields[0] == "in" {
		if t, ok := relative(fields[1], fields[2]); ok {
			return iso(t), nil
		}
	}
	if len(fields) == 3 && fields[2] == "ago" {
		if t, ok := relative(fields[0], fields[1]); ok {
			return iso(now.Add(-t.Sub(now))), nil
		}
	}
	if _, err := time.Parse("2006-01-02", s); err != nil {
		return "", fmt.Errorf("unrecognized date %q", s)
	}
	return s, nil
}

func priorityLevel(s string) (int, error) {
	switch strings.TrimSpace(s) {
	case "highest":
		return 0, nil
	case "high":
		return 1, nil
	case "medium":
		return 2, nil
	case "none":
		return 3, nil
	case "low":
		return 4, nil
	case "lowest":
		return 5, nil
	}
	return 0, fmt.Errorf("unrecognized priority %q", s)
}

// Err returns the parse error, if any.
func (q *Query) Err() error { return q.err }

// Run filters, sorts, groups, and limits tasks. Group order follows first
// appearance after sorting; the "" group renders last as "No <field>".
type Group struct {
	Name  string `json:"name"`
	Tasks []Task `json:"tasks"`
}

func (q *Query) Run(all []Task) []Group {
	env := queryEnv{today: time.Now().Format("2006-01-02")}
	var matched []Task
	for i := range all {
		ok := true
		for _, f := range q.filters {
			if !f(&all[i], env) {
				ok = false
				break
			}
		}
		if ok {
			matched = append(matched, all[i])
		}
	}

	sortBy := q.sortBy
	if len(sortBy) == 0 {
		sortBy = []string{"due", "priority"}
	}
	sort.SliceStable(matched, func(i, j int) bool {
		a, b := matched[i], matched[j]
		for _, f := range sortBy {
			var cmp int
			switch f {
			case "due":
				cmp = compareDate(a.Due, b.Due)
			case "scheduled":
				cmp = compareDate(a.Scheduled, b.Scheduled)
			case "done":
				cmp = compareDate(a.DoneDate, b.DoneDate)
			case "priority":
				cmp = a.Priority - b.Priority
			case "path":
				cmp = strings.Compare(a.Path, b.Path)
			case "description":
				cmp = strings.Compare(strings.ToLower(a.Text), strings.ToLower(b.Text))
			}
			if cmp != 0 {
				return cmp < 0
			}
		}
		return false
	})

	if q.limit > 0 && len(matched) > q.limit {
		matched = matched[:q.limit]
	}

	if q.groupBy == "" {
		return []Group{{Name: "", Tasks: matched}}
	}
	keyOf := func(t Task) string {
		switch q.groupBy {
		case "due":
			return t.Due
		case "path", "folder":
			if i := strings.LastIndex(t.Path, "/"); i >= 0 {
				return t.Path[:i]
			}
			return "/"
		case "priority":
			return [6]string{"Highest", "High", "Medium", "Normal", "Low", "Lowest"}[t.Priority]
		case "status":
			return t.Status
		}
		return ""
	}
	var groups []Group
	pos := map[string]int{}
	for _, t := range matched {
		k := keyOf(t)
		if i, ok := pos[k]; ok {
			groups[i].Tasks = append(groups[i].Tasks, t)
		} else {
			pos[k] = len(groups)
			groups = append(groups, Group{Name: k, Tasks: []Task{t}})
		}
	}
	return groups
}

// compareDate sorts empty dates last.
func compareDate(a, b string) int {
	if a == b {
		return 0
	}
	if a == "" {
		return 1
	}
	if b == "" {
		return -1
	}
	return strings.Compare(a, b)
}
