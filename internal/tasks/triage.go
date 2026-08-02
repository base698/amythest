package tasks

import (
	"fmt"
	"os"
	"path/filepath"
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
	line := lines[lineNo-1]
	m := taskLineRe.FindStringSubmatch(line)
	if m == nil {
		return nil, fmt.Errorf("line %d is not a task", lineNo)
	}
	task := parseTask(m[1][0], m[2])
	if task.Status != StatusOpen {
		return nil, fmt.Errorf("line %d is not an open task", lineNo)
	}
	if task.Text != mutation.ExpectedText {
		return nil, fmt.Errorf("task changed; refresh before triaging")
	}
	if task.Due != "" {
		return nil, fmt.Errorf("task already has a due date; refresh the triage queue")
	}

	switch mutation.Action {
	case TriageBacklog:
		if !containsTag(task.Tags, "backlog") {
			lines[lineNo-1] = strings.TrimRight(line, " ") + " #backlog"
		}
	case TriageDue:
		if task.Due != "" {
			return nil, fmt.Errorf("task already has a due date")
		}
		if _, err := time.Parse("2006-01-02", mutation.Due); err != nil {
			return nil, fmt.Errorf("invalid due date %q", mutation.Due)
		}
		lines[lineNo-1] = strings.TrimRight(line, " ") + " 📅 " + mutation.Due
	case TriageReference:
		lines[lineNo-1] = triageTaskPrefixRe.ReplaceAllString(line, "${1} ")
	case TriageCancel:
		updated := strings.TrimRight(setCheckbox(line, '-'), " ")
		lines[lineNo-1] = updated + " ❌ " + now.Format("2006-01-02")
	default:
		return nil, fmt.Errorf("unsupported triage action %q", mutation.Action)
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
	if len(items) == 0 {
		return fmt.Errorf("no tasks supplied")
	}
	abs := filepath.Join(vaultRoot, filepath.FromSlash(relPath))
	src, err := os.ReadFile(abs)
	if err != nil {
		return err
	}
	_, body := vault.ParseFrontmatter(src)
	prefix := src[:len(src)-len(body)]

	newBody := body
	for _, item := range items {
		newBody, err = TriageLine(newBody, item.Line, item.Mutation, now)
		if err != nil {
			return err
		}
	}
	info, err := os.Stat(abs)
	if err != nil {
		return err
	}
	tmp := abs + ".amythest-tmp"
	out := append(append([]byte{}, prefix...), newBody...)
	if err := os.WriteFile(tmp, out, info.Mode().Perm()); err != nil {
		return err
	}
	if err := os.Rename(tmp, abs); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func containsTag(tags []string, want string) bool {
	for _, tag := range tags {
		if tag == want {
			return true
		}
	}
	return false
}
