package tasks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/base698/amythest/internal/vault"
)

func TestSetNotePropertyAtVersionWritesAndReindexesOnce(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	original := []byte("---\nstatus: active\n---\nBody text.\n")
	path := filepath.Join(root, "Project.md")
	if err := os.WriteFile(path, original, 0o640); err != nil {
		t.Fatal(err)
	}
	reindexed := 0
	err = SetNotePropertyTypedAtVersionAndReindex(root, "Project.md", "status", "done", "string", FileVersion(original), func() error {
		reindexed++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if reindexed != 1 {
		t.Fatalf("reindex calls = %d, want 1", reindexed)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fm, body := vault.ParseFrontmatter(after)
	if fm.MetaString("status") != "done" || string(body) != "Body text.\n" {
		t.Fatalf("mutation result meta=%#v body=%q", fm.Meta, body)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %o, want 640", info.Mode().Perm())
	}
}

func TestSetNotePropertyAtVersionRejectsStaleWriteBeforeReindex(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	original := []byte("---\nstatus: active\n---\nBody text.\n")
	changed := []byte("---\nstatus: paused\n---\nBody changed elsewhere.\n")
	path := filepath.Join(root, "Project.md")
	if err := os.WriteFile(path, changed, 0o644); err != nil {
		t.Fatal(err)
	}
	reindexed := 0
	err = SetNotePropertyTypedAtVersionAndReindex(root, "Project.md", "status", "done", "string", FileVersion(original), func() error {
		reindexed++
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "note changed") {
		t.Fatalf("stale error = %v", err)
	}
	if reindexed != 0 {
		t.Fatalf("stale write reindexed %d times", reindexed)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(changed) {
		t.Fatalf("stale write changed file to %q", after)
	}
}

func TestTypedFrontmatterStringCannotInjectYAML(t *testing.T) {
	src := []byte("---\nstatus: active\n---\nBody text.\n")
	value := "safe\nevil: true\n---\nnot body"
	node, err := typedScalarValueNode(value, "string")
	if err != nil {
		t.Fatal(err)
	}
	out, err := setFrontmatterScalar(src, "caption", node)
	if err != nil {
		t.Fatal(err)
	}
	fm, body := vault.ParseFrontmatter(out)
	if fm.Err != nil || fm.MetaString("caption") != value {
		t.Fatalf("caption was not preserved as one string: meta=%#v err=%v", fm.Meta, fm.Err)
	}
	if string(body) != "Body text.\n" {
		t.Fatalf("body altered or injected: %q", body)
	}
}

func TestClearFrontmatterPropertyRemovesKeyAndPreservesBody(t *testing.T) {
	src := []byte("---\ntasks: false\ntitle: Widget\n---\nBody text.\n")
	out, err := setFrontmatterScalar(src, "tasks", nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "tasks:") || !strings.Contains(string(out), "title: Widget") {
		t.Fatalf("cleared frontmatter = %q", out)
	}
	if !strings.HasSuffix(string(out), "Body text.\n") {
		t.Fatalf("body altered: %q", out)
	}
}

func TestSetFrontmatterScalar(t *testing.T) {
	src := []byte("---\ntitle: Widget\nweight: 2\n---\nBody text.\n")

	// update an existing key with float inference
	out, err := setFrontmatterScalar(src, "weight", scalarValueNode("0.8"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "weight: 0.8") || !strings.Contains(string(out), "title: Widget") {
		t.Errorf("update = %q", out)
	}
	if !strings.HasSuffix(string(out), "Body text.\n") {
		t.Errorf("body altered: %q", out)
	}

	// add a new key with string typing
	out, err = setFrontmatterScalar(src, "status", scalarValueNode("in review"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "status: in review") {
		t.Errorf("add = %q", out)
	}

	// bool inference
	out, err = setFrontmatterScalar(src, "complex", scalarValueNode("true"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "complex: true") {
		t.Errorf("bool = %q", out)
	}

	// no-op when the value already matches
	same, err := setFrontmatterScalar(src, "weight", scalarValueNode("2"))
	if err != nil {
		t.Fatal(err)
	}
	if string(same) != string(src) {
		t.Errorf("expected no-op, got %q", same)
	}

	// note without frontmatter gains a block
	out, err = setFrontmatterScalar([]byte("Just a body.\n"), "rating", scalarValueNode("5"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(out), "---\nrating: 5\n---\n") || !strings.HasSuffix(string(out), "Just a body.\n") {
		t.Errorf("new frontmatter = %q", out)
	}
}
