//go:build darwin

package tasks

import (
	"errors"
	"fmt"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// openVaultRoot resolves the configured root while rejecting symlinks in every
// component (O_NOFOLLOW_ANY), matching the guarantee openat2 with
// RESOLVE_NO_SYMLINKS provides on Linux. Callers must pass a canonical root
// (the server resolves it at scan time); macOS system paths like /var and /tmp
// are themselves symlinks and are rejected unresolved.
func openVaultRoot(vaultRoot string) (int, error) {
	root, err := filepath.Abs(vaultRoot)
	if err != nil {
		return -1, err
	}
	return unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW_ANY, 0)
}

func renameatxNp(parentFrom int, from string, parentTo int, to string, flags uint32) error {
	err := unix.RenameatxNp(parentFrom, from, parentTo, to, flags)
	if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOSYS) {
		return fmt.Errorf("vault filesystem does not support the required safe atomic rename operation: %w", err)
	}
	return err
}

// renameExchangeAt atomically swaps from and to; both must exist.
func renameExchangeAt(parentFrom int, from string, parentTo int, to string) error {
	return renameatxNp(parentFrom, from, parentTo, to, unix.RENAME_SWAP)
}

// renameNoReplaceAt renames from to to, failing with EEXIST if to exists.
func renameNoReplaceAt(parentFrom int, from string, parentTo int, to string) error {
	return renameatxNp(parentFrom, from, parentTo, to, unix.RENAME_EXCL)
}
