package tasks

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/base698/amythest/internal/vault"
	"golang.org/x/sys/unix"
)

// MoveTaskInput is the complete stale-state identity rendered for a task row.
type MoveTaskInput struct {
	Line            int
	ExpectedText    string
	ExpectedStatus  string
	ExpectedVersion string
}

// CreatedTaskReference describes the card created after source validation. Its
// rollback removes exactly that card if the source write or reindex fails.
type CreatedTaskReference struct {
	Reference string
	Rollback  func() error
}

type CreateTaskReference func(Task) (CreatedTaskReference, error)

var movableTaskLineRe = regexp.MustCompile(`^(\s*)[-*+]\s+\[(.)\]\s+(.*)$`)

var replaceTaskFileForMove = replaceTaskFileAtResult

// MoveTaskToBoardInFileAndReindex validates the exact source snapshot before
// creating a card, then atomically converts the checkbox into a reference. On a
// source-write or reindex failure it restores the source before deleting the
// newly created card, so a missing source task always retains a card.
func MoveTaskToBoardInFileAndReindex(vaultRoot, relPath string, input MoveTaskInput, create CreateTaskReference, reindex func() error) error {
	decoded, err := hex.DecodeString(input.ExpectedVersion)
	if err != nil || len(decoded) != sha256.Size {
		return fmt.Errorf("task version is required; refresh and retry")
	}
	if input.Line < 1 || input.ExpectedStatus == "" || create == nil {
		return fmt.Errorf("task identity is incomplete; refresh and retry")
	}

	rootFD, release, err := acquireTaskVaultWriteLockFD(vaultRoot)
	if err != nil {
		return err
	}
	defer release()
	parentFD, leaf, err := openParentAt(rootFD, relPath, false)
	if err != nil {
		return err
	}
	defer unix.Close(parentFD)
	src, mode, err := readRegularAt(parentFD, leaf)
	if err != nil {
		return err
	}
	if FileVersion(src) != input.ExpectedVersion {
		return fmt.Errorf("task file changed; refresh and retry")
	}
	fm, body := vault.ParseFrontmatter(src)
	if fm.Err != nil {
		return fmt.Errorf("malformed frontmatter: %w", fm.Err)
	}
	parsed, _ := ParseFile("", relPath, body)
	var current *Task
	for i := range parsed {
		if parsed[i].Line == input.Line {
			current = &parsed[i]
			break
		}
	}
	if current == nil {
		return fmt.Errorf("source line is no longer a task; refresh and retry")
	}
	if current.Text != input.ExpectedText || current.Status != input.ExpectedStatus {
		return fmt.Errorf("task text or status changed; refresh and retry")
	}
	if current.Status != StatusOpen {
		return fmt.Errorf("only open tasks can be moved to a board")
	}

	lineStart, lineEnd, line, err := bodyLine(body, input.Line)
	if err != nil {
		return err
	}
	carriage := ""
	lineText := string(line)
	if strings.HasSuffix(lineText, "\r") {
		carriage = "\r"
		lineText = strings.TrimSuffix(lineText, "\r")
	}
	match := movableTaskLineRe.FindStringSubmatch(lineText)
	if match == nil {
		return fmt.Errorf("source line is no longer a task; refresh and retry")
	}

	created, err := create(*current)
	if err != nil {
		return err
	}
	rollbackCard := func() error {
		if created.Rollback == nil {
			return nil
		}
		return created.Rollback()
	}
	if created.Reference == "" || strings.ContainsAny(created.Reference, "\r\n") {
		return errors.Join(fmt.Errorf("created card returned an invalid reference"), rollbackCard())
	}
	replacement := []byte(match[1] + "- " + match[3] + " → " + created.Reference + carriage)
	newBody := make([]byte, 0, len(body)-len(line)+len(replacement))
	newBody = append(newBody, body[:lineStart]...)
	newBody = append(newBody, replacement...)
	newBody = append(newBody, body[lineEnd:]...)
	prefix := src[:len(src)-len(body)]
	updated := append(append([]byte{}, prefix...), newBody...)
	committed, replaceErr := replaceTaskFileForMove(parentFD, leaf, src, updated, mode)
	if replaceErr != nil {
		if committed {
			// The reference is already installed; retaining the card is the only
			// state that cannot lose work or permit an identical retry.
			return replaceErr
		}
		return errors.Join(replaceErr, rollbackCard())
	}
	if reindex == nil {
		return nil
	}
	if err := reindex(); err != nil {
		restored, restoreErr := replaceTaskFileAtResult(parentFD, leaf, updated, src, mode)
		if !restored {
			// Keep the card when source restoration fails: this preserves the
			// no-partial-loss invariant even under an uncooperative editor race.
			return errors.Join(err, fmt.Errorf("restore source after reindex failure: %w", restoreErr))
		}
		cardRollbackErr := rollbackCard()
		if cardRollbackErr == nil {
			return errors.Join(err, restoreErr)
		}
		// The card still exists, so reapply its reference before returning.
		// This leaves the original task identity stale and prevents a retry
		// from creating a second card.
		reapplied, reapplyErr := replaceTaskFileAtResult(parentFD, leaf, src, updated, mode)
		if !reapplied {
			return errors.Join(err, restoreErr, cardRollbackErr, fmt.Errorf("reapply card reference: %w", reapplyErr))
		}
		return errors.Join(err, restoreErr, cardRollbackErr, reapplyErr)
	}
	return nil
}

// bodyLine returns one body-relative line excluding its LF, while lineEnd
// points at the LF so replacement preserves it.
func bodyLine(body []byte, line int) (lineStart, lineEnd int, content []byte, err error) {
	lineStart = 0
	for current := 1; current < line; current++ {
		i := bytes.IndexByte(body[lineStart:], '\n')
		if i < 0 {
			return 0, 0, nil, fmt.Errorf("task line %d is out of range", line)
		}
		lineStart += i + 1
	}
	if lineStart > len(body) {
		return 0, 0, nil, fmt.Errorf("task line %d is out of range", line)
	}
	if i := bytes.IndexByte(body[lineStart:], '\n'); i >= 0 {
		lineEnd = lineStart + i
		return lineStart, lineEnd, body[lineStart:lineEnd], nil
	}
	lineEnd = len(body)
	return lineStart, lineEnd, body[lineStart:lineEnd], nil
}
