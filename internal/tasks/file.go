package tasks

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/base698/amythest/internal/vault"
	"golang.org/x/sys/unix"
	"gopkg.in/yaml.v3"
)

var taskFileWriteMu sync.Mutex

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

// The root directory inode is the stable cross-process lock. Unlike a lock file
// in TMPDIR, all Amythest processes using the same resolved vault necessarily
// contend on the same object and no artifact is left in the vault.
func acquireTaskVaultWriteLockFD(vaultRoot string) (int, func(), error) {
	taskFileWriteMu.Lock()
	fd, err := openVaultRoot(vaultRoot)
	if err != nil {
		taskFileWriteMu.Unlock()
		return -1, nil, err
	}
	if err := unix.Flock(fd, unix.LOCK_EX); err != nil {
		_ = unix.Close(fd)
		taskFileWriteMu.Unlock()
		return -1, nil, err
	}
	release := func() {
		_ = unix.Flock(fd, unix.LOCK_UN)
		_ = unix.Close(fd)
		taskFileWriteMu.Unlock()
	}
	return fd, release, nil
}

func acquireTaskVaultWriteLock(vaultRoot string) (func(), error) {
	_, release, err := acquireTaskVaultWriteLockFD(vaultRoot)
	return release, err
}

func cleanVaultRelativePath(relPath string) (string, error) {
	if relPath == "" || filepath.IsAbs(relPath) || strings.ContainsRune(relPath, '\x00') {
		return "", fmt.Errorf("invalid vault path %q", relPath)
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(relPath)))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("vault path %q escapes the vault", relPath)
	}
	for _, part := range strings.Split(clean, "/") {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("invalid vault path %q", relPath)
		}
	}
	return clean, nil
}

// openParentAt walks directory components without following symlinks. The
// returned descriptor pins the actual directory through the final operation,
// eliminating pathname re-resolution between validation and rename.
func openParentAt(rootFD int, relPath string, create bool) (int, string, error) {
	clean, err := cleanVaultRelativePath(relPath)
	if err != nil {
		return -1, "", err
	}
	parts := strings.Split(clean, "/")
	leaf := parts[len(parts)-1]
	fd, err := unix.Dup(rootFD)
	if err != nil {
		return -1, "", err
	}
	for _, part := range parts[:len(parts)-1] {
		next, openErr := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if errors.Is(openErr, unix.ENOENT) && create {
			if mkdirErr := unix.Mkdirat(fd, part, 0o755); mkdirErr != nil && !errors.Is(mkdirErr, unix.EEXIST) {
				_ = unix.Close(fd)
				return -1, "", mkdirErr
			}
			next, openErr = unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		}
		_ = unix.Close(fd)
		if openErr != nil {
			return -1, "", openErr
		}
		fd = next
	}
	return fd, leaf, nil
}

func readRegularAt(parentFD int, leaf string) ([]byte, os.FileMode, error) {
	fd, err := unix.Openat(parentFD, leaf, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, 0, err
	}
	file := os.NewFile(uintptr(fd), leaf)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, 0, err
	}
	if !info.Mode().IsRegular() {
		return nil, 0, fmt.Errorf("%s is not a regular file", leaf)
	}
	content, err := io.ReadAll(file)
	return content, info.Mode().Perm(), err
}

// ReadNoteFile reads a regular vault-relative file without following any path
// component symlink. It is used to render stale-state versions for file-wide
// controls without exposing files outside the configured vault.
func ReadNoteFile(vaultRoot, relPath string) ([]byte, error) {
	rootFD, err := openVaultRoot(vaultRoot)
	if err != nil {
		return nil, err
	}
	defer unix.Close(rootFD)
	parentFD, leaf, err := openParentAt(rootFD, relPath, false)
	if err != nil {
		return nil, err
	}
	defer unix.Close(parentFD)
	content, _, err := readRegularAt(parentFD, leaf)
	return content, err
}

