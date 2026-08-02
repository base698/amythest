//go:build linux

package tasks

import (
	"errors"
	"fmt"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// openVaultRoot resolves the configured root in one kernel path walk and
// rejects symlinks in every component. This avoids validating one pathname and
// then reopening a different directory after an ancestor swap.
func openVaultRoot(vaultRoot string) (int, error) {
	root, err := filepath.Abs(vaultRoot)
	if err != nil {
		return -1, err
	}
	how := &unix.OpenHow{
		Flags:   uint64(unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC),
		Resolve: uint64(unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS),
	}
	return unix.Openat2(unix.AT_FDCWD, root, how)
}

func renameat2(parentFrom int, from string, parentTo int, to string, flags uint) error {
	err := unix.Renameat2(parentFrom, from, parentTo, to, flags)
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.EOPNOTSUPP) {
		return fmt.Errorf("vault filesystem does not support the required safe atomic rename operation: %w", err)
	}
	return err
}

// renameExchangeAt atomically swaps from and to; both must exist.
func renameExchangeAt(parentFrom int, from string, parentTo int, to string) error {
	return renameat2(parentFrom, from, parentTo, to, unix.RENAME_EXCHANGE)
}

// renameNoReplaceAt renames from to to, failing with EEXIST if to exists.
func renameNoReplaceAt(parentFrom int, from string, parentTo int, to string) error {
	return renameat2(parentFrom, from, parentTo, to, unix.RENAME_NOREPLACE)
}
