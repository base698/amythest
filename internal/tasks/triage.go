package tasks

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/base698/amythest/internal/vault"
)

type TriageAction string

const (
	TriageBacklog   TriageAction = "backlog"
	TriageDue       TriageAction = "due"
	TriageReference TriageAction = "reference"
	TriageCancel    TriageAction = "cancel"
)

var triageTaskPrefixRe = regexp.MustCompile(`^(\s*[-*+])\s+\[.\]\s+`)

type TriageMutation struct {
	Action       TriageAction
	ExpectedText string
	Due          string
}

type TriageItem struct {
	Line     int
	Mutation TriageMutation
}

// TriageLine applies one explicit classification to an open task. Line numbers
// are body-relative, matching the task index and ToggleLine.
func TriageLine(body []byte, lineNo int, mutation TriageMutation, now time.Time) ([]byte, error) {
	lines := strings.Split(string(body), "\n")
	if lineNo < 1 || lineNo > len(lines) {
		return nil, fmt.Errorf("line %d out of range", lineNo)
	}
	updated, err := triageLineText(lines[lineNo-1], lineNo, mutation, now)
	if err != nil {
		return nil, err
	}
	lines[lineNo-1] = updated
	return []byte(strings.Join(lines, "\n")), nil
}

func triageLineText(line string, lineNo int, mutation TriageMutation, now time.Time) (string, error) {
	m := taskLineRe.FindStringSubmatch(line)
	if m == nil {
		return "", fmt.Errorf("line %d is not a task", lineNo)
	}
	task := parseTask(m[1][0], m[2])
	if task.Status != StatusOpen {
		return "", fmt.Errorf("line %d is not an open task", lineNo)
	}
	if task.Text != mutation.ExpectedText {
		return "", fmt.Errorf("task changed; refresh before triaging")
	}
	if task.Due != "" {
		return "", fmt.Errorf("task already has a due date; refresh the triage queue")
	}

	switch mutation.Action {
	case TriageBacklog:
		if !containsTag(task.Tags, "backlog") {
			return strings.TrimRight(line, " ") + " #backlog", nil
		}
		return line, nil
	case TriageDue:
		if _, err := time.Parse("2006-01-02", mutation.Due); err != nil {
			return "", fmt.Errorf("invalid due date %q", mutation.Due)
		}
		return strings.TrimRight(line, " ") + " 📅 " + mutation.Due, nil
	case TriageReference:
		return triageTaskPrefixRe.ReplaceAllString(line, "${1} "), nil
	case TriageCancel:
		updated := strings.TrimRight(setCheckbox(line, '-'), " ")
		return updated + " ❌ " + now.Format("2006-01-02"), nil
	default:
		return "", fmt.Errorf("unsupported triage action %q", mutation.Action)
	}
}

func triageBatchBody(body []byte, items []TriageItem, now time.Time) ([]byte, error) {
	lines := strings.Split(string(body), "\n")
	seen := make(map[int]struct{}, len(items))
	for _, item := range items {
		if item.Line < 1 || item.Line > len(lines) {
			return nil, fmt.Errorf("line %d out of range", item.Line)
		}
		if _, duplicate := seen[item.Line]; duplicate {
			return nil, fmt.Errorf("line %d appears more than once", item.Line)
		}
		seen[item.Line] = struct{}{}
		updated, err := triageLineText(lines[item.Line-1], item.Line, item.Mutation, now)
		if err != nil {
			return nil, err
		}
		lines[item.Line-1] = updated
	}
	return []byte(strings.Join(lines, "\n")), nil
}

// TriageInFile applies one triage mutation through the same body-relative,
// frontmatter-preserving atomic-write contract as ToggleInFile.
func TriageInFile(vaultRoot, relPath string, line int, mutation TriageMutation, now time.Time) error {
	return TriageBatchInFile(vaultRoot, relPath, []TriageItem{{Line: line, Mutation: mutation}}, now)
}

// TriageBatchInFile applies a file-level classification in one atomic write.
// Every item is validated against the current body before anything reaches disk.
func TriageBatchInFile(vaultRoot, relPath string, items []TriageItem, now time.Time) error {
	return TriageBatchInFileAndReindex(vaultRoot, relPath, items, now, nil)
}

func TriageBatchInFileAndReindex(vaultRoot, relPath string, items []TriageItem, now time.Time, reindex func() error) error {
	if len(items) == 0 {
		return fmt.Errorf("no tasks supplied")
	}
	return mutateTaskFileAndReindex(vaultRoot, relPath, func(src []byte) ([]byte, error) {
		_, body := vault.ParseFrontmatter(src)
		prefix := src[:len(src)-len(body)]
		newBody, err := triageBatchBody(body, items, now)
		if err != nil {
			return nil, err
		}
		return append(append([]byte{}, prefix...), newBody...), nil
	}, reindex)
}

func containsTag(tags []string, want string) bool {
	for _, tag := range tags {
		if tag == want {
			return true
		}
	}
	return false
}
