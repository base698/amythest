package tasks

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// ToggleLine checks or unchecks the task on the given 1-based body line and
// returns the new body. Completing a task appends a ✅ done-date; completing
// a recurring task (🔁) also inserts the next occurrence, unchecked and with
// its dates advanced, above the completed line — matching the Obsidian
// Tasks plugin's behavior.
func ToggleLine(body []byte, lineNo int, done bool, now time.Time) (newBody []byte, recurred bool, err error) {
	lines := strings.Split(string(body), "\n")
	if lineNo < 1 || lineNo > len(lines) {
		return nil, false, fmt.Errorf("line %d out of range", lineNo)
	}
	line := lines[lineNo-1]
	m := taskLineRe.FindStringSubmatch(line)
	if m == nil {
		return nil, false, fmt.Errorf("line %d is not a task", lineNo)
	}

	today := now.Format("2006-01-02")
	if done {
		updated := setCheckbox(line, 'x')
		if !strings.Contains(updated, "✅") {
			updated = strings.TrimRight(updated, " ") + " ✅ " + today
		}
		task := parseTask(m[1][0], m[2])
		if task.Recurrence != "" {
			if next, ok := nextOccurrenceLine(line, task, now); ok {
				lines[lineNo-1] = next + "\n" + updated
				return []byte(strings.Join(lines, "\n")), true, nil
			}
		}
		lines[lineNo-1] = updated
		return []byte(strings.Join(lines, "\n")), false, nil
	}

	updated := setCheckbox(line, ' ')
	updated = doneDateRe.ReplaceAllString(updated, "")
	lines[lineNo-1] = strings.TrimRight(updated, " ")
	return []byte(strings.Join(lines, "\n")), false, nil
}

var (
	checkboxRe = regexp.MustCompile(`^(\s*[-*+]\s+\[).(\])`)
	doneDateRe = regexp.MustCompile(`\s*✅\s*\d{4}-\d{2}-\d{2}`)
	taskDateRe = regexp.MustCompile(`(📅|⏳|🛫)(\s*)(\d{4}-\d{2}-\d{2})`)
)

func setCheckbox(line string, state byte) string {
	return checkboxRe.ReplaceAllString(line, "${1}"+string(state)+"${2}")
}

// nextOccurrenceLine builds the new unchecked occurrence for a completed
// recurring task: same line with every date advanced per the rule and any
// done-date removed. Returns false when the rule is unsupported or the task
// has no dates to advance.
func nextOccurrenceLine(line string, task Task, now time.Time) (string, bool) {
	rule, whenDone := parseRecurrence(task.Recurrence)
	if rule == nil {
		return "", false
	}
	base := firstNonEmpty(task.Due, task.Scheduled, task.Start)
	if base == "" {
		return "", false
	}
	baseDate, err := time.Parse("2006-01-02", base)
	if err != nil {
		return "", false
	}
	// "when done" recurs from the completion date; otherwise from the
	// task's own date. The delta is always measured against the task's
	// date so every date field shifts onto the new occurrence.
	from := baseDate
	if whenDone {
		from = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	}
	next := rule(from)
	if !next.After(baseDate) {
		return "", false
	}
	deltaDays := int(next.Sub(baseDate).Hours() / 24)

	out := taskDateRe.ReplaceAllStringFunc(line, func(m string) string {
		sub := taskDateRe.FindStringSubmatch(m)
		d, err := time.Parse("2006-01-02", sub[3])
		if err != nil {
			return m
		}
		return sub[1] + sub[2] + d.AddDate(0, 0, deltaDays).Format("2006-01-02")
	})
	out = doneDateRe.ReplaceAllString(out, "")
	return strings.TrimRight(setCheckbox(out, ' '), " "), true
}

var weekdayNames = map[string]time.Weekday{
	"sunday": time.Sunday, "monday": time.Monday, "tuesday": time.Tuesday,
	"wednesday": time.Wednesday, "thursday": time.Thursday,
	"friday": time.Friday, "saturday": time.Saturday,
	"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday, "tues": time.Tuesday,
	"wed": time.Wednesday, "thu": time.Thursday, "thur": time.Thursday, "thurs": time.Thursday,
	"fri": time.Friday, "sat": time.Saturday,
}