func createTempAt(parentFD int, leaf string, content []byte, mode os.FileMode) (string, error) {
	for attempt := 0; attempt < 32; attempt++ {
		var nonce [12]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return "", err
		}
		name := fmt.Sprintf(".%s.amythest-%x", leaf, nonce[:])
		fd, err := unix.Openat(parentFD, name, unix.O_CREAT|unix.O_EXCL|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, uint32(mode.Perm()))
		if errors.Is(err, unix.EEXIST) {
			continue
		}
		if err != nil {
			return "", err
		}
		if err := unix.Fchmod(fd, uint32(mode.Perm())); err != nil {
			_ = unix.Close(fd)
			_ = unix.Unlinkat(parentFD, name, 0)
			return "", err
		}
		file := os.NewFile(uintptr(fd), name)
		ok := false
		defer func() {
			if !ok {
				_ = unix.Unlinkat(parentFD, name, 0)
			}
		}()
		if _, err := file.Write(content); err != nil {
			_ = file.Close()
			return "", err
		}
		if err := file.Sync(); err != nil {
			_ = file.Close()
			return "", err
		}
		if err := file.Close(); err != nil {
			return "", err
		}
		ok = true
		return name, nil
	}
	return "", fmt.Errorf("could not allocate a unique temporary file")
}

func renameat2(parentFrom int, from string, parentTo int, to string, flags uint) error {
	err := unix.Renameat2(parentFrom, from, parentTo, to, flags)
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.EOPNOTSUPP) {
		return fmt.Errorf("vault filesystem does not support the required safe atomic rename operation: %w", err)
	}
	return err
}

// rollbackTaskExchangeAt restores the version displaced by the first exchange.
// The second exchange can itself race with an uncooperative external editor, so
// it inspects (rather than blindly deletes) the file displaced by the rollback.
// A second external version is left at its random temporary name and surfaced
// to the caller instead of being silently lost.
func rollbackTaskExchangeAt(parentFD int, leaf, tmp string, updated []byte) (string, error) {
	if err := renameat2(parentFD, tmp, parentFD, leaf, unix.RENAME_EXCHANGE); err != nil {
		return tmp, fmt.Errorf("rollback exchange failed: %w", err)
	}
	displaced, _, err := readRegularAt(parentFD, tmp)
	if err != nil {
		return tmp, fmt.Errorf("rollback restored the prior file but could not inspect %s: %w", tmp, err)
	}
	if !bytes.Equal(displaced, updated) {
		_ = unix.Fsync(parentFD)
		return tmp, fmt.Errorf("file changed again during rollback; concurrent version preserved as %s", tmp)
	}
	if err := unix.Unlinkat(parentFD, tmp, 0); err != nil {
		return tmp, fmt.Errorf("rollback restored the prior file but could not remove %s: %w", tmp, err)
	}
	_ = unix.Fsync(parentFD)
	return "", nil
}

// replaceTaskFileAtResult atomically exchanges the prepared replacement with
// the target and reports whether updated is durably installed at leaf. A
// post-exchange cleanup error can therefore be distinguished from a failed
// commit by composite operations that have already created another artifact.
func replaceTaskFileAtResult(parentFD int, leaf string, expected, updated []byte, mode os.FileMode) (bool, error) {
	tmp, err := createTempAt(parentFD, leaf, updated, mode)
	if err != nil {
		return false, err
	}
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = unix.Unlinkat(parentFD, tmp, 0)
		}
	}()
	if err := renameat2(parentFD, tmp, parentFD, leaf, unix.RENAME_EXCHANGE); err != nil {
		return false, err
	}
	old, _, readErr := readRegularAt(parentFD, tmp)
	if readErr == nil && bytes.Equal(old, expected) {
		if err := unix.Unlinkat(parentFD, tmp, 0); err != nil {
			// The exchange already committed updated at leaf. Report that state
			// so callers never delete a paired card after a cleanup-only failure.
			return true, err
		}
		removeTemp = false
		_ = unix.Fsync(parentFD)
		return true, nil
	}
	removeTemp = false
	preserved, rollbackErr := rollbackTaskExchangeAt(parentFD, leaf, tmp, updated)
	if rollbackErr != nil {
		current, _, currentErr := readRegularAt(parentFD, leaf)
		committed := currentErr == nil && bytes.Equal(current, updated)
		return committed, fmt.Errorf("task file changed; %v (original read error: %v; preserved path: %s)", rollbackErr, readErr, preserved)
	}
	if readErr != nil {
		return false, fmt.Errorf("task file changed during update; restored original: %w", readErr)
	}
	return false, fmt.Errorf("task file changed during update; reload and retry")
}

