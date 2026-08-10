package tui

import (
	"fmt"
	"strings"

	"github.com/base698/amythest/internal/apiclient"
	"github.com/base698/amythest/internal/kanban/board"
)

// A card edits as one plain-text document: a small key/value header, a "---"
// rule, then the description verbatim. parseCardEdit diffs the result against
// the card and returns a patch of only what changed, so an untouched field
// can't clobber a concurrent update it never saw.

const cardEditSeparator = "---"

func serializeCardEdit(c board.Card) string {
	var b strings.Builder
	fmt.Fprintf(&b, "title: %s\n", c.Title)
	fmt.Fprintf(&b, "status: %s\n", c.Status)
	fmt.Fprintf(&b, "priority: %s\n", c.Priority)
	fmt.Fprintf(&b, "due: %s\n", c.DueDate)
	fmt.Fprintf(&b, "labels: %s\n", strings.Join(c.Labels, ", "))
	fmt.Fprintf(&b, "blocked: %t\n", c.Blocked)
	b.WriteString(cardEditSeparator + "\n")
	b.WriteString(c.Description)
	return b.String()
}

// parseCardEdit reads the document back. Everything after the first "---"
// line is the description, verbatim — a description that itself starts with
// "---" is safe because the header always comes first.
func parseCardEdit(text string, current board.Card) (apiclient.CardPatch, error) {
	var patch apiclient.CardPatch
	lines := strings.Split(text, "\n")
	sep := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == cardEditSeparator {
			sep = i
			break
		}
	}
	if sep == -1 {
		return patch, fmt.Errorf("missing %q separator line", cardEditSeparator)
	}

	for _, line := range lines[:sep] {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return patch, fmt.Errorf("bad header line %q (want key: value)", line)
		}
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(key) {
		case "title":
			if value == "" {
				return patch, fmt.Errorf("title cannot be empty")
			}
			if value != current.Title {
				patch.Title = &value
			}
		case "status":
			status := board.Status(value)
			if !board.ValidStatus(status) {
				return patch, fmt.Errorf("bad status %q (triage|backlog|ready|in_progress|verify|done)", value)
			}
			if status != current.Status {
				patch.Status = &status
			}
		case "priority":
			priority := board.Priority(value)
			if !board.ValidPriority(priority) {
				return patch, fmt.Errorf("bad priority %q (p0|p1|p2|p3)", value)
			}
			if priority != current.Priority {
				patch.Priority = &priority
			}
		case "due":
			if value != "" && !isoDateRe.MatchString(value) {
				return patch, fmt.Errorf("bad due date %q (YYYY-MM-DD or empty)", value)
			}
			if value != current.DueDate {
				patch.DueDate = &value
			}
		case "labels":
			labels := splitLabels(value)
			if !equalStrings(labels, current.Labels) {
				patch.Labels = &labels
			}
		case "blocked":
			if value != "true" && value != "false" {
				return patch, fmt.Errorf("bad blocked %q (true|false)", value)
			}
			blocked := value == "true"
			if blocked != current.Blocked {
				patch.Blocked = &blocked
			}
		default:
			return patch, fmt.Errorf("unknown header %q", strings.TrimSpace(key))
		}
	}

	desc := strings.TrimRight(strings.Join(lines[sep+1:], "\n"), "\n")
	if desc != strings.TrimRight(current.Description, "\n") {
		patch.Description = &desc
	}
	return patch, nil
}

func splitLabels(value string) []string {
	labels := []string{}
	for _, part := range strings.Split(value, ",") {
		if part = strings.TrimSpace(part); part != "" {
			labels = append(labels, part)
		}
	}
	return labels
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func emptyPatch(p apiclient.CardPatch) bool {
	return p.Title == nil && p.Description == nil && p.DueDate == nil &&
		p.Milestone == nil && p.Priority == nil && p.Status == nil &&
		p.Assignee == nil && p.Agent == nil && p.Blocked == nil && p.Labels == nil
}
