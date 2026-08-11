package tasks

import (
	"bytes"
	"errors"
	"strings"

	"golang.org/x/sys/unix"
)

// AppendTaskAndReindex appends a "- [ ] text" line to the note at relPath,
// creating the file (and missing parent folders) when it does not exist yet —
// the daily-note case. Existing content is preserved byte-for-byte apart from
// normalizing the trailing newline before the appended line.
func AppendTaskAndReindex(vaultRoot, relPath, text string, reindex func() error) error {
	line := "- [ ] " + strings.TrimSpace(text)
	rootFD, release, err := acquireTaskVaultWriteLockFD(vaultRoot)
	if err != nil {
		return err
	}
	defer release()
	parentFD, leaf, err := openParentAt(rootFD, relPath, true)
	if err != nil {
		return err
	}
	defer unix.Close(parentFD)

	src, mode, err := readRegularAt(parentFD, leaf)
	switch {
	case err == nil:
		updated := []byte(line + "\n")
		if len(bytes.TrimSpace(src)) > 0 {
			updated = append(append(bytes.TrimRight(src, "\n"), '\n'), updated...)
		}
		if err := replaceTaskFileAt(parentFD, leaf, src, updated, mode); err != nil {
			return err
		}
	case errors.Is(err, unix.ENOENT):
		// New note: create atomically under the vault write lock.
		tmp, createErr := createTempAt(parentFD, leaf, []byte(line+"\n"), 0o644)
		if createErr != nil {
			return createErr
		}
		if renameErr := unix.Renameat(parentFD, tmp, parentFD, leaf); renameErr != nil {
			_ = unix.Unlinkat(parentFD, tmp, 0)
			return renameErr
		}
		_ = unix.Fsync(parentFD)
	default:
		return err
	}
	if reindex != nil {
		return reindex()
	}
	return nil
}
