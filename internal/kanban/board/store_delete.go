package board

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// ErrBoardNotEmpty is returned when permanent deletion is requested for a
// board that still contains active or archived cards.
var ErrBoardNotEmpty = errors.New("board is not empty")

// DeleteBoard permanently removes a board only after verifying that both its
// active file and archive are empty. Archiving is the normal lifecycle; this
// method exists to clean up accidental empty boards without risking card data.
func (s *Store) DeleteBoard(name string) error {
	return s.withLockMode(name, false, func() error {
		active, err := readBoard(s.boardPath(name))
		if err != nil {
			return err
		}
		archived, err := readBoard(s.donePath(name))
		if err != nil {
			return err
		}
		if len(active.Cards) != 0 || len(archived.Cards) != 0 {
			return ErrBoardNotEmpty
		}

		dir := filepath.Join(s.root, name)
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("board contains unexpected symlink %q", entry.Name())
			}
			switch entry.Name() {
			case boardFile, doneFile, legacyBoardFile, legacyDoneFile, ".lock":
				if entry.IsDir() {
					return fmt.Errorf("board contains unexpected directory %q", entry.Name())
				}
			case "attachments":
				if !entry.IsDir() {
					return fmt.Errorf("board attachments path is not a directory")
				}
				attachments, readErr := os.ReadDir(filepath.Join(dir, "attachments"))
				if readErr != nil {
					return readErr
				}
				if len(attachments) != 0 {
					return errors.New("board contains orphaned attachments")
				}
			default:
				return fmt.Errorf("board contains unexpected path %q", entry.Name())
			}
		}

		quarantine := filepath.Join(s.root, ".deleted-board-"+name+"-"+strconv.FormatInt(time.Now().UnixNano(), 10))
		if err := os.Rename(dir, quarantine); err != nil {
			return err
		}
		if err := syncDir(s.root); err != nil {
			if rollbackErr := os.Rename(quarantine, dir); rollbackErr != nil {
				return errors.Join(err, fmt.Errorf("restore board after directory sync failure: %w", rollbackErr))
			}
			_ = syncDir(s.root)
			return err
		}
		if err := os.RemoveAll(quarantine); err != nil {
			if rollbackErr := os.Rename(quarantine, dir); rollbackErr != nil {
				return errors.Join(err, fmt.Errorf("restore board after cleanup failure: %w", rollbackErr))
			}
			_ = syncDir(s.root)
			return err
		}
		return syncDir(s.root)
	})
}
