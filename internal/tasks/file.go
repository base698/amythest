package tasks

import (
	"os"
	"path/filepath"
	"time"

	"github.com/base698/amythest/internal/vault"
)

// ToggleInFile checks or unchecks the task on the given 1-based line of the
// note at vaultRoot/relPath, writing atomically (temp + rename) and leaving any
// frontmatter block untouched. Reports whether completing a recurring task also
// inserted its next occurrence.
//
// Shared by the HTTP write path and the MCP toggle_task tool so an agent and
// the web UI apply identical recurrence and file-safety semantics.
func ToggleInFile(vaultRoot, relPath string, line int, done bool, now time.Time) (bool, error) {
	abs := filepath.Join(vaultRoot, filepath.FromSlash(relPath))
	src, err := os.ReadFile(abs)
	if err != nil {
		return false, err
	}
	_, body := vault.ParseFrontmatter(src)
	prefix := src[:len(src)-len(body)]

	newBody, recurred, err := ToggleLine(body, line, done, now)
	if err != nil {
		return false, err
	}

	info, err := os.Stat(abs)
	if err != nil {
		return false, err
	}
	tmp := abs + ".amythest-tmp"
	out := append(append([]byte{}, prefix...), newBody...)
	if err := os.WriteFile(tmp, out, info.Mode().Perm()); err != nil {
		return false, err
	}
	if err := os.Rename(tmp, abs); err != nil {
		_ = os.Remove(tmp)
		return false, err
	}
	return recurred, nil
}