// parseRecurrence understands the common Obsidian Tasks rules:
//
//	every day | every N days | every week | every N weeks
//	every month | every N months | every year | every N years
//	every week on Wednesday[, Saturday] | every weekday
//	… optionally suffixed with "when done" (advance from today).
func parseRecurrence(rule string) (func(time.Time) time.Time, bool) {
	r := strings.ToLower(strings.TrimSpace(rule))
	whenDone := false
	if cut, ok := strings.CutSuffix(r, "when done"); ok {
		whenDone = true
		r = strings.TrimSpace(cut)
	}
	r = strings.TrimPrefix(r, "every")
	r = strings.TrimSpace(r)

	if r == "weekday" {
		return func(t time.Time) time.Time {
			n := t.AddDate(0, 0, 1)
			for n.Weekday() == time.Saturday || n.Weekday() == time.Sunday {
				n = n.AddDate(0, 0, 1)
			}
			return n
		}, whenDone
	}

	// "week on wednesday, saturday"
	if rest, ok := strings.CutPrefix(r, "week on "); ok {
		var days []time.Weekday
		for _, part := range strings.FieldsFunc(rest, func(c rune) bool { return c == ',' || c == ' ' }) {
			if wd, ok := weekdayNames[strings.TrimSpace(part)]; ok {
				days = append(days, wd)
			}
		}
		if len(days) == 0 {
			return nil, false
		}
		return func(t time.Time) time.Time {
			for i := 1; i <= 7; i++ {
				n := t.AddDate(0, 0, i)
				for _, wd := range days {
					if n.Weekday() == wd {
						return n
					}
				}
			}
			return t
		}, whenDone
	}

	// "N weeks on wednesday[, saturday]" — jump N-1 weeks, then land on the
	// next listed weekday. Vault data uses this form (e.g. haircut).
	if fields := strings.Fields(r); len(fields) >= 4 &&
		(fields[1] == "week" || fields[1] == "weeks") && fields[2] == "on" {
		count := 0
		if _, err := fmt.Sscanf(fields[0], "%d", &count); err != nil || count < 1 {
			if n, ok := wordNumbers[fields[0]]; ok {
				count = n
			} else {
				return nil, false
			}
		}
		var days []time.Weekday
		for _, part := range strings.FieldsFunc(strings.Join(fields[3:], " "), func(c rune) bool { return c == ',' || c == ' ' }) {
			if wd, ok := weekdayNames[strings.TrimSpace(part)]; ok {
				days = append(days, wd)
			}
		}
		if len(days) == 0 {
			return nil, false
		}
		return func(t time.Time) time.Time {
			base := t.AddDate(0, 0, 7*(count-1))
			for i := 1; i <= 7; i++ {
				n := base.AddDate(0, 0, i)
				for _, wd := range days {
					if n.Weekday() == wd {
						return n
					}
				}
			}
			return t
		}, whenDone
	}

	count := 1
	fields := strings.Fields(r)
	unit := r
	if len(fields) == 2 {
		if _, err := fmt.Sscanf(fields[0], "%d", &count); err != nil || count < 1 {
			if n, ok := wordNumbers[fields[0]]; ok {
				count = n
			} else {
				return nil, false
			}
		}
		unit = fields[1]
	} else if len(fields) != 1 {
		return nil, false
	}

	switch strings.TrimSuffix(unit, "s") {
	case "day":
		return func(t time.Time) time.Time { return t.AddDate(0, 0, count) }, whenDone
	case "week":
		return func(t time.Time) time.Time { return t.AddDate(0, 0, 7*count) }, whenDone
	case "month":
		return func(t time.Time) time.Time { return t.AddDate(0, count, 0) }, whenDone
	case "year":
		return func(t time.Time) time.Time { return t.AddDate(count, 0, 0) }, whenDone
	}
	return nil, false
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