func replaceTaskFileAt(parentFD int, leaf string, expected, updated []byte, mode os.FileMode) error {
	_, err := replaceTaskFileAtResult(parentFD, leaf, expected, updated, mode)
	return err
}

func replaceExistingNoteFileAt(parentFD int, leaf string, expected, updated []byte, mode os.FileMode) error {
	return replaceTaskFileAt(parentFD, leaf, expected, updated, mode)
}

// WithVaultWriteLock runs fn while holding the shared in-process and
// cross-process vault critical section. Callers already inside a mutation must
// use their operation's AndReindex callback instead of re-entering this lock.
func WithVaultWriteLock(vaultRoot string, fn func() error) error {
	_, release, err := acquireTaskVaultWriteLockFD(vaultRoot)
	if err != nil {
		return err
	}
	defer release()
	return fn()
}

func mutateTaskFileAndReindex(vaultRoot, relPath string, mutate func([]byte) ([]byte, error), reindex func() error) error {
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
	updated, err := mutate(src)
	if err != nil {
		return err
	}
	if err := replaceTaskFileAt(parentFD, leaf, src, updated, mode); err != nil {
		return err
	}
	if reindex != nil {
		return reindex()
	}
	return nil
}

func mutateTaskFile(vaultRoot, relPath string, mutate func([]byte) ([]byte, error)) error {
	return mutateTaskFileAndReindex(vaultRoot, relPath, mutate, nil)
}

// FileVersion is the stable content identity rendered with destructive file
// controls and checked again while holding the vault writer lock.
func FileVersion(content []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(content))
}

// HideFileFromTasks sets the canonical top-level tasks property to false using
// the same stale-safe, mode-preserving, no-follow writer as task mutations.
func HideFileFromTasks(vaultRoot, relPath, expectedVersion string) error {
	return HideFileFromTasksAndReindex(vaultRoot, relPath, expectedVersion, nil)
}

func HideFileFromTasksAndReindex(vaultRoot, relPath, expectedVersion string, reindex func() error) error {
	return mutateTaskFileAndReindex(vaultRoot, relPath, func(src []byte) ([]byte, error) {
		decoded, err := hex.DecodeString(expectedVersion)
		if err != nil || len(decoded) != sha256.Size {
			return nil, fmt.Errorf("file version is required; refresh and retry")
		}
		if FileVersion(src) != expectedVersion {
			return nil, fmt.Errorf("file changed; refresh and retry")
		}
		return setTasksDisabledFrontmatter(src)
	}, reindex)
}

func setTasksDisabledFrontmatter(src []byte) ([]byte, error) {
	newline := []byte("\n")
	if bytes.Contains(src, []byte("\r\n")) {
		newline = []byte("\r\n")
	}
	fm, body := vault.ParseFrontmatter(src)
	hasOpeningDelimiter := bytes.HasPrefix(src, []byte("---\n")) || bytes.HasPrefix(src, []byte("---\r\n"))
	if hasOpeningDelimiter && len(body) == len(src) {
		return nil, fmt.Errorf("malformed frontmatter: missing closing delimiter")
	}
	if fm.Err != nil {
		return nil, fmt.Errorf("malformed frontmatter: %w", fm.Err)
	}
	if hasOpeningDelimiter && frontmatterOnlyComments(fm.Raw) {
		out := append([]byte("---"), newline...)
		raw := []byte(fm.Raw)
		out = append(out, raw...)
		if len(raw) > 0 && !bytes.HasSuffix(raw, newline) {
			out = append(out, newline...)
		}
		out = append(out, []byte("tasks: false")...)
		out = append(out, newline...)
		out = append(out, []byte("---")...)
		out = append(out, newline...)
		out = append(out, body...)
		return out, nil
	}

	var document yaml.Node
	if hasOpeningDelimiter {
		if err := yaml.Unmarshal([]byte(fm.Raw), &document); err != nil {
			return nil, fmt.Errorf("malformed frontmatter: %w", err)
		}
	} else {
		document = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
		body = src
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("malformed frontmatter: top-level YAML must be a mapping")
	}
	mapping := document.Content[0]
	found := false
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value != "tasks" {
			continue
		}
		found = true
		value := mapping.Content[i+1]
		if value.Kind == yaml.ScalarNode && value.Tag == "!!bool" && value.Value == "false" {
			return append([]byte(nil), src...), nil
		}
		*value = yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "false"}
	}
	if !found {
		mapping.Content = append(mapping.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "tasks"},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "false"},
		)
	}
	var encoded bytes.Buffer
	encoder := yaml.NewEncoder(&encoded)
	encoder.SetIndent(2)
	if err := encoder.Encode(&document); err != nil {
		return nil, fmt.Errorf("encode frontmatter: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("encode frontmatter: %w", err)
	}
	yamlBytes := bytes.TrimSuffix(encoded.Bytes(), []byte("\n"))
	if bytes.Equal(newline, []byte("\r\n")) {
		yamlBytes = bytes.ReplaceAll(yamlBytes, []byte("\n"), newline)
	}
	out := append([]byte("---"), newline...)
	out = append(out, yamlBytes...)
	out = append(out, newline...)
	out = append(out, []byte("---")...)
	out = append(out, newline...)
	out = append(out, body...)
	return out, nil
}

