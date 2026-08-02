package mcp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/base698/amythest/internal/tasks"
	"github.com/base698/amythest/internal/vault"
)

func TestToggleTaskRejectsStaleVersionWithoutRecurrenceOrReindex(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "Project.md")
	original := []byte("- [ ] Recurring 🔁 every week\n- [ ] Other\n")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	v, err := vault.Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	shifted := []byte("- [ ] Inserted\n- [ ] Recurring 🔁 every week\n- [ ] Other\n")
	if err := os.WriteFile(path, shifted, 0o644); err != nil {
		t.Fatal(err)
	}
	rescans := 0
	out, err := toggleTask(Deps{Vault: func() *vault.Vault { return v }, Rescan: func() error {
		rescans++
		return nil
	}}, toggleTaskIn{Slug: "Project", Line: 1, ExpectedVersion: tasks.FileVersion(original), Done: true})
	if err == nil {
		t.Fatal("expected stale version rejection")
	}
	if out.Recurred || rescans != 0 {
		t.Fatalf("stale MCP toggle recurred=%v rescans=%d", out.Recurred, rescans)
	}
	if got, readErr := os.ReadFile(path); readErr != nil || string(got) != string(shifted) {
		t.Fatalf("stale MCP toggle changed shifted task: %q err=%v", got, readErr)
	}
}
