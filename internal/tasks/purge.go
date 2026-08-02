package tasks

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/base698/amythest/internal/vault"
)

// purgeCancelledBody removes whole lines holding cancelled tasks. Only
// cancelled tasks may be purged — deletion stays a two-step, recoverable
// flow (cancel first, purge later). Indented child lines are left in place.
func purgeCancelledBody(body []byte, lineNos []int) ([]byte, error) {
	sorted := append([]int(nil), lineNos...)
	sort.Sort(sort.Reverse(sort.IntSlice(sorted)))
	prev := 0
	for _, lineNo := range sorted {
		if lineNo == prev {
			return nil, fmt.Errorf("line %d appears more than once", lineNo)
		}
		prev = lineNo
	}
	// Validate everything before removing anything.
	for _, lineNo := range sorted {
		_, _, content, err := bodyLine(body, lineNo)
		if err != nil {
			return nil, err
		}
		m := taskLineRe.FindStringSubmatch(strings.TrimSuffix(string(content), "\r"))
		if m == nil {
			return nil, fmt.Errorf("line %d is not a task", lineNo)
		}
		if parseTask(m[1][0], m[2]).Status != StatusCancelled {
			return nil, fmt.Errorf("line %d is not cancelled; cancel it before purging", lineNo)
		}
	}
	// Remove bottom-up so earlier offsets stay valid.
	out := append([]byte(nil), body...)
	for _, lineNo := range sorted {
		start, end, _, err := bodyLine(out, lineNo)
		if err != nil {
			return nil, err
		}
		cut := end
		if cut < len(out) {
			cut++ // consume the line's LF
		}
		out = append(out[:start], out[cut:]...)
	}
	return out, nil
}

// PurgeCancelledInFileAndReindex permanently removes cancelled task lines
// under the shared stale-safe atomic writer with the whole-file version
// guard. This is the only destructive task mutation; everything else in this
// package converts lines rather than deleting them.
func PurgeCancelledInFileAndReindex(vaultRoot, relPath string, lineNos []int, expectedVersion string, reindex func() error) error {
	if len(lineNos) == 0 {
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
		newBody, err := purgeCancelledBody(body, lineNos)
		if err != nil {
			return nil, err
		}
		return append(append([]byte{}, prefix...), newBody...), nil
	}, reindex)
}