func frontmatterOnlyComments(raw string) bool {
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			return false
		}
	}
	return true
}

type FileDisposition string

const (
	FileDispositionArchive FileDisposition = "archive"
	FileDispositionTrash   FileDisposition = "trash"
)

func dispositionDestination(source string, disposition FileDisposition) (string, error) {
	source, err := cleanVaultRelativePath(source)
	if err != nil {
		return "", err
	}
	if !strings.HasSuffix(strings.ToLower(source), ".md") {
		return "", fmt.Errorf("only markdown notes may be moved")
	}
	switch disposition {
	case FileDispositionArchive:
		return "Archive/Deleted/" + source, nil
	case FileDispositionTrash:
		return ".trash/Amythest/" + source, nil
	default:
		return "", fmt.Errorf("unsupported file disposition %q", disposition)
	}
}

// MoveFileInVault performs a recoverable same-filesystem move. It never
// overwrites a destination and verifies the exact source version after rename;
// a stale source is atomically restored whenever the original path is free.
func MoveFileInVault(vaultRoot, source string, disposition FileDisposition, expectedVersion string) (string, error) {
	return MoveFileInVaultAndReindex(vaultRoot, source, disposition, expectedVersion, nil)
}

func MoveFileInVaultAndReindex(vaultRoot, source string, disposition FileDisposition, expectedVersion string, reindex func() error) (string, error) {
	destination, err := dispositionDestination(source, disposition)
	if err != nil {
		return "", err
	}
	decodedVersion, decodeErr := hex.DecodeString(expectedVersion)
	if decodeErr != nil || len(decodedVersion) != sha256.Size {
		return "", fmt.Errorf("file version is required; refresh and retry")
	}
	rootFD, release, err := acquireTaskVaultWriteLockFD(vaultRoot)
	if err != nil {
		return "", err
	}
	defer release()
	sourceFD, sourceLeaf, err := openParentAt(rootFD, source, false)
	if err != nil {
		return "", err
	}
	defer unix.Close(sourceFD)
	before, _, err := readRegularAt(sourceFD, sourceLeaf)
	if err != nil {
		return "", err
	}
	if FileVersion(before) != expectedVersion {
		return "", fmt.Errorf("file changed; refresh before moving it")
	}
	destFD, destLeaf, err := openParentAt(rootFD, destination, true)
	if err != nil {
		return "", err
	}
	defer unix.Close(destFD)
	if err := renameat2(sourceFD, sourceLeaf, destFD, destLeaf, unix.RENAME_NOREPLACE); err != nil {
		if errors.Is(err, unix.EEXIST) {
			return "", fmt.Errorf("destination %q already exists", destination)
		}
		return "", err
	}
	moved, _, verifyErr := readRegularAt(destFD, destLeaf)
	if verifyErr == nil && FileVersion(moved) == expectedVersion {
		_ = unix.Fsync(sourceFD)
		if destFD != sourceFD {
			_ = unix.Fsync(destFD)
		}
		if reindex != nil {
			if err := reindex(); err != nil {
				return "", err
			}
		}
		return destination, nil
	}
	if rollbackErr := renameat2(destFD, destLeaf, sourceFD, sourceLeaf, unix.RENAME_NOREPLACE); rollbackErr != nil {
		return "", fmt.Errorf("source changed during move and rollback failed: %v", rollbackErr)
	}
	if verifyErr != nil {
		return "", fmt.Errorf("source changed during move; restored original: %w", verifyErr)
	}
	return "", fmt.Errorf("file changed during move; restored original")
}

