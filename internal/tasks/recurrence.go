package tasks

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"github.com/base698/amythest/internal/vault"
)

// The 🔁 field's value runs until the next emoji marker (or end of line),
// matching untilNextMarker's parse. 🏁 is not in emojiMarkers but is a real
// Obsidian Tasks field, so stop there too rather than swallowing it.
var recurrenceFieldRe = regexp.MustCompile(`\s*🔁[^📅⏳🛫🔁✅❌🔺⏫🔼🔽⏬➕🏁]*`)

// ValidRecurrence reports whether the completion engine understands rule.
// Rules must carry the Obsidian "every" prefix even though the engine's
// parser is lenient about it.
func ValidRecurrence(rule string) bool {
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(rule)), "every") {
		return false
	}
	next, _ := parseRecurrence(rule)
	return next != nil
}

// UpdateRecurrenceLine sets, changes, or clears (recurrence="") the 🔁 rule
// on the task at the 1-based body line. The expected values make stale UI
// requests fail instead of changing a different task after an outside edit.
// On insert the field goes before 📅 when present — Obsidian's field order.
func UpdateRecurrenceLine(body []byte, lineNo int, expectedText, expectedStatus, expectedRecurrence, recurrence string) ([]byte, error) {
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
	if len(recurrenceFieldRe.FindAllStringIndex(line, -1)) > 1 {
		return nil, fmt.Errorf("task has multiple recurrence rules; resolve them and retry")
	}
	m := taskLineRe.FindStringSubmatch(line)
	if m == nil {
		return nil, fmt.Errorf("line %d is not a task", lineNo)
	}
	task := parseTask(m[1][0], m[2])
	if task.Text != expectedText || task.Status != expectedStatus || task.Recurrence != expectedRecurrence {
		return nil, fmt.Errorf("task changed during update; reload and retry")
	}
	recurrence = strings.TrimSpace(recurrence)
	if recurrence != "" && !ValidRecurrence(recurrence) {
		return nil, fmt.Errorf("unsupported recurrence %q (try: every 4 days, every week on wed,sat, every 2 weeks when done)", recurrence)
	}

	if task.Recurrence != "" {
		replaced := false
		updated := recurrenceFieldRe.ReplaceAllStringFunc(line, func(string) string {
			if recurrence != "" && !replaced {
				replaced = true
				return " 🔁 " + recurrence + " "
			}
			return " "
		})
		updated = strings.TrimRight(squashSpaces(updated), " ")
		lines[lineNo-1] = updated + lineEnding
		return []byte(strings.Join(lines, "\n")), nil
	}
	if recurrence == "" {
		return append([]byte(nil), body...), nil
	}
	field := " 🔁 " + recurrence
	if idx := strings.Index(line, "📅"); idx >= 0 {
		insert := strings.TrimRight(line[:idx], " ")
		lines[lineNo-1] = insert + field + " " + line[idx:] + lineEnding
	} else {
		lines[lineNo-1] = strings.TrimRight(line, " ") + field + lineEnding
	}
	return []byte(strings.Join(lines, "\n")), nil
}

// UpdateRecurrenceInFileAndReindex applies UpdateRecurrenceLine under the
// vault write lock with the same version check and atomic replace as due
// edits, preserving frontmatter.
func UpdateRecurrenceInFileAndReindex(vaultRoot, relPath string, line int, expectedText, expectedStatus, expectedRecurrence, recurrence, expectedVersion string, reindex func() error) error {
	return mutateTaskFileAndReindex(vaultRoot, relPath, func(src []byte) ([]byte, error) {
		decoded, decodeErr := hex.DecodeString(expectedVersion)
		if decodeErr != nil || len(decoded) != sha256.Size {
			return nil, fmt.Errorf("task version is required; refresh and retry")
		}
		if FileVersion(src) != expectedVersion {
			return nil, fmt.Errorf("task file changed; refresh and retry")
		}
		_, body := vault.ParseFrontmatter(src)
		prefix := src[:len(src)-len(body)]
		newBody, err := UpdateRecurrenceLine(body, line, expectedText, expectedStatus, expectedRecurrence, recurrence)
		if err != nil {
			return nil, err
		}
		return append(append([]byte{}, prefix...), newBody...), nil
	}, reindex)
}

// ReplaceNoteBodyAndReindex swaps a note's markdown body wholesale (its
// frontmatter is preserved) with the standard version check, vault write
// lock, and atomic replace.
func ReplaceNoteBodyAndReindex(vaultRoot, relPath string, newBody []byte, expectedVersion string, reindex func() error) error {
	return mutateTaskFileAndReindex(vaultRoot, relPath, func(src []byte) ([]byte, error) {
		decoded, decodeErr := hex.DecodeString(expectedVersion)
		if decodeErr != nil || len(decoded) != sha256.Size {
			return nil, fmt.Errorf("note version is required; refresh and retry")
		}
		if FileVersion(src) != expectedVersion {
			return nil, fmt.Errorf("note changed; refresh and retry")
		}
		_, body := vault.ParseFrontmatter(src)
		prefix := src[:len(src)-len(body)]
		return append(append([]byte{}, prefix...), newBody...), nil
	}, reindex)
}

// squashSpaces collapses runs of spaces left behind by field surgery while
// leaving leading indentation intact.
func squashSpaces(line string) string {
	indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
	rest := strings.TrimLeft(line, " \t")
	for strings.Contains(rest, "  ") {
		rest = strings.ReplaceAll(rest, "  ", " ")
	}
	return indent + rest
}
