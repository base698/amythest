package tasks

import (
	"path/filepath"
	"testing"
)

// tempVaultDir returns a canonical temporary vault root. openVaultRoot rejects
// symlinks in every path component, and on macOS t.TempDir() itself lives
// under the /var -> /private/var symlink.
func tempVaultDir(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}
