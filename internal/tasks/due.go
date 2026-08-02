package tasks

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

var dueDateFieldRe = regexp.MustCompile(`\s*📅\s*\d{4}-\d{2}-\d{2}`)

// UpdateDueDateLine sets the due date on a task identified by its current
// body-relative line, text, status, and due date. The expected values make stale UI
// requests fail instead of changing a different task after an outside edit.
func UpdateDueDateLine(body []byte, lineNo int, expectedText, expectedStatus, expectedDue, due string) ([]byte, error) {
	lines := strings.Split(string(body), "\n")
	if lineNo < 1 || lineNo > len(lines) {
		return nil, fmt.Errorf("line %d out of range", lineNo)
	}
	line := lines[lineNo-1]
	lineEnding := ""
	if strings.HasSuffix(line, "\r") {
		line = strings.TrimSuffix(line, "\r")
		lineEnding = "\r"
	}
	if len(dueDateFieldRe.FindAllStringIndex(line, -1)) > 1 {
		return nil, fmt.Errorf("task has multiple due dates; resolve them and retry")
	}
	m := taskLineRe.FindStringSubmatch(line)
	if m == nil {
		return nil, fmt.Errorf("line %d is not a task", lineNo)
	}
	task := parseTask(m[1][0], m[2])
	if task.Text != expectedText || task.Status != expectedStatus || task.Due != expectedDue {
		return nil, fmt.Errorf("task changed during update; reload and retry")
	}
	if due != "" {
		parsed, err := time.Parse("2006-01-02", due)
		if err != nil || parsed.Format("2006-01-02") != due {
			return nil, fmt.Errorf("due date must be a valid YYYY-MM-DD date")
		}
	}

	if task.Due != "" {
		replaced := false
		updated := dueDateFieldRe.ReplaceAllStringFunc(line, func(string) string {
			if due != "" && !replaced {
				replaced = true
				return " 📅 " + due
			}
			return ""
		})
		lines[lineNo-1] = strings.TrimRight(updated, " ") + lineEnding
		return []byte(strings.Join(lines, "\n")), nil
	}
	if due == "" {
		return append([]byte(nil), body...), nil
	}
	lines[lineNo-1] = strings.TrimRight(line, " ") + " 📅 " + due + lineEnding
	return []byte(strings.Join(lines, "\n")), nil
}
