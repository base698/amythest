package tasks

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/base698/amythest/internal/vault"
)

// CancelItem identifies one open task to mark cancelled.
type CancelItem struct {
	Line         int
	ExpectedText string
}

// cancelBatchBody marks each identified open task cancelled ([-] plus a ❌
// date). Every item is validated against the current body before anything
// changes, so a stale selection fails whole rather than partially applying.
func cancelBatchBody(body []byte, items []CancelItem, now time.Time) ([]byte, error) {
	lines := strings.Split(string(body), "\n")
	seen := make(map[int]struct{}, len(items))
	for _, item := range items {
		if item.Line < 1 || item.Line > len(lines) {
			return nil, fmt.Errorf("line %d out of range", item.Line)
		}
		if _, dup := seen[item.Line]; dup {
			return nil, fmt.Errorf("line %d appears more than once", item.Line)
		}
		seen[item.Line] = struct{}{}
		line := lines[item.Line-1]
		ending := ""
		if strings.HasSuffix(line, "\r") {
			line = strings.TrimSuffix(line, "\r")
			ending = "\r"
		}
		m := taskLineRe.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("line %d is not a task", item.Line)
		}
		task := parseTask(m[1][0], m[2])
		if task.Status != StatusOpen || task.Text != item.ExpectedText {
			return nil, fmt.Errorf("task changed during update; reload and retry")
		}
		lines[item.Line-1] = strings.TrimRight(setCheckbox(line, '-'), " ") + " ❌ " + now.Format("2006-01-02") + ending
	}
	return []byte(strings.Join(lines, "\n")), nil
}

// CancelTasksInFileAndReindex marks open tasks cancelled under the shared
// stale-safe atomic writer. Unlike triage cancel it accepts any open task,
// dated or not; the whole-file version guard pins the exact content the
// caller saw.
func CancelTasksInFileAndReindex(vaultRoot, relPath string, items []CancelItem, expectedVersion string, now time.Time, reindex func() error) error {
	if len(items) == 0 {
		return fmt.Errorf("no tasks supplied")
	}
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
		newBody, err := cancelBatchBody(body, items, now)
		if err != nil {
			return nil, err
		}
		return append(append([]byte{}, prefix...), newBody...), nil
	}, reindex)
}