// WriteNoteFile is the shared atomic note-creation path used by MCP. It
// participates in the same in-process and cross-process vault lock as task and
// disposition writes and never follows path-component symlinks.
func WriteNoteFile(vaultRoot, relPath string, content []byte, overwrite bool) error {
	return WriteNoteFileAndReindex(vaultRoot, relPath, content, overwrite, nil)
}

func WriteNoteFileAndReindex(vaultRoot, relPath string, content []byte, overwrite bool, reindex func() error) error {
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
	existing, mode, existingErr := readRegularAt(parentFD, leaf)
	exists := existingErr == nil
	if existingErr != nil && !errors.Is(existingErr, unix.ENOENT) {
		return existingErr
	}
	if exists && !overwrite {
		return fmt.Errorf("%s already exists (set overwrite to replace)", relPath)
	}
	if !exists {
		mode = 0o644
	}
	if exists {
		if err := replaceExistingNoteFileAt(parentFD, leaf, existing, content, mode); err != nil {
			return err
		}
	} else {
		tmp, err := createTempAt(parentFD, leaf, content, mode)
		if err != nil {
			return err
		}
		defer unix.Unlinkat(parentFD, tmp, 0)
		if err := renameat2(parentFD, tmp, parentFD, leaf, unix.RENAME_NOREPLACE); err != nil {
			return err
		}
	}
	_ = unix.Fsync(parentFD)
	if reindex != nil {
		return reindex()
	}
	return nil
}

// ToggleInFile checks or unchecks the task on the given 1-based line of the
// note at vaultRoot/relPath, preserving frontmatter and using an atomic,
// stale-safe replacement shared by HTTP and MCP.
func ToggleInFile(vaultRoot, relPath string, line int, expectedVersion string, done bool, now time.Time) (bool, error) {
	return ToggleInFileAndReindex(vaultRoot, relPath, line, expectedVersion, done, now, nil)
}

// ToggleInFileAndReindex keeps the exact-file check, atomic mutation, and
// synchronous index refresh in one shared vault critical section.
func ToggleInFileAndReindex(vaultRoot, relPath string, line int, expectedVersion string, done bool, now time.Time, reindex func() error) (bool, error) {
	var recurred bool
	err := mutateTaskFileAndReindex(vaultRoot, relPath, func(src []byte) ([]byte, error) {
		decoded, decodeErr := hex.DecodeString(expectedVersion)
		if decodeErr != nil || len(decoded) != sha256.Size {
			return nil, fmt.Errorf("task version is required; refresh and retry")
		}
		if FileVersion(src) != expectedVersion {
			return nil, fmt.Errorf("task file changed; refresh and retry")
		}
		_, body := vault.ParseFrontmatter(src)
		prefix := src[:len(src)-len(body)]
		newBody, didRecur, err := ToggleLine(body, line, done, now)
		if err != nil {
			return nil, err
		}
		recurred = didRecur
		return append(append([]byte{}, prefix...), newBody...), nil
	}, reindex)
	return recurred, err
}

// UpdateDueDateInFile sets, changes, or clears one task due date while
// preserving frontmatter and using the shared stale-safe atomic writer.
func UpdateDueDateInFile(vaultRoot, relPath string, line int, expectedText, expectedStatus, expectedDue, due string) error {
	return UpdateDueDateInFileAndReindex(vaultRoot, relPath, line, expectedText, expectedStatus, expectedDue, due, nil)
}

func UpdateDueDateInFileAndReindex(vaultRoot, relPath string, line int, expectedText, expectedStatus, expectedDue, due string, reindex func() error) error {
	return mutateTaskFileAndReindex(vaultRoot, relPath, func(src []byte) ([]byte, error) {
		_, body := vault.ParseFrontmatter(src)
		prefix := src[:len(src)-len(body)]
		newBody, err := UpdateDueDateLine(body, line, expectedText, expectedStatus, expectedDue, due)
		if err != nil {
			return nil, err
		}
		return append(append([]byte{}, prefix...), newBody...), nil
	}, reindex)
}
