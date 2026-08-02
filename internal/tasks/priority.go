package tasks

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/base698/amythest/internal/vault"
)

// PriorityNone is the implicit level: Obsidian Tasks writes no emoji for it.
const PriorityNone = 3

// priorityMarker maps a level to its Obsidian Tasks emoji.
var priorityMarker = [6]string{"🔺", "⏫", "🔼", "", "🔽", "⏬"}

// ValidPriority reports whether p is a level this vocabulary can express.
func ValidPriority(p int) bool { return p >= 0 && p < len(priorityMarker) }

// UpdatePriorityLine rewrites one task's priority emoji. The marker is placed
// immediately after the description — where Obsidian Tasks renders it —
// rather than appended, so round-tripping a line through the UI does not
// reorder its metadata.
func UpdatePriorityLine(body []byte, lineNo int, expectedText, expectedStatus string, expectedPriority, priority int) ([]byte, error) {
	if !ValidPriority(priority) {
		return nil, fmt.Errorf("priority must be between 0 and 5")
	}
	lines := strings.Split(string(body), "\n")
	if lineNo < 1 || lineNo > len(lines) {
		return nil, fmt.Errorf("line %d out of range", lineNo)
	}
	line := lines[lineNo-1]
	ending := ""
	if strings.HasSuffix(line, "\r") {
		line = strings.TrimSuffix(line, "\r")
		ending = "\r"
	}
	m := taskLineRe.FindStringSubmatchIndex(line)
	if m == nil {
		return nil, fmt.Errorf("line %d is not a task", lineNo)
	}
	rest := line[m[4]:m[5]]
	task := parseTask(line[m[2]], rest)
	if task.Text != expectedText || task.Status != expectedStatus || task.Priority != expectedPriority {
		return nil, fmt.Errorf("task changed during update; reload and retry")
	}
	if priority == expectedPriority {
		return append([]byte(nil), body...), nil
	}

	cleaned := rest
	for _, marker := range priorityMarker {
		if marker != "" {
			cleaned = strings.ReplaceAll(cleaned, marker, "")
		}
	}
	// Metadata begins at the first remaining marker; the priority slots in
	// just before it so dates and recurrence keep their order.
	cut := len(cleaned)
	for _, em := range emojiMarkers {
		if i := strings.Index(cleaned, em.marker); i >= 0 && i < cut {
			cut = i
		}
	}
	head := strings.TrimRight(cleaned[:cut], " ")
	tail := strings.TrimLeft(cleaned[cut:], " ")
	updated := head
	if marker := priorityMarker[priority]; marker != "" {
		updated += " " + marker
	}
	if tail != "" {
		updated += " " + tail
	}
	lines[lineNo-1] = line[:m[4]] + strings.TrimRight(updated, " ") + ending
	return []byte(strings.Join(lines, "\n")), nil
}

// UpdatePriorityInFileAndReindex applies a priority change through the shared
// stale-safe atomic writer, guarded by the whole-file version.
func UpdatePriorityInFileAndReindex(vaultRoot, relPath string, line int, expectedText, expectedStatus string, expectedPriority, priority int, expectedVersion string, reindex func() error) error {
	return mutateTaskFileAndReindex(vaultRoot, relPath, func(src []byte) ([]byte, error) {
		decoded, err := hex.DecodeString(expectedVersion)
		if err != nil || len(decoded) != sha256.Size {
			return nil, fmt.Errorf("task version is required; refresh and retry")
		}
		if FileVersion(src) != expectedVersion {
			return nil, fmt.Errorf("task file changed; refresh and retry")
		}
		_, body := vault.ParseFrontmatter(src)
		prefix := src[:len(src)-len(body)]
		newBody, err := UpdatePriorityLine(body, line, expectedText, expectedStatus, expectedPriority, priority)
		if err != nil {
			return nil, err
		}
		return append(append([]byte{}, prefix...), newBody...), nil
	}, reindex)
}
