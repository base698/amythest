package tasks

import (
	"fmt"
	"html"
	"regexp"
	"strings"
)

// Annotate adds inert quoted context below an exact task snapshot. The caller
// must save with the note's version; shifted/edited tasks are never guessed at.
func Annotate(body string, target Task, text string) (string, error) {
	lines := strings.Split(body, "\n")
	if target.Line < 1 || target.Line > len(lines) {
		return "", fmt.Errorf("task moved; refresh and retry")
	}
	t, ok := ParseLine(lines[target.Line-1], target.Slug, target.Path, target.Line, "")
	if !ok || t.Text != target.Text || t.Status != target.Status {
		return "", fmt.Errorf("task changed; refresh and retry")
	}
	raw := lines[target.Line-1]
	indent := raw[:len(raw)-len(strings.TrimLeft(raw, " \t"))] + "  > "
	annotation := []string{indent + "<!-- amy-context -->"}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r", ""), "\n") {
		annotation = append(annotation, indent+line)
	}
	annotation = append(annotation, indent+"<!-- /amy-context -->")
	result := append([]string{}, lines[:target.Line]...)
	result = append(result, annotation...)
	result = append(result, lines[target.Line:]...)
	return strings.Join(result, "\n"), nil
}

var contextMarker = regexp.MustCompile(`<!-- (?:/?amy-context|amy-message:[A-Za-z0-9-]+) -->`)

// ContextText hides bookkeeping comments in task and terminal note previews.
func ContextText(text string) string {
	return html.UnescapeString(contextMarker.ReplaceAllString(text, ""))
}

// ownedContextEnd returns the byte length of one complete generated block. It
// cannot cross an ordinary line, another task, or a differently indented block.
func ownedContextEnd(rest string) int {
	first, _, _ := strings.Cut(rest, "\n")
	first = strings.TrimSuffix(first, "\r")
	if strings.TrimSpace(first) != "> <!-- amy-context -->" {
		return 0
	}
	prefix := strings.TrimSuffix(first, "<!-- amy-context -->")
	offset := 0
	for raw := range strings.Lines(rest) {
		line := strings.TrimRight(raw, "\r\n")
		if !strings.HasPrefix(line, prefix) {
			return 0
		}
		offset += len(raw)
		if line == prefix+"<!-- /amy-context -->" {
			return offset
		}
	}
	return 0
}
